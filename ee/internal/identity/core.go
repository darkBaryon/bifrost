// 本文件定义三个服务共用的核心：依赖、哈希槽、会话与权限判定、分页辅助；会话操作见 session.go，账号见 accounts.go，密码见 passwords.go。
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

// core 是三个服务共享的依赖与判定：同一个仓储、同一个哈希槽、同一份假哈希。三条线互不调用，跨线动作经 Tx 或这里的判定函数。
type core struct {
	repo      Repository
	hasher    PasswordHasher
	policy    AccountPolicy
	options   Options
	hashSlots chan struct{}
	dummyHash string // 未知账号也做一次比较，避免用耗时判断账号是否存在
}

// SessionService 是会话线：登录、验证、登出与 WebSocket 票据。
type SessionService struct{ *core }

// AccountService 是账号线：初始化、旧管理员导入、建号、启停与列表。
type AccountService struct{ *core }

// PasswordService 是密码线：本人改密、管理员重置、离线恢复与密码事件查询。
type PasswordService struct{ *core }

// Services 是 New 装配出的三条线；三者共享同一个 core，调用方按需要只持有其中一条。
type Services struct {
	Session  *SessionService
	Account  *AccountService
	Password *PasswordService
}

// New 校验选项并预生成比较用的假哈希；policy 为 nil 时使用 ChiefPolicy。
func New(repo Repository, hasher PasswordHasher, options Options, policy AccountPolicy) (*Services, error) {
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
	c := &core{repo: repo, hasher: hasher, policy: policy, options: options,
		hashSlots: make(chan struct{}, maxConcurrentHashes), dummyHash: dummy}
	return &Services{Session: &SessionService{c}, Account: &AccountService{c}, Password: &PasswordService{c}}, nil
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
func (s *core) withHashSlot(fn func() error) error {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		return ErrLimited
	}
	return SafeError(fn())
}

func (s *core) hash(password string) (hash string, err error) {
	err = s.withHashSlot(func() (e error) { hash, e = s.hasher.Hash(password); return e })
	return hash, err
}

func (s *core) compare(hash, password string) (ok bool, err error) {
	err = s.withHashSlot(func() (e error) { ok, e = s.hasher.Compare(hash, password); return e })
	return ok, err
}

// validPassword 执行新密码规则：合法 UTF-8、字符数与字节数在范围内、不等于部署初始密码；不做 trim。
func (s *core) validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= MinPasswordRunes &&
		len(password) <= MaxPasswordBytes && password != s.options.InitialPassword
}

// verify 判断会话记录在 at 时刻是否可用：账号存在且启用、会话未撤销未过期、签发版本与当前版本一致。
func verify(r AuthRecord, at time.Time) (Principal, error) {
	c, v := r.Account, r.Session
	if c.ID == "" || c.Status != StatusActive || v.RevokedAt != nil || !v.ExpiresAt.After(at) || c.AuthVersion != v.IssuedAuthVersion {
		return Principal{}, ErrUnauthorized
	}
	return Principal{AccountID: c.ID, SessionID: v.ID, AuthVersion: c.AuthVersion, MustChangePassword: c.MustChangePassword}, nil
}

// resolve 把会话读取结果转换为身份；会话不存在同样视为未认证，存储故障折叠为 ErrUnavailable。
func resolve(r AuthRecord, err error) (Principal, AccountRecord, error) {
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, AccountRecord{}, ErrUnauthorized
		}
		return Principal{}, AccountRecord{}, SafeError(err)
	}
	p, err := verify(r, now())
	return p, r.Account, err
}

// current 重新读取调用方声明的会话，并核对账号与版本未变；事务内外都用这一个实现。
func current(lookup func(string) (AuthRecord, error), claimed Principal) (Principal, AccountRecord, error) {
	actual, c, err := resolve(lookup(claimed.SessionID))
	if err == nil && (actual.AccountID != claimed.AccountID || actual.AuthVersion != claimed.AuthVersion) {
		err = ErrUnauthorized
	}
	return actual, c, err
}

func (s *core) current(ctx context.Context, p Principal) (Principal, AccountRecord, error) {
	return current(func(id string) (AuthRecord, error) { return s.repo.AuthByID(ctx, id) }, p)
}

// fullSession 拒绝仍需改密的受限会话；受限会话只能使用本人资料、改密和登出。
func fullSession(p Principal) error {
	if p.MustChangePassword {
		return ErrForbidden
	}
	return nil
}

// State 返回初始化状态与固定恢复锚点账号；经嵌入提升到三条线，外部从任意一条调用等价。
func (s *core) State(ctx context.Context) (State, error) {
	v, err := s.repo.State(ctx)
	return v, SafeError(err)
}

// RequireAccountManager 供既有宿主入口使用：重验正常会话并应用 CreateAccounts 策略；同 State，三条线上等价。
func (s *core) RequireAccountManager(ctx context.Context, p Principal) error {
	return s.requireAction(ctx, p, CreateAccounts, "")
}

// page 校验分页参数并解码游标：limit 必须在 1 到 MaxPageSize 之间，游标为空或可解码；不改写 limit。
func page(cursor string, limit int) (Cursor, error) {
	if limit < 1 || limit > MaxPageSize {
		return Cursor{}, ErrInvalid
	}
	var c Cursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > maxCursorBytes {
			return Cursor{}, ErrInvalid
		}
		if json.Unmarshal(raw, &c) != nil || c.At.IsZero() || !uuidPattern.MatchString(c.ID) {
			return Cursor{}, ErrInvalid
		}
	}
	return c, nil
}

func encodeCursor(at time.Time, id string) string {
	b, _ := json.Marshal(Cursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}
