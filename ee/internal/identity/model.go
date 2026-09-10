// Package identity 管理本地账号、密码、会话和密码操作记录。
package identity

import (
	"context"
	"errors"
	"time"
)

// Error 是不含底层数据库或凭据内容的业务错误。
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

// SafeError 将未识别的存储故障映射为固定错误，不泄露SQL参数。
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

// WSTicketTTL 是签发和HTTP响应共用的票据有效期。
const WSTicketTTL = 30 * time.Second

// 登录限流的窗口与阈值由存储执行，HTTP据同一窗口生成Retry-After。
const (
	LoginRateWindow          = time.Minute
	LoginAttemptsPerIP       = 30
	LoginAttemptsPerUsername = 5
)

type diagnosticOperationKey struct{}
type diagnosticOperation struct{ Name, ID string }

// WithDiagnosticOperation 为一次入口操作分配关联ID，不携带凭据或HTTP对象。
func WithDiagnosticOperation(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, diagnosticOperationKey{}, diagnosticOperation{Name: name, ID: randomID()})
}
func DiagnosticOperation(ctx context.Context) (string, string) {
	if v, ok := ctx.Value(diagnosticOperationKey{}).(diagnosticOperation); ok {
		return v.Name, v.ID
	}
	return "identity", "none"
}

// Principal 仅携带身份句柄，不能凭调用方构造的字段跳过服务端校验。
type Principal struct {
	AccountID          string `json:"account_id"`
	SessionID          string `json:"-"`
	AuthVersion        int64  `json:"-"`
	MustChangePassword bool   `json:"must_change_password"`
}

// Account 是允许返回给控制台的账号信息。
type Account struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	DisplayName        string `json:"display_name"`
	Status             string `json:"status"`
	MustChangePassword bool   `json:"must_change_password"`
}

// Credential 仅用于服务与存储协作，凭据永不参与JSON输出。
type Credential struct {
	Account
	PasswordHash string    `json:"-"`
	AuthVersion  int64     `json:"-"`
	CreatedAt    time.Time `json:"-"`
	UpdatedAt    time.Time `json:"-"`
}
type State struct {
	Initialized    bool
	ChiefAccountID string
}
type Session struct {
	ID, TokenHash, AccountID string
	IssuedAuthVersion        int64
	ExpiresAt, CreatedAt     time.Time
	RevokedAt                *time.Time
}
type AuthRecord struct {
	Session    Session
	Credential Credential
}

// IssuedSession 的原始凭据只交给HTTP适配写Cookie。
type IssuedSession struct {
	Principal Principal
	Token     string `json:"-"`
	ExpiresAt time.Time
}
type PasswordEvent struct {
	ID          string    `json:"id"`
	OperationID string    `json:"operation_id"`
	ActorID     string    `json:"actor_id"`
	TargetID    string    `json:"target_id"`
	ActorName   string    `json:"actor_name"`
	TargetName  string    `json:"target_name"`
	Action      string    `json:"action"`
	Result      string    `json:"result"`
	ReasonCode  string    `json:"reason_code"`
	OccurredAt  time.Time `json:"occurred_at"`
}
type Ticket struct {
	Hash, SessionID string
	ExpiresAt       time.Time
	ConsumedAt      *time.Time
}
type Cursor struct {
	At time.Time
	ID string
}
type AccountPage struct {
	Items      []Account `json:"items"`
	NextCursor *string   `json:"next_cursor"`
}
type EventPage struct {
	Items      []PasswordEvent `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

// Repository 的事务仅针对身份业务；实现不拥有共享数据库连接。
type Repository interface {
	State(context.Context) (State, error)
	CredentialByName(context.Context, string) (Credential, error)
	AuthByHash(context.Context, string) (AuthRecord, error)
	AuthByID(context.Context, string) (AuthRecord, error)
	Transaction(context.Context, func(Tx) error) error
	ReserveLogin(context.Context, string, string, time.Time) error
	Accounts(context.Context, Cursor, int) ([]Credential, error)
	Events(context.Context, string, Cursor, int) ([]PasswordEvent, error)
}

// Tx 在同一锁定state事务中读写完整身份操作，禁止嵌套打开独立事务。
type Tx interface {
	State() State
	SaveState(State) error
	Account(string) (Credential, error)
	AccountByName(string) (Credential, error)
	InsertAccount(Credential) error
	SaveAccount(Credential) error
	AuthByID(string) (AuthRecord, error)
	InsertSession(Session) error
	RevokeSession(string, time.Time) error
	RevokeSessions(string, time.Time) error
	Event(string) (PasswordEvent, error)
	InsertEvent(PasswordEvent) error
	InsertTicket(Ticket) error
	ConsumeTicket(string, time.Time) (string, error)
}

// PasswordHasher 由适配层注入现有bcrypt实现。
type PasswordHasher interface {
	Hash(string) (string, error)
	Compare(string, string) (bool, error)
	ValidHash(string) bool
}

// AccountPolicy 供后续RBAC替换当前chief策略；通过上下文取得事务内身份快照。
type AccountPolicy interface {
	RequireAccountManager(context.Context, Principal) error
	CanReadAllPasswordEvents(context.Context, Principal) (bool, error)
}
type policyStateKey struct{}
type ChiefPolicy struct{}

func (ChiefPolicy) RequireAccountManager(ctx context.Context, p Principal) error {
	state, ok := ctx.Value(policyStateKey{}).(State)
	if !ok || !state.Initialized || p.MustChangePassword || p.AccountID != state.ChiefAccountID {
		return ErrForbidden
	}
	return nil
}
func (c ChiefPolicy) CanReadAllPasswordEvents(ctx context.Context, p Principal) (bool, error) {
	return c.RequireAccountManager(ctx, p) == nil, nil
}
