// 本文件实现账号生命周期、密码与会话一致性规则；所有需要身份的操作都按会话重新核对。
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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

// BootstrapLegacy 在未初始化时把旧管理员凭据导入为主管理员，用户名与哈希字节原样保留；已初始化则忽略输入。
func (s *Service) BootstrapLegacy(ctx context.Context, username, passwordHash string) error {
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		if tx.State().Initialized {
			return nil
		}
		if username == "" || !s.hasher.ValidHash(passwordHash) {
			return ErrInvalid
		}
		c := Credential{Account: Account{ID: randomID(), Username: username, Status: StatusActive},
			PasswordHash: passwordHash, AuthVersion: 1, CreatedAt: now(), UpdatedAt: now()}
		if err := tx.InsertAccount(c); err != nil {
			return err
		}
		return tx.SaveState(State{Initialized: true, ChiefAccountID: c.ID})
	}))
}

// Initialize 用部署初始化密钥创建主管理员；只允许成功一次，不签发会话。
func (s *Service) Initialize(ctx context.Context, setupToken, username, password string) (Account, error) {
	state, err := s.State(ctx)
	if err != nil {
		return Account{}, err
	}
	if state.Initialized {
		return Account{}, ErrConflict
	}
	provided, expected := sha256.Sum256([]byte(setupToken)), sha256.Sum256([]byte(s.options.SetupToken))
	if s.options.SetupToken == "" || subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
		return Account{}, ErrForbidden
	}
	if !usernamePattern.MatchString(username) || !s.validPassword(password) {
		return Account{}, ErrInvalid
	}
	hash, err := s.hash(password)
	if err != nil {
		return Account{}, err
	}
	var out Account
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		// 两个节点同时初始化时，后进入事务的一方在这里看到已初始化。
		if tx.State().Initialized {
			return ErrConflict
		}
		c := Credential{Account: Account{ID: randomID(), Username: username, Status: StatusActive},
			PasswordHash: hash, AuthVersion: 1, CreatedAt: now(), UpdatedAt: now()}
		if err := tx.InsertAccount(c); err != nil {
			return err
		}
		if err := tx.SaveState(State{Initialized: true, ChiefAccountID: c.ID}); err != nil {
			return err
		}
		out = c.Account
		return nil
	})
	return out, SafeError(err)
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

// CreateAccount 由主管理员建号，使用部署初始密码并要求首次登录改密。
// 用户名重复返回 ErrConflict；哈希在事务外计算，事务内再次核对调用方权限。
func (s *Service) CreateAccount(ctx context.Context, p Principal, username, displayName string) (Account, error) {
	if err := s.RequireAccountManager(ctx, p); err != nil {
		return Account{}, err
	}
	if !usernamePattern.MatchString(username) || !utf8.ValidString(displayName) || utf8.RuneCountInString(displayName) > MaxDisplayNameRunes {
		return Account{}, ErrInvalid
	}
	hash, err := s.hash(s.options.InitialPassword)
	if err != nil {
		return Account{}, err
	}
	var out Account
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		if _, err := s.manager(tx, p); err != nil {
			return err
		}
		if _, err := tx.AccountByName(username); err == nil {
			return ErrConflict
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		c := Credential{Account: Account{ID: randomID(), Username: username, DisplayName: displayName, Status: StatusActive, MustChangePassword: true},
			PasswordHash: hash, AuthVersion: 1, CreatedAt: now(), UpdatedAt: now()}
		if err := tx.InsertAccount(c); err != nil {
			return err
		}
		out = c.Account
		return nil
	})
	return out, SafeError(err)
}

// SetAccountStatus 启用或停用账号并撤销其全部会话；同状态幂等，不升版本也不撤销。
// 主管理员不能停用自己；启用不恢复旧会话。
func (s *Service) SetAccountStatus(ctx context.Context, p Principal, id string, status AccountStatus) (Account, error) {
	if status != StatusActive && status != StatusDisabled {
		return Account{}, ErrInvalid
	}
	var out Account
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		if _, err := s.manager(tx, p); err != nil {
			return err
		}
		if id == p.AccountID && status == StatusDisabled {
			return ErrForbidden
		}
		c, err := tx.Account(id)
		if err != nil {
			return err
		}
		if c.Status != status {
			c.Status = status
			c.AuthVersion++
			c.UpdatedAt = now()
			if err = tx.SaveAccount(c); err != nil {
				return err
			}
			if err = tx.RevokeSessions(c.ID, now()); err != nil {
				return err
			}
		}
		out = c.Account
		return nil
	})
	return out, SafeError(err)
}

