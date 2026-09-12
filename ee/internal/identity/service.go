// 本文件定义服务、共享辅助函数与会话规则：签发、验证、重验、登出与 WS 票据；账号操作见 accounts.go，密码操作见 passwords.go。
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// Options 是已由部署配置解析的认证规则，不包含全局 Server 或 Config。
type Options struct {
	InitialPassword string
	SetupToken      string
	SessionTTL      time.Duration
}

// Validate 检查部署值是否在业务允许范围内；初始密码同样受 bcrypt 字节上限约束。
func (o Options) Validate() error {
	if o.InitialPassword == "" || !utf8.ValidString(o.InitialPassword) || len(o.InitialPassword) > MaxPasswordBytes {
		return ErrInvalid
	}
	if o.SessionTTL < MinSessionTTL || o.SessionTTL > MaxSessionTTL {
		return ErrInvalid
	}
	return nil
}

// Service 执行账号规则；依赖由 app 通过构造函数注入。
type Service struct {
	repo      Repository
	hasher    PasswordHasher
	policy    AccountPolicy
	options   Options
	hashSlots chan struct{}
	dummyHash string // 未知账号也做一次比较，避免用耗时判断账号是否存在
}

// NewService 校验选项并预生成比较用的假哈希；policy 为 nil 时使用 ChiefPolicy。
func NewService(repo Repository, hasher PasswordHasher, options Options, policy AccountPolicy) (*Service, error) {
	if repo == nil || hasher == nil {
		return nil, ErrInvalid
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if policy == nil {
		policy = ChiefPolicy{}
	}
	dummy, err := hasher.Hash("dummy-verification-only")
	if err != nil {
		return nil, SafeError(err)
	}
	return &Service{repo: repo, hasher: hasher, policy: policy, options: options,
		hashSlots: make(chan struct{}, maxConcurrentHashes), dummyHash: dummy}, nil
}

func now() time.Time { return time.Now().UTC() }

// randomID 生成 UUIDv4；crypto/rand 失败时进程无法安全工作，直接终止。
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// randomToken 生成会话 token 或票据的原文，只在签发时返回给适配层。
func randomToken() string {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// digest 是存储 token、用户名与来源 IP 时使用的不可逆摘要。
func digest(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// withHashSlot 限制并行 bcrypt 计算；槽位占满时返回 ErrLimited 而不是排队。
func (s *Service) withHashSlot(fn func() error) error {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		return ErrLimited
	}
	return SafeError(fn())
}

func (s *Service) hash(password string) (hash string, err error) {
	err = s.withHashSlot(func() (e error) { hash, e = s.hasher.Hash(password); return e })
	return hash, err
}

func (s *Service) compare(hash, password string) (ok bool, err error) {
	err = s.withHashSlot(func() (e error) { ok, e = s.hasher.Compare(hash, password); return e })
	return ok, err
}

// validPassword 执行新密码规则：合法 UTF-8、字符数与字节数在范围内、不等于部署初始密码；不做 trim。
func (s *Service) validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= MinPasswordRunes &&
		len(password) <= MaxPasswordBytes && password != s.options.InitialPassword
}

// verify 判断会话记录在 at 时刻是否可用：账号存在且启用、会话未撤销未过期、签发版本与当前版本一致。
func verify(r AuthRecord, at time.Time) (Principal, error) {
	c, v := r.Credential, r.Session
	if c.ID == "" || c.Status != StatusActive || v.RevokedAt != nil || !v.ExpiresAt.After(at) || c.AuthVersion != v.IssuedAuthVersion {
		return Principal{}, ErrUnauthorized
	}
	return Principal{AccountID: c.ID, SessionID: v.ID, AuthVersion: c.AuthVersion, MustChangePassword: c.MustChangePassword}, nil
}

// resolve 把会话读取结果转换为身份；会话不存在同样视为未认证，存储故障折叠为 ErrUnavailable。
func resolve(r AuthRecord, err error) (Principal, Credential, error) {
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, Credential{}, ErrUnauthorized
		}
		return Principal{}, Credential{}, SafeError(err)
	}
	p, err := verify(r, now())
	return p, r.Credential, err
}

// current 重新读取调用方声明的会话，并核对账号与版本未变；事务内外都用这一个实现。
func current(lookup func(string) (AuthRecord, error), claimed Principal) (Principal, Credential, error) {
	actual, c, err := resolve(lookup(claimed.SessionID))
	if err == nil && (actual.AccountID != claimed.AccountID || actual.AuthVersion != claimed.AuthVersion) {
		err = ErrUnauthorized
	}
	return actual, c, err
}

func (s *Service) current(ctx context.Context, p Principal) (Principal, Credential, error) {
	return current(func(id string) (AuthRecord, error) { return s.repo.AuthByID(ctx, id) }, p)
}

// fullSession 拒绝仍需改密的受限会话；受限会话只能使用本人资料、改密和登出。
func fullSession(p Principal) error {
	if p.MustChangePassword {
		return ErrForbidden
	}
	return nil
}

// authorize 要求正常会话且具备账号管理权限。
func (s *Service) authorize(state State, p Principal) error {
	if err := fullSession(p); err != nil {
		return err
	}
	return s.policy.RequireAccountManager(state, p)
}

// manager 在事务内核对调用方仍是正常会话且有账号管理权限，返回其账号记录。
func (s *Service) manager(tx Tx, p Principal) (Credential, error) {
	actual, c, err := current(tx.AuthByID, p)
	if err != nil {
		return c, err
	}
	return c, s.authorize(tx.State(), actual)
}

