// 本文件检查账号能做什么，并列出权限来自哪些角色；每次操作都会重新核实身份和读取权限。
package rbac

import "context"

// Subject 带着本次操作者的账号和登录信息。每次操作都要查库核实，不能只信这些传入值。
type Subject struct {
	AccountID, SessionID string
	AuthVersion          int64 // 登录时记录的账号版本；必须与数据库中的当前版本一致。
}

// AccountInfo 是从身份模块读取的账号信息，不包含密码或登录凭据。
type AccountInfo struct {
	ID, Username, DisplayName  string
	Active, MustChangePassword bool
}

// Grant 说明一个权限来自哪些角色。例如两个角色都授予查看日志权限，就记录这两个角色的编号。
type Grant struct {
	Code    Permission
	RoleIDs []RoleID
}

// Effective 用来展示账号有哪些角色、能做哪些操作，以及每个权限是哪个角色给的。
type Effective struct {
	AccountID   string
	Roles       []Role
	Permissions []Grant
}

// Access 是本次检查得到的账号权限，只供当前请求或消息使用。下一次必须重新读取，避免继续使用已撤回的权限。
type Access struct {
	AccountID   string
	RoleIDs     []RoleID
	Permissions []Permission
	Chief       bool
}

// Allows 检查这份结果里有没有指定权限，不会重新查库；有管理权限不代表有查看或敏感权限。
func (a Access) Allows(p Permission) bool {
	for _, v := range a.Permissions {
		if p == v {
			return true
		}
	}
	return false
}

// Service 提供角色管理和权限检查；读写数据所需的实现由创建它的代码传入。
type Service struct{ repo Repository }

// New 创建角色权限服务。没有传入存储实现时，后续操作返回 unavailable，不会放行。
func New(repo Repository) *Service { return &Service{repo: repo} }

// Authorize 检查当前账号有没有指定权限；传入目录里不存在的权限，一律拒绝。
func (s *Service) Authorize(ctx context.Context, actor Subject, permission Permission) error {
	if !KnownPermission(permission) {
		return ErrForbidden
	}
	return s.read(ctx, func(queries Queries) error {
		return checkPermission(queries, actor, permission)
	})
}

// checkPermission 检查操作者能否执行指定操作；身份、权限或查询有任何问题都返回错误。
func checkPermission(queries Queries, actor Subject, permission Permission) error {
	if err := checkLogin(queries, actor); err != nil {
		return err
	}
	account, err := accountPermissions(queries, actor.AccountID)
	if err != nil {
		return err
	}
	for _, grant := range account.Permissions {
		if grant.Code == permission {
			return nil
		}
	}
	return ErrForbidden
}

// checkLogin 检查操作者是否仍然登录、账号是否启用、是否已完成必须的密码修改。
func checkLogin(queries Queries, actor Subject) error {
	account, err := queries.Revalidate(actor)
	if err != nil {
		return err
	}
	if !account.Active {
		return ErrUnauthorized
	}
	if account.MustChangePassword {
		return ErrForbidden
	}
	return nil
}

// accountPermissions 只汇总目标账号的角色和权限；是否允许读取或使用这些结果，由调用它的代码检查。
func accountPermissions(queries Queries, id string) (Effective, error) {
	out := Effective{AccountID: id, Roles: []Role{}, Permissions: []Grant{}}
	if _, e := queries.Account(id); e != nil {
		return out, e
	}
	roles, e := queries.AccountRoles(id)
	if e != nil {
		return out, e
	}
	for i := range roles {
		roles[i] = presentedRole(roles[i])
	}
	out.Roles = roles
	sources := map[Permission][]RoleID{}
	for _, role := range roles {
		codes := role.PermissionCodes
		for _, code := range codes {
			sources[code] = append(sources[code], role.ID)
		}
	}
	for _, code := range Permissions() {
		if ids := sources[code]; len(ids) > 0 {
			out.Permissions = append(out.Permissions, Grant{Code: code, RoleIDs: ids})
		}
	}
	return out, nil
}

// Snapshot 核实当前登录状态并返回账号权限；后续请求或消息必须重新调用，不能复用这次结果。
func (s *Service) Snapshot(ctx context.Context, p Subject) (Access, error) {
	var out Access
	e := s.read(ctx, func(queries Queries) error {
		out = Access{AccountID: p.AccountID, RoleIDs: []RoleID{}, Permissions: []Permission{}}
		if e := checkLogin(queries, p); e != nil {
			return e
		}
		account, e := accountPermissions(queries, p.AccountID)
		if e != nil {
			return e
		}
		for _, role := range account.Roles {
			out.RoleIDs = append(out.RoleIDs, role.ID)
			out.Chief = out.Chief || role.SystemCode == SystemChief
		}
		for _, grant := range account.Permissions {
			out.Permissions = append(out.Permissions, grant.Code)
		}
		return nil
	})
	return out, e
}

// Me 让已登录、已启用且无需强制改密的账号查看自己的角色和权限，不要求 Users.View。
func (s *Service) Me(ctx context.Context, p Subject) (Effective, error) {
	var out Effective
	e := s.read(ctx, func(queries Queries) error {
		if e := checkLogin(queries, p); e != nil {
			return e
		}
		var e error
		out, e = accountPermissions(queries, p.AccountID)
		return e
	})
	return out, e
}

// GetAccountRoles 查看指定账号的角色和权限，必须有 Users.View，即使查自己也一样；无需此权限查自己请用 Me。
func (s *Service) GetAccountRoles(ctx context.Context, p Subject, id string) (Effective, error) {
	var out Effective
	e := s.read(ctx, func(queries Queries) error {
		if e := checkPermission(queries, p, UsersView); e != nil {
			return e
		}
		var e error
		out, e = accountPermissions(queries, id)
		return e
	})
	return out, e
}

func (s *Service) read(ctx context.Context, fn func(Queries) error) error {
	if s.repo == nil {
		return ErrUnavailable
	}
	return SafeError(s.repo.Read(ctx, fn))
}

func (s *Service) write(ctx context.Context, fn func(Tx) error) error {
	if s.repo == nil {
		return ErrUnavailable
	}
	return SafeError(s.repo.Write(ctx, fn))
}

// ValidateNotificationAudience 检查操作者能否发布通知，以及接收通知的角色是否都存在。
func (s *Service) ValidateNotificationAudience(ctx context.Context, p Subject, ids []RoleID) error {
	return s.read(ctx, func(v Queries) error {
		if e := checkPermission(v, p, NotificationsManage); e != nil {
			return e
		}
		normalized, e := roleIDs(ids)
		if e != nil {
			return e
		}
		for _, id := range normalized {
			if _, e := v.Role(id); e != nil {
				return e
			}
		}
		return nil
	})
}
