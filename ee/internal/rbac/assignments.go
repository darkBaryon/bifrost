// 本文件管理角色分配、最后有效主管理员与初始化/离线恢复绑定。
package rbac

import (
	"context"
	"slices"
)

func roleIDs(ids []RoleID) ([]RoleID, error) {
	if len(ids) > MaxRolesPerAccount {
		return nil, ErrInvalid
	}
	out := append([]RoleID{}, ids...)
	slices.Sort(out)
	out = slices.Compact(out)
	for _, id := range out {
		if id == 0 || id > MaxRoleID {
			return nil, ErrInvalid
		}
	}
	return out, nil
}

func remainingChief(v ReadView, target string) error {
	roles, e := v.AccountRoles(target)
	if e != nil {
		return e
	}
	chief := false
	for _, role := range roles {
		chief = chief || role.SystemCode == SystemChief
	}
	if !chief {
		return nil
	}
	account, e := v.Account(target)
	if e != nil {
		return e
	}
	if !account.Active {
		return nil
	}
	ids, e := v.RoleAccountIDs(ChiefRoleID)
	if e != nil {
		return e
	}
	n, e := v.CountActiveAccounts(ids)
	if e != nil {
		return e
	}
	if n <= 1 {
		return ErrConflict
	}
	return nil
}

// SetAccountRoles 整组替换，禁止改自己；保护检查与关系修改必须共用身份state写锁。
func (s *Service) SetAccountRoles(ctx context.Context, p Subject, target string, ids []RoleID) (Effective, error) {
	var out Effective
	e := s.write(ctx, func(tx Tx) error {
		if e := require(tx, p, UsersManage); e != nil {
			return e
		}
		if _, e := tx.Account(target); e != nil {
			return e
		}
		if p.AccountID == target {
			return ErrForbidden
		}
		normalized, e := roleIDs(ids)
		if e != nil {
			return e
		}
		keepsChief := false
		for _, id := range normalized {
			role, err := tx.Role(id)
			if err != nil {
				return err
			}
			keepsChief = keepsChief || role.SystemCode == SystemChief
		}
		if !keepsChief {
			if e := remainingChief(tx, target); e != nil {
				return e
			}
		}
		if e := tx.ReplaceAccountRoles(target, normalized); e != nil {
			return e
		}
		out, e = effective(tx, target)
		return e
	})
	return out, e
}

// BeforeDisable 只通过identity事务策略调用，绑定仓储不另开事务。
func (s *Service) BeforeDisable(ctx context.Context, p Subject, target string) error {
	return s.write(ctx, func(tx Tx) error {
		if e := require(tx, p, UsersManage); e != nil {
			return e
		}
		return remainingChief(tx, target)
	})
}

func (s *Service) bindChief(ctx context.Context, id string) error {
	return s.write(ctx, func(tx Tx) error {
		state := tx.State()
		if !state.Initialized || state.ChiefAccountID != id {
			return ErrForbidden
		}
		if _, e := tx.Account(id); e != nil {
			return e
		}
		return tx.AddAccountRole(id, ChiefRoleID)
	})
}

// BindInitialChief 仅供迁移或首次初始化钩子调用，不能由角色管理HTTP调用。
func (s *Service) BindInitialChief(ctx context.Context, id string) error { return s.bindChief(ctx, id) }

// RestoreChief 仅供离线恢复事务增补恢复锚点角色，保留其他关系。
func (s *Service) RestoreChief(ctx context.Context, id string) error { return s.bindChief(ctx, id) }
