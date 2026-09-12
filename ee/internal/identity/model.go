// Package identity 管理本地账号、密码、会话和密码操作记录；不处理 HTTP、数据库或宿主对象。
// 本文件定义业务类型、对外枚举、业务限制、错误以及服务所需的接口。
package identity

import (
	"context"
	"encoding/base64"
	"errors"
	"regexp"
	"time"
)

// Error 是不含底层数据库或凭据内容的业务错误；HTTP 适配按值映射状态码，值同时作为事件的 reason_code。
type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrUnauthorized Error = "unauthorized"
	ErrForbidden    Error = "forbidden"
	ErrInvalid      Error = "invalid_input"
	ErrConflict     Error = "conflict"
	ErrNotFound     Error = "not_found"
	ErrLimited      Error = "rate_limited"
	ErrUnavailable  Error = "unavailable"
)

// SafeError 保留业务错误，把其他任何错误（含存储故障）折叠为 ErrUnavailable，避免泄露 SQL 或驱动信息。
func SafeError(err error) error {
	if err == nil {
		return nil
	}
	var e Error
	if errors.As(err, &e) {
		return e
	}
	return ErrUnavailable
}

// AccountStatus 是账号状态，持久化并出现在接口响应中。
type AccountStatus string

const (
	StatusActive   AccountStatus = "active"
	StatusDisabled AccountStatus = "disabled"
)

// EventAction 是密码操作事件的动作类型。
type EventAction string

const (
	ActionPasswordChange EventAction = "password_change" // 本人改密
	ActionPasswordReset  EventAction = "password_reset"  // 主管理员重置
	ActionAdminRecovery  EventAction = "admin_recovery"  // 离线恢复命令
)

// EventResult 是密码操作事件的结果。
type EventResult string

const (
	ResultSuccess EventResult = "success"
	ResultFailure EventResult = "failure"
)

// OperatorActor 是离线恢复事件的 actor_id 与 actor_name，不对应任何账号。
const OperatorActor = "operator"

// 产品规则（方案 4.1、4.3）。
const (
	DefaultInitialPassword = "123456"       // 建号与重置使用的初始密码，部署可覆盖
	MinPasswordRunes       = 8              // 新密码最少字符数
	MaxPasswordBytes       = 72             // bcrypt 只处理前 72 字节，超出拒绝而不是截断
	MaxDisplayNameRunes    = 128            // 显示名最多字符数
	DefaultSessionTTL      = 24 * time.Hour // 会话绝对有效期默认值
	MinSessionTTL          = time.Hour      // 部署可设的会话有效期下限
	MaxSessionTTL          = 7 * 24 * time.Hour
	WSTicketTTL            = 30 * time.Second // WebSocket 一次性票据有效期，HTTP 响应的 expires_in 由此推导
	DefaultPageSize        = 20               // 分页省略 limit 时的条数
	MaxPageSize            = 100
)

// 登录限流：服务给出桶与阈值，存储原子执行；HTTP 用同一窗口生成 Retry-After。
const (
	LoginRateWindow          = time.Minute
	LoginAttemptsPerIP       = 30
	LoginAttemptsPerUsername = 5
)

// 内部尺寸。
const (
	tokenBytes            = 32   // 会话 token 与 WS 票据的随机字节数
	maxLoginUsernameBytes = 512  // 登录输入超过此长度视为凭据错误，不查库也不计入限流
	maxLoginPasswordBytes = 1024 // 同上
	maxConcurrentHashes   = 4    // 每进程并行 bcrypt 上限，超出返回 ErrLimited
	maxCursorBytes        = 256  // 分页游标解码后的最大字节数
)

// tokenLength 是 token 经 base64url 无填充编码后的长度，用于在查库前拒绝形状不对的凭据。
var tokenLength = base64.RawURLEncoding.EncodedLen(tokenBytes)