// ChangePassword 校验旧密码后替换本人密码并撤销全部会话，调用方随后须重新登录。
// 旧密码错误返回 ErrInvalid 且不改变任何状态；比较期间密码被他人重置则返回 ErrConflict。
func (s *Service) ChangePassword(ctx context.Context, p Principal, oldPassword, newPassword string) error {
	if !s.validPassword(newPassword) || newPassword == oldPassword {
		return ErrInvalid
	}
	_, before, err := s.current(ctx, p)
	if err != nil {
		return err
	}
	ok, err := s.compare(before.PasswordHash, oldPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalid
	}
	hash, err := s.hash(newPassword)
	if err != nil {
		return err
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		_, c, err := current(tx.AuthByID, p)
		if err != nil {
			return err
		}
		if c.PasswordHash != before.PasswordHash {
			return ErrConflict
		}
		_, err = s.replacePassword(tx, c, hash, false,
			PasswordEvent{ActorID: c.ID, ActorName: c.Username, TargetID: c.ID, TargetName: c.Username, Action: ActionPasswordChange})
		return err
	}))
}

// replacePassword 是三种改密路径共用的事务尾部：写新哈希、升版本、撤销目标全部会话、记录成功事件，返回写入的事件。
func (s *Service) replacePassword(tx Tx, c Credential, hash string, mustChange bool, event PasswordEvent) (PasswordEvent, error) {
	c.PasswordHash = hash
	c.AuthVersion++
	c.MustChangePassword = mustChange
	c.UpdatedAt = now()
	if err := tx.SaveAccount(c); err != nil {
		return PasswordEvent{}, err
	}
	if err := tx.RevokeSessions(c.ID, now()); err != nil {
		return PasswordEvent{}, err
	}
	if event.ID == "" {
		event.ID = randomID()
	}
	if event.OperationID == "" {
		event.OperationID = randomID()
	}
	event.OccurredAt = now()
	event.Result = ResultSuccess
	return event, tx.InsertEvent(event)
}

// ResetPassword 由主管理员把目标密码重置为部署初始密码并强制改密，成功事件与改密同事务提交。
// 权限、目标或自我重置等业务失败会写入失败事件并提交，随后作为错误返回；只有存储故障才回滚。
// 同一 actor/target 用同一 operation_id 重试时原样返回首次结果，不再改密；同 ID 不同 actor/target 返回 ErrConflict。
func (s *Service) ResetPassword(ctx context.Context, p Principal, targetID, operationID string) (PasswordEvent, error) {
	if !uuidPattern.MatchString(operationID) || !uuidPattern.MatchString(targetID) {
		return PasswordEvent{}, ErrInvalid
	}
	hash, err := s.hash(s.options.InitialPassword)
	if err != nil {
		return PasswordEvent{}, err
	}
	var event PasswordEvent
	var outcome error
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		actual, actor, err := current(tx.AuthByID, p)
		if err != nil {
			return err
		}
		prev, err := tx.Event(operationID)
		switch {
		case err == nil:
			if prev.ActorID != p.AccountID || prev.TargetID != targetID {
				return ErrConflict
			}
			event, outcome = prev, nil
			if prev.Result == ResultFailure {
				outcome = Error(prev.ReasonCode)
			}
			return nil
		case !errors.Is(err, ErrNotFound):
			return err
		}
		event = PasswordEvent{ID: randomID(), OperationID: operationID, ActorID: actor.ID, ActorName: actor.Username,
			TargetID: targetID, Action: ActionPasswordReset, OccurredAt: now()}
		target, err := tx.Account(targetID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		event.TargetName = target.Username
		outcome = s.authorize(tx.State(), actual)
		switch {
		case outcome != nil:
		case targetID == p.AccountID:
			outcome = ErrForbidden // 主管理员改自己的密码走本人改密
		case err != nil:
			outcome = ErrNotFound
		}
		if outcome != nil {
			event.Result, event.ReasonCode = ResultFailure, SafeError(outcome).Error()
			return tx.InsertEvent(event)
		}
		event, err = s.replacePassword(tx, target, hash, true, event)
		return err
	})
	if err != nil {
		return PasswordEvent{}, SafeError(err)
	}
	return event, outcome
}

