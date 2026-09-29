// Package identity 管理尚未迁出的本地账号、密码和密码操作记录；不处理 HTTP、数据库或宿主对象。
// 本文件定义业务类型、对外枚举、业务限制、错误以及服务所需的接口。
package identity

import (
	"context"
	"errors"
	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"gorm.io/gorm"
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
	var a auth.Error
	if errors.As(err, &a) {
		return Error(a)
	}
	var v session.Error
	if errors.As(err, &v) {
		return Error(v)
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
	ActionPasswordReset  EventAction = "password_reset"  // 管理者重置
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
	DefaultInitialPassword = "123456" // 建号与重置使用的初始密码，部署可覆盖
	MinPasswordRunes       = 8        // 新密码最少字符数
	MaxPasswordBytes       = 72       // bcrypt 只处理前 72 字节，超出拒绝而不是截断
	MaxDisplayNameRunes    = 128      // 显示名最多字符数
	DefaultPageSize        = 20       // 分页省略 limit 时的条数
	MaxPageSize            = 100
)

// 内部游标尺寸。
const maxCursorBytes = 256

// usernamePattern 只约束新建账号；迁移的旧用户名原样保留并允许登录。
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{2,63}$`)

// ValidAccountID 检查账号编号是否符合身份模块使用的UUID格式。
func ValidAccountID(id string) bool { return uuidPattern.MatchString(id) }

// uuidPattern 校验调用方提供的账号 ID 与 operation_id。
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Account 是允许对外展示的账号信息，不含凭据。
type Account struct {
	ID                 string
	Username           string
	DisplayName        string
	Status             AccountStatus
	MustChangePassword bool
	CreatedAt          time.Time  // 建号时间；账号列表按它与 ID 倒序分页
	LastLoginAt        *time.Time // 最近一次成功登录时间；从未登录或旧记录为nil
}

// AccountRecord 是账号的完整记录，只在服务与存储之间传递。
type AccountRecord struct {
	Account
	PasswordHash string
	AuthVersion  int64 // 密码或状态每变更一次加一，会话签发时的版本不匹配即失效
	UpdatedAt    time.Time
}

// State 是身份单例状态：是否已初始化以及固定恢复锚点账号。
type State struct {
	Initialized    bool
	ChiefAccountID string
}

// PasswordEvent 是密码操作记录；账号名是操作时的快照，记录不随账号停用或删除而移除。
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

// Cursor 是按时间与 ID 倒序分页的位置。
type Cursor struct {
	At time.Time
	ID string
}

// AccountPage 与 EventPage 的 Items 恒非 nil（空页为空切片），NextCursor 为空表示没有更多。
type AccountPage struct {
	Items      []Account
	NextCursor string
}

type EventPage struct {
	Items      []PasswordEvent
	NextCursor string
}

// Repository 提供账号存储；Read让一组查询看到同一份数据，Transaction让一组修改一起提交或回滚。
// 读取未找到时返回 ErrNotFound，同时返回的值是零值，调用方不得使用；存储故障原样返回，由服务折叠为 ErrUnavailable。
type Repository interface {
	// Read执行一组账号查询，传入的Queries只供本次回调使用。
	Read(context.Context, func(Queries) error) error
	// State 读取单例状态；迁移完成后该行必然存在。
	State(context.Context) (State, error)
	// RecordByName 按登录名读取完整账号记录。
	RecordByName(context.Context, string) (AccountRecord, error)
	// AuthByID 复用 auth 模块的联合查询，供尚未迁出的账号操作复验身份。
	AuthByID(context.Context, string) (auth.Record, error)
	// Transaction 在锁定 state 的事务中执行 fn；fn 返回错误则整体回滚。
	Transaction(context.Context, func(Tx) error) error
	// Accounts 从游标之后按创建时间与 ID 倒序读取最多 n 条；游标为零值表示从头开始。
	Accounts(context.Context, Cursor, int) ([]Account, error)
	// Events 按目标账号筛选，target 为空表示全部。
	Events(context.Context, string, Cursor, int) ([]PasswordEvent, error)
}

// Tx 是一个已锁定 state 的身份事务；读取未找到时返回 ErrNotFound。
type Tx interface {
	// AccessPolicy返回加入当前事务的账号操作策略；未配置时由业务服务使用默认策略。
	AccessPolicy() AccountPolicy
	State() State
	SaveState(State) error
	Account(string) (AccountRecord, error)
	AccountByName(string) (AccountRecord, error)
	// InsertAccount 与 InsertEvent 在唯一键冲突时返回 ErrConflict。
	InsertAccount(AccountRecord) error
	SaveAccount(AccountRecord) error
	// DeleteAccount 只删除账号；会话和票据由业务层显式调用 session 清理。
	DeleteAccount(string) error
	AuthByID(string) (auth.Record, error)
	// DB 供跨模块操作显式复用当前事务，不得由调用方提交。
	DB() *gorm.DB
	Event(string) (PasswordEvent, error)
	InsertEvent(PasswordEvent) error
}

// PasswordHasher 抽象密码哈希，由装配层注入；由 app 与 auth 共用并发受限的哈希器。
type PasswordHasher interface {
	Hash(string) (string, error)
	Compare(string, string) (bool, error)
	ValidHash(string) bool
}
