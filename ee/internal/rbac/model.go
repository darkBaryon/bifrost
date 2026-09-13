// Package rbac 管理固定权限、角色和账号角色关系，不处理身份凭据或HTTP。
package rbac

import (
	"context"
	"errors"
	"time"
)

// Error 是可对外返回的安全业务错误。
type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrInvalid      Error = "invalid_input"
	ErrUnauthorized Error = "unauthorized"
	ErrForbidden    Error = "forbidden"
	ErrNotFound     Error = "not_found"
	ErrConflict     Error = "conflict"
	ErrUnavailable  Error = "unavailable"
)

// ConflictError 带关联账号数，供删除角色接口说明不能删除的原因。
type ConflictError struct{ AccountCount int }

func (e *ConflictError) Error() string { return string(ErrConflict) }

func (e *ConflictError) Unwrap() error { return ErrConflict }

// SafeError 保留已知业务错误，屏蔽存储内部信息。
func SafeError(err error) error {
	if err == nil {
		return nil
	}
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return conflict
	}
	var business Error
	if errors.As(err, &business) {
		return business
	}
	return ErrUnavailable
}

// RoleID 是可无损传给JS与宿主通知的数值角色标识。
type RoleID uint64

// SystemCode 标识不可删除的预置角色，不随显示名变化。
type SystemCode string

// 迁移固定 system_code 与 ID 的对应关系，且业务禁止修改；识别角色语义始终用 SystemCode。
const (
	SystemChief     SystemCode = "chief"
	SystemDeveloper SystemCode = "developer"
	SystemReadonly  SystemCode = "readonly"
	ChiefRoleID     RoleID     = 1
	DeveloperRoleID RoleID     = 2
	ReadonlyRoleID  RoleID     = 3
	// MaxRoleID 来自JS安全整数范围，保证通知与HTTP数字往返无损。
	MaxRoleID RoleID = 1<<53 - 1
	// 下列首版限制来自已评审接口合同。
	MaxRoleNameRunes        = 64
	MaxRoleDescriptionRunes = 512
	MaxRolesPerAccount      = 64
	DefaultPageSize         = 20
	MaxPageSize             = 100
	MaxCursorBytes          = 128
)

// Subject 仅为身份句柄，所有服务调用都在仓储范围内重验。
type Subject struct {
	AccountID, SessionID string
	AuthVersion          int64
}

// AccountInfo 是身份适配提供的无凭据账号信息。
type AccountInfo struct {
	ID, Username, DisplayName  string
	Active, MustChangePassword bool
}

// State 保存身份初始化与恢复锚点的安全视图。
type State struct {
	Initialized    bool
	ChiefAccountID string
}

// Role 是角色及其当前权限、账号关联计数。
type Role struct {
	ID                   RoleID
	Name, Description    string
	SystemCode           SystemCode
	PermissionCodes      []Permission
	AccountCount         int
	CreatedAt, UpdatedAt time.Time
}

// RoleInput 是创建或完整替换角色的业务输入。
type RoleInput struct {
	Name, Description string
	PermissionCodes   []Permission
}

// Grant 说明所有赋予同一权限的角色来源。
type Grant struct {
	Code    Permission
	RoleIDs []RoleID
}

// Effective 是账号的当前角色、权限与来源。
type Effective struct {
	AccountID   string
	Roles       []Role
	Permissions []Grant
}

// Access 是一次请求或消息的独立授权快照，不可跨请求缓存。
type Access struct {
	AccountID   string
	RoleIDs     []RoleID
	Permissions []Permission
	Chief       bool
}

// Allows 只查询当前快照，不隐式扩大Manage或敏感权限。
func (a Access) Allows(p Permission) bool {
	for _, v := range a.Permissions {
		if p == v {
			return true
		}
	}
	return false
}

// Page 使用ID倒序游标，不保留跨请求数据库快照。
type Page struct {
	Items      []Role
	NextCursor string
}

// ReadView 绑定同一连接快照；不存在的账号/角色返回ErrNotFound。
type ReadView interface {
	// State 返回当前快照的初始化状态和固定恢复锚点。
	State() State
	// Revalidate 重验会话句柄、账号状态和密码版本，不接受调用方自报身份。
	Revalidate(subject Subject) (AccountInfo, error)
	// Account 查找账号安全字段；不包含密码或凭据。
	Account(accountID string) (AccountInfo, error)
	// CountActiveAccounts 对ID去重后计数；强制改密的active账号仍计入。
	CountActiveAccounts(accountIDs []string) (int, error)
	// Role 返回持久化角色；动态chief全权由业务层投影。
	Role(roleID RoleID) (Role, error)
	// Roles 按ID倒序读取beforeID之前的最多limit行；零游标从头开始。
	Roles(beforeID RoleID, limit int) ([]Role, error)
	// AccountRoles 返回ID升序的去重角色，未分配时为空集合。
	AccountRoles(accountID string) ([]Role, error)
	// RoleAccountIDs 返回全部关联账号ID，包含停用账号。
	RoleAccountIDs(roleID RoleID) ([]string, error)
}

// Tx 在唯一身份state写锁下操作角色表；不写身份表。
type Tx interface {
	ReadView
	// InsertRole 插入自定义角色并分配不可复用的新ID。
	InsertRole(input RoleInput) (Role, error)
	// UpdateRole 完整替换角色元信息及权限集合。
	UpdateRole(roleID RoleID, input RoleInput) (Role, error)
	// DeleteRole 仅删除业务层已确认无关联的角色。
	DeleteRole(roleID RoleID) error
	// ReplaceAccountRoles 完整替换账号角色集合；空集合表示清空。
	ReplaceAccountRoles(accountID string, roleIDs []RoleID) error
	// AddAccountRole 幂等补齐一条关系，不移除已有角色。
	AddAccountRole(accountID string, roleID RoleID) error
}

// Repository 管理读取快照和写事务的生命周期；回调对象不得逸出。
type Repository interface {
	// Read 在权威库的一致快照中调用fn，不写业务数据。
	Read(ctx context.Context, fn func(ReadView) error) error
	// Write 在统一写锁下调用fn；任何错误使整个事务回滚。
	Write(ctx context.Context, fn func(Tx) error) error
}
