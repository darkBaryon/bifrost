// 本文件计算权限并集和来源；每个入口在当前仓储快照内重验身份。
package rbac

import "context"

// Service 通过显式仓储依赖提供角色和授权业务。
type Service struct{ repo Repository }

// New 绑定仓储；仓储缺失时操作返回unavailable，不默认授予权限。
func New(repo Repository) *Service { return &Service{repo: repo} }

func (s *Service) read(ctx context.Context, fn func(ReadView) error) error {
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

func normal(v ReadView, p Subject) error {
	a, e := v.Revalidate(p)
	if e != nil {
		return e
	}
	if !a.Active {
		return ErrUnauthorized
	}
	if a.MustChangePassword {
		return ErrForbidden
	}
	return nil
}

func effective(v ReadView, id string) (Effective, error) {
	out := Effective{AccountID: id, Roles: []Role{}, Permissions: []Grant{}}
	if _, e := v.Account(id); e != nil {
		return out, e
	}
	roles, e := v.AccountRoles(id)
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

func access(v ReadView, p Subject) (Access, error) {
	out := Access{AccountID: p.AccountID, RoleIDs: []RoleID{}, Permissions: []Permission{}}
	if e := normal(v, p); e != nil {
		return out, e
	}
	eff, e := effective(v, p.AccountID)
	if e != nil {
		return out, e
	}
	for _, role := range eff.Roles {
		out.RoleIDs = append(out.RoleIDs, role.ID)
		out.Chief = out.Chief || role.SystemCode == SystemChief
	}
	for _, grant := range eff.Permissions {
		out.Permissions = append(out.Permissions, grant.Code)
	}
	return out, nil
}

func require(v ReadView, p Subject, permission Permission) error {
	a, e := access(v, p)
	if e != nil {
		return e
	}
	if !a.Allows(permission) {
		return ErrForbidden
	}
	return nil
}

// Snapshot 取得当前正常会话的独立快照，不能在后续请求或消息复用。
func (s *Service) Snapshot(ctx context.Context, p Subject) (Access, error) {
	var out Access
	e := s.read(ctx, func(v ReadView) error {
		var e error
		out, e = access(v, p)
		return e
	})
	return out, e
}

// Authorize 验证固定码的当前权限；未知码始终拒绝。
func (s *Service) Authorize(ctx context.Context, p Subject, permission Permission) error {
	if !KnownPermission(permission) {
		return ErrForbidden
	}
	return s.read(ctx, func(v ReadView) error { return require(v, p, permission) })
}

// Me 正常会话可读取本人权限，不要求Users.View。
func (s *Service) Me(ctx context.Context, p Subject) (Effective, error) {
	var out Effective
	e := s.read(ctx, func(v ReadView) error {
		if e := normal(v, p); e != nil {
			return e
		}
		var e error
		out, e = effective(v, p.AccountID)
		return e
	})
	return out, e
}

// GetAccountRoles 查询任意账号均要求Users.View，本人免此权限只能使用Me。
func (s *Service) GetAccountRoles(ctx context.Context, p Subject, id string) (Effective, error) {
	var out Effective
	e := s.read(ctx, func(v ReadView) error {
		if e := require(v, p, UsersView); e != nil {
			return e
		}
		var e error
		out, e = effective(v, id)
		return e
	})
	return out, e
}