// State 返回初始化状态与主管理员账号。
func (s *Service) State(ctx context.Context) (State, error) {
	v, err := s.repo.State(ctx)
	return v, SafeError(err)
}

// Authenticate 用 Cookie 中的原始 token 换取身份；形状不对的 token 不查库。
func (s *Service) Authenticate(ctx context.Context, raw string) (Principal, error) {
	if len(raw) != tokenLength {
		return Principal{}, ErrUnauthorized
	}
	p, _, err := resolve(s.repo.AuthByHash(ctx, digest(raw)))
	return p, err
}

// ValidateSession 按会话 ID 重新验证，供 WebSocket 连接在每次写入前调用；调用方不能持有旧的 RequestCtx。
func (s *Service) ValidateSession(ctx context.Context, id string) (Principal, error) {
	p, _, err := resolve(s.repo.AuthByID(ctx, id))
	return p, err
}

// RequireAccountManager 供宿主对管理路由使用：重新核对会话，并要求正常会话与管理权限。
func (s *Service) RequireAccountManager(ctx context.Context, p Principal) error {
	actual, _, err := s.current(ctx, p)
	if err != nil {
		return err
	}
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	return s.authorize(state, actual)
}

// Me 返回调用方自己的账号信息，受限会话同样可用。
func (s *Service) Me(ctx context.Context, p Principal) (Account, error) {
	_, c, err := s.current(ctx, p)
	return c.Account, err
}

// Login 先限流、再比较密码，最后在事务内重读账号后签发会话；错误用户名、密码和停用账号统一返回 ErrUnauthorized。
// 超长输入不可能是有效凭据，直接拒绝且不占用限流桶，避免用巨大请求耗尽哈希槽。
func (s *Service) Login(ctx context.Context, username, password, peerIP string) (IssuedSession, error) {
	if username == "" || len(username) > maxLoginUsernameBytes || len(password) > maxLoginPasswordBytes {
		return IssuedSession{}, ErrUnauthorized
	}
	limits := []LoginLimit{
		{Key: "ip:" + digest(peerIP), Max: LoginAttemptsPerIP, Window: LoginRateWindow},
		{Key: "name:" + digest(username), Max: LoginAttemptsPerUsername, Window: LoginRateWindow},
	}
	if err := s.repo.ReserveLogin(ctx, limits, now()); err != nil {
		return IssuedSession{}, SafeError(err)
	}
	c, err := s.repo.CredentialByName(ctx, username)
	missing := errors.Is(err, ErrNotFound)
	if err != nil && !missing {
		return IssuedSession{}, SafeError(err)
	}
	hash := c.PasswordHash
	if missing {
		hash = s.dummyHash
	}
	ok, err := s.compare(hash, password)
	if err != nil {
		return IssuedSession{}, err
	}
	if missing || !ok || c.Status != StatusActive {
		return IssuedSession{}, ErrUnauthorized
	}
	raw := randomToken()
	var out IssuedSession
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		// 比较密码期间可能发生重置或停用：签发前重读，版本或哈希变化则拒绝。
		latest, err := tx.Account(c.ID)
		if err != nil {
			return err
		}
		if latest.AuthVersion != c.AuthVersion || latest.PasswordHash != c.PasswordHash || latest.Status != StatusActive {
			return ErrUnauthorized
		}
		v := Session{ID: randomID(), TokenHash: digest(raw), AccountID: c.ID, IssuedAuthVersion: latest.AuthVersion,
			CreatedAt: now(), ExpiresAt: now().Add(s.options.SessionTTL)}
		if err = tx.InsertSession(v); err != nil {
			return err
		}
		p, err := verify(AuthRecord{Session: v, Credential: latest}, now())
		if err != nil {
			return err
		}
		out = IssuedSession{Principal: p, Account: latest.Account, Token: raw, ExpiresAt: v.ExpiresAt}
		return nil
	})
	return out, SafeError(err)
}

// Logout 撤销 token 对应的会话；空、形状不对或已失效的 token 同样视为成功，浏览器随后清除 Cookie。
func (s *Service) Logout(ctx context.Context, raw string) error {
	if len(raw) != tokenLength {
		return nil
	}
	r, err := s.repo.AuthByHash(ctx, digest(raw))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return SafeError(err)
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error { return tx.RevokeSession(r.Session.ID, now()) }))
}

// IssueTicket 为正常的主管理员会话签发一次性 WebSocket 票据，原文只返回一次。
func (s *Service) IssueTicket(ctx context.Context, p Principal) (string, error) {
	raw := randomToken()
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		if _, err := s.manager(tx, p); err != nil {
			return err
		}
		return tx.InsertTicket(Ticket{Hash: digest(raw), SessionID: p.SessionID, ExpiresAt: now().Add(WSTicketTTL)})
	})
	return raw, SafeError(err)
}

// ConsumeTicket 消费票据并返回其会话身份；票据只能用一次，且会话仍须是正常的主管理员会话。
func (s *Service) ConsumeTicket(ctx context.Context, raw string) (Principal, error) {
	if len(raw) != tokenLength {
		return Principal{}, ErrUnauthorized
	}
	var out Principal
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		id, err := tx.ConsumeTicket(digest(raw), now())
		if err != nil {
			return err
		}
		p, _, err := resolve(tx.AuthByID(id))
		if err != nil {
			return err
		}
		if err := s.authorize(tx.State(), p); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, SafeError(err)
}