// usernamePattern 只约束新建账号；迁移的旧用户名原样保留并允许登录。
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{2,63}$`)

// uuidPattern 校验调用方提供的账号 ID 与 operation_id。
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Principal 是已验证会话的身份句柄；服务端每次操作都按 SessionID 重新核对，不信任调用方构造的字段。
type Principal struct {
	AccountID          string
	SessionID          string
	AuthVersion        int64
	MustChangePassword bool
}

// Account 是允许对外展示的账号信息，不含凭据。
type Account struct {
	ID                 string
	Username           string
	DisplayName        string
	Status             AccountStatus
	MustChangePassword bool
}

// AccountRecord 是账号的完整记录，只在服务与存储之间传递。
type AccountRecord struct {
	Account
	PasswordHash string
	AuthVersion  int64 // 密码或状态每变更一次加一，会话签发时的版本不匹配即失效
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// State 是身份单例状态：是否已初始化以及主管理员账号。
type State struct {
	Initialized    bool
	ChiefAccountID string
}

// Session 只保存 token 的摘要；撤销通过 RevokedAt 标记，记录不删除。
type Session struct {
	ID                string
	TokenHash         string
	AccountID         string
	IssuedAuthVersion int64
	ExpiresAt         time.Time
	CreatedAt         time.Time
	RevokedAt         *time.Time
}

// AuthRecord 是一次读取得到的会话及其账号，避免分两次查询读到不一致的版本。
type AuthRecord struct {
	Session Session
	Account AccountRecord
}

// IssuedSession 是登录结果；Token 只在此处返回一次，由 HTTP 适配写入 Cookie。
type IssuedSession struct {
	Principal Principal
	Account   Account
	Token     string
	ExpiresAt time.Time
}

// PasswordEvent 是密码操作记录；账号名是操作时的快照，记录不随账号停用删除。
type PasswordEvent struct {
	ID          string
	OperationID string // 重置由调用方提供的 UUID，其他事件由服务生成
	ActorID     string
	ActorName   string
	TargetID    string
	TargetName  string
	Action      EventAction
	Result      EventResult
	ReasonCode  string // 失败时为业务 Error 的值，成功为空
	OccurredAt  time.Time
}

// Ticket 是一次性 WebSocket 握手票据；消费状态由存储维护。
type Ticket struct {
	Hash      string
	SessionID string
	ExpiresAt time.Time
}

// Cursor 是按时间与 ID 倒序分页的位置。
type Cursor struct {
	At time.Time
	ID string
}

// AccountPage 与 EventPage 的 NextCursor 为空表示没有更多。
type AccountPage struct {
	Items      []Account
	NextCursor string
}

type EventPage struct {
	Items      []PasswordEvent
	NextCursor string
}

// LoginLimit 是一个限流桶：Key 为不可逆摘要，同一窗口内最多 Max 次。
type LoginLimit struct {
	Key    string
	Max    int
	Window time.Duration
}

// Repository 由存储实现；读取方法在事务外执行，写入通过 Transaction。
// 读取未找到时返回 ErrNotFound，同时返回的值是零值，调用方不得使用；存储故障原样返回，由服务折叠为 ErrUnavailable。
type Repository interface {
	// State 读取单例状态；迁移完成后该行必然存在。
	State(context.Context) (State, error)
	// RecordByName 按登录名读取完整账号记录。
	RecordByName(context.Context, string) (AccountRecord, error)
	// AuthByHash 按 token 摘要一次读出会话及其账号；AuthByID 按会话 ID 读出同样的记录。
	AuthByHash(context.Context, string) (AuthRecord, error)
	AuthByID(context.Context, string) (AuthRecord, error)
	// Transaction 在锁定 state 的事务中执行 fn；fn 返回错误则整体回滚。
	Transaction(context.Context, func(Tx) error) error
	// ReserveLogin 原子地为全部桶各预占一次；任一桶超限返回 ErrLimited 且不预占。
	ReserveLogin(context.Context, []LoginLimit, time.Time) error
	// Accounts 从游标之后按创建时间与 ID 倒序读取最多 n 条；游标为零值表示从头开始。
	Accounts(context.Context, Cursor, int) ([]AccountRecord, error)
	// Events 按目标账号筛选，target 为空表示全部。
	Events(context.Context, string, Cursor, int) ([]PasswordEvent, error)
}

// Tx 是一个已锁定 state 的身份事务；读取未找到时返回 ErrNotFound。
type Tx interface {
	State() State
	SaveState(State) error
	Account(string) (AccountRecord, error)
	AccountByName(string) (AccountRecord, error)
	// InsertAccount 与 InsertEvent 在唯一键冲突时返回 ErrConflict。
	InsertAccount(AccountRecord) error
	SaveAccount(AccountRecord) error
	AuthByID(string) (AuthRecord, error)
	InsertSession(Session) error
	RevokeSession(string, time.Time) error
	RevokeSessions(string, time.Time) error
	Event(string) (PasswordEvent, error)
	InsertEvent(PasswordEvent) error
	InsertTicket(Ticket) error
	// ConsumeTicket 只成功一次，返回票据绑定的会话 ID；已消费、过期或不存在返回 ErrUnauthorized。
	ConsumeTicket(string, time.Time) (string, error)
}

// PasswordHasher 由存储适配注入宿主的 bcrypt 实现。
type PasswordHasher interface {
	Hash(string) (string, error)
	Compare(string, string) (bool, error)
	ValidHash(string) bool
}

// AccountPolicy 决定谁能管理账号；State 由服务在同一读取或事务中提供，后续 RBAC 通过装配替换。
// 受限会话（仍需改密）的限制由服务执行，策略不必重复检查。
type AccountPolicy interface {
	RequireAccountManager(State, Principal) error
	CanReadAllPasswordEvents(State, Principal) (bool, error)
}

// ChiefPolicy 是 RBAC 接入前的暂行策略：只有主管理员能管理账号并查看全部事件。
type ChiefPolicy struct{}

func (ChiefPolicy) RequireAccountManager(state State, p Principal) error {
	if !state.Initialized || p.AccountID != state.ChiefAccountID {
		return ErrForbidden
	}
	return nil
}

func (c ChiefPolicy) CanReadAllPasswordEvents(state State, p Principal) (bool, error) {
	return c.RequireAccountManager(state, p) == nil, nil
}

// 诊断关联：入口为一次操作分配 ID，存储在失败时连同操作名一起记录，日志不含凭据或 HTTP 对象。

type diagnosticOperationKey struct{}

type diagnosticOperation struct{ Name, ID string }

// WithDiagnosticOperation 为一次入口操作分配关联 ID。
func WithDiagnosticOperation(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, diagnosticOperationKey{}, diagnosticOperation{Name: name, ID: randomID()})
}

// DiagnosticOperation 返回操作名与关联 ID；没有分配时返回固定占位值。
func DiagnosticOperation(ctx context.Context) (name, id string) {
	if v, ok := ctx.Value(diagnosticOperationKey{}).(diagnosticOperation); ok {
		return v.Name, v.ID
	}
	return "identity", "none"
}
