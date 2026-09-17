// Package rbac 管理角色、给账号分配角色，并检查账号能做哪些操作。登录和密码由身份模块负责。
package rbac

import "context"

// 本文件约定业务代码需要哪些数据读写能力；数据库实现负责满足这些约定。

// Queries 提供一次读取中需要的查询。所有查询使用同一个数据库连接，看到同一时刻的数据。
// 查找不存在的账号或角色时，返回 ErrNotFound。
type Queries interface {
	// State 读取系统是否已初始化，以及离线恢复使用的账号编号。
	State() State
	// Revalidate 核实登录会话属于该账号、仍然有效，并检查账号状态和版本；不能只凭传入的账号编号认人。
	Revalidate(subject Subject) (AccountInfo, error)
	// Account 按编号读取账号信息，不返回密码或登录凭据。
	Account(accountID string) (AccountInfo, error)
	// CountActiveAccounts 统计这些账号中有多少处于启用状态，同一账号只算一次；需要改密码但仍启用的账号也算。
	CountActiveAccounts(accountIDs []string) (int, error)
	// Role 读取数据库里的角色。主管理员在库中不保存逐项权限，由本包读取后补成全部权限。
	Role(roleID RoleID) (Role, error)
	// Roles 按编号从大到小取最多 limit 个角色，只取编号小于 beforeID 的；beforeID 为0时从头取。
	Roles(beforeID RoleID, limit int) ([]Role, error)
	// AccountRoles 列出账号的角色，按编号从小到大排列，不重复；没有分配角色时返回空列表。
	AccountRoles(accountID string) ([]Role, error)
	// RoleAccountIDs 列出所有使用该角色的账号编号，包括已停用的账号。
	RoleAccountIDs(roleID RoleID) ([]string, error)
}

// Tx 提供同一事务中的角色读写操作。必须先锁住身份模块的 state 记录，与账号修改共用这把锁。
// 这样多个请求就不能同时通过“还剩一个主管理员”的检查，再各自移除一个。这里不修改身份模块的数据表。
type Tx interface {
	Queries
	// InsertRole 保存新角色并分配一个新编号；已删除角色的编号不能再用。
	InsertRole(input RoleInput) (Role, error)
	// UpdateRole 用提交的内容替换角色的名称、说明和全部权限。
	UpdateRole(roleID RoleID, input RoleInput) (Role, error)
	// DeleteRole 删除角色；调用前，业务代码必须确认没有账号使用它。
	DeleteRole(roleID RoleID) error
	// ReplaceAccountRoles 用新列表替换账号的全部角色；传空列表就是移除所有角色。
	ReplaceAccountRoles(accountID string, roleIDs []RoleID) error
	// AddAccountRole 给账号添加一个角色，已有就不重复添加，也不移除其他角色。
	AddAccountRole(accountID string, roleID RoleID) error
}

// Repository 负责打开和结束数据库读取或事务。传给 fn 的查询、写入对象，只能在 fn 执行期间使用。
type Repository interface {
	// Read 从主数据库读取，让 fn 中的查询看到同一时刻的数据；不使用缓存或只读副本，也不修改业务数据。
	Read(ctx context.Context, fn func(Queries) error) error
	// Write 取得与身份模块共用的写锁后执行 fn；只有全部成功才提交，任何一步失败都撤销本次事务的修改。
	Write(ctx context.Context, fn func(Tx) error) error
}
