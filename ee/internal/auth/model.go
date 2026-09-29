// Package auth 负责登录与会话身份验证；账号修改、角色权限和票据授权由各业务入口负责。
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/session"
	"gorm.io/gorm"
)

// Error 是可直接映射为接口状态的不含凭据和存储细节的业务错误。
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

// SafeError 保留认证和会话业务错误，其余故障统一隐藏为服务不可用。
func SafeError(err error) error {
	if err == nil {
		return nil
	}
	var authError Error
	if errors.As(err, &authError) {
		return authError
	}
	var sessionError session.Error
	if errors.As(err, &sessionError) {
		return sessionError
	}
	return ErrUnavailable
}

// AccountStatus 是认证读取的账号状态；沿用已有持久化与接口值。
type AccountStatus string

// StatusActive 是允许登录和验证会话的账号状态。
const StatusActive AccountStatus = "active"

// 登录限流沿用账号认证方案：一个窗口内同时限制来源 IP 和登录名。
const (
	LoginRateWindow          = time.Minute
	LoginAttemptsPerIP       = 30
	LoginAttemptsPerUsername = 5
	maxLoginUsernameBytes    = 512
	maxLoginPasswordBytes    = 1024
)

// User 是可交给接口层转换的账号资料，不含密码和认证版本。
type User struct {
	ID                 string
	Username           string
	DisplayName        string
	Status             AccountStatus
	MustChangePassword bool
	CreatedAt          time.Time
	LastLoginAt        *time.Time
}

// Credentials 是认证所需的账号完整记录，不能直接作为 HTTP 响应。
type Credentials struct {
	User
	PasswordHash string
	AuthVersion  int64
	UpdatedAt    time.Time
}

// Record 是一次联合查询得到的账号与会话，避免分开读取时版本不一致。
type Record struct {
	Account Credentials
	Session session.Record
}

// LoginResult 返回新会话；Token 仅供接口层设置 Cookie，不写入数据库或日志。
type LoginResult struct {
	Principal session.Principal
	Account   User
	Token     string
	ExpiresAt time.Time
}

// LoginLimit 是一个限流桶；Key 只保存登录名或 IP 的不可逆摘要。
type LoginLimit struct {
	Key    string
	Max    int
	Window time.Duration
}

// Store 读取认证记录，只写最近登录时间和登录限流桶。
// WithTx 返回绑定当前事务的副本；不能修改共享 Store，nil 事务不能回退主连接。
type Store interface {
	WithTx(*gorm.DB) Store
	AccountByName(context.Context, string) (Credentials, error)
	AccountByID(context.Context, string) (Credentials, error)
	AuthByHash(context.Context, string) (Record, error)
	AuthByID(context.Context, string) (Record, error)
	UpdateLastLogin(ctx context.Context, accountID string, loggedAt, updatedAt time.Time) error
	ReserveLogin(context.Context, []LoginLimit, time.Time) error
}

// PasswordHasher 提供密码算法；装配方共享同一个 Passwords 实例限制进程内并发。
type PasswordHasher interface {
	Hash(string) (string, error)
	Compare(string, string) (bool, error)
	ValidHash(string) bool
}