// RecoverAdmin 仅由离线命令调用：重设主管理员密码、启用账号并撤销全部会话，事件的操作者记为 operator。
func (s *Service) RecoverAdmin(ctx context.Context, password string) error {
	if !s.validPassword(password) {
		return ErrInvalid
	}
	hash, err := s.hash(password)
	if err != nil {
		return err
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		state := tx.State()
		if !state.Initialized {
			return ErrNotFound
		}
		c, err := tx.Account(state.ChiefAccountID)
		if err != nil {
			return err
		}
		c.Status = StatusActive
		_, err = s.replacePassword(tx, c, hash, false,
			PasswordEvent{ActorID: OperatorActor, ActorName: OperatorActor, TargetID: c.ID, TargetName: c.Username, Action: ActionAdminRecovery})
		return err
	}))
}

// page 解析分页参数：limit 必须在 1 到 MaxPageSize 之间，游标为空或可解码。
func page(cursor string, limit int) (Cursor, int, error) {
	if limit < 1 || limit > MaxPageSize {
		return Cursor{}, 0, ErrInvalid
	}
	var c Cursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > maxCursorBytes {
			return Cursor{}, 0, ErrInvalid
		}
		if json.Unmarshal(raw, &c) != nil || c.At.IsZero() || !uuidPattern.MatchString(c.ID) {
			return Cursor{}, 0, ErrInvalid
		}
	}
	return c, limit, nil
}

func encodeCursor(at time.Time, id string) string {
	b, _ := json.Marshal(Cursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

// ListAccounts 由主管理员分页读取账号；多取一条判断是否还有下一页。
func (s *Service) ListAccounts(ctx context.Context, p Principal, cursor string, limit int) (AccountPage, error) {
	out := AccountPage{Items: []Account{}}
	if err := s.RequireAccountManager(ctx, p); err != nil {
		return out, err
	}
	c, n, err := page(cursor, limit)
	if err != nil {
		return out, err
	}
	rows, err := s.repo.Accounts(ctx, c, n+1)
	if err != nil {
		return out, SafeError(err)
	}
	if len(rows) > n {
		out.NextCursor = encodeCursor(rows[n-1].CreatedAt, rows[n-1].ID)
		rows = rows[:n]
	}
	for _, r := range rows {
		out.Items = append(out.Items, r.Account)
	}
	return out, nil
}

// ListPasswordEvents 分页读取密码事件：可读全部的调用方按 target 筛选，其他调用方只能查看自己的记录，指定他人返回 ErrForbidden。
func (s *Service) ListPasswordEvents(ctx context.Context, p Principal, target, cursor string, limit int) (EventPage, error) {
	out := EventPage{Items: []PasswordEvent{}}
	actual, _, err := s.current(ctx, p)
	if err != nil {
		return out, err
	}
	if err := fullSession(actual); err != nil {
		return out, err
	}
	state, err := s.State(ctx)
	if err != nil {
		return out, err
	}
	all, err := s.policy.CanReadAllPasswordEvents(state, actual)
	if err != nil {
		return out, err
	}
	if !all {
		if target != "" && target != p.AccountID {
			return out, ErrForbidden
		}
		target = p.AccountID
	}
	c, n, err := page(cursor, limit)
	if err != nil {
		return out, err
	}
	rows, err := s.repo.Events(ctx, target, c, n+1)
	if err != nil {
		return out, SafeError(err)
	}
	if len(rows) > n {
		out.NextCursor = encodeCursor(rows[n-1].OccurredAt, rows[n-1].ID)
		rows = rows[:n]
	}
	out.Items = rows
	return out, nil
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
