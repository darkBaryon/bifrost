// 本文件给账号分配角色，防止移除最后一个启用的主管理员，并为初始化和离线恢复补上主管理员角色。
package rbac

import (
	"context"
	"slices"
)

// State 记录系统是否已初始化，以及离线恢复管理员时应恢复哪个账号。
type State struct {
	Initialized    bool
	ChiefAccountID string // 固定的恢复账号，不代表只有这个账号能拥有主管理员角色。
}

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

// remainingChief 检查目标是不是最后一个启用的主管理员；如果是，就不允许停用它或移除它的主管理员角色。
func remainingChief(queries Queries, target string) error {
	roles, e := queries.AccountRoles(target)
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
	account, e := queries.Account(target)
	if e != nil {
		return e
	}
	if !account.Active {
		return nil
	}
	ids, e := queries.RoleAccountIDs(ChiefRoleID)
	if e != nil {
		return e
	}
	n, e := queries.CountActiveAccounts(ids)
	if e != nil {
		return e
	}
	if n <= 1 {
		return ErrConflict
	}
	return nil
}

// SetAccountRoles 替换目标账号的全部角色，空列表表示清空；操作者不能修改自己的角色分配。
// 检查最后管理员和修改分配必须共用身份模块的 state 写锁，防止同时操作时绕过保护。
func (s *Service) SetAccountRoles(ctx context.Context, p Subject, target string, ids []RoleID) (Effective, error) {
	var out Effective
	e := s.write(ctx, func(tx Tx) error {
		if e := checkPermission(tx, p, UsersManage); e != nil {
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
		out, e = accountPermissions(tx, target)
		return e
	})
	return out, e
}

// BeforeDisable 供身份模块在停用账号前检查操作权限和最后管理员保护，本方法不执行停用。
// 调用时必须使用身份模块正在执行的事务，不能另开事务，让检查与停用一起成功或失败。
func (s *Service) BeforeDisable(ctx context.Context, p Subject, target string) error {
	return s.write(ctx, func(tx Tx) error {
		if e := checkPermission(tx, p, UsersManage); e != nil {
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

// BindInitialChief 在数据库升级或首次初始化时，给固定的恢复账号补上主管理员角色；不能作为普通角色管理接口开放。
func (s *Service) BindInitialChief(ctx context.Context, id string) error { return s.bindChief(ctx, id) }

// RestoreChief 仅在离线恢复事务中使用，给固定的恢复账号补上主管理员角色，保留它已有的其他角色。
func (s *Service) RestoreChief(ctx context.Context, id string) error { return s.bindChief(ctx, id) }

// BeforeDelete 供身份模块在同一删除事务内检查最后管理员并清空角色关联。
// 必须绑定身份当前事务；后续账号删除失败时，角色解绑也要回滚。
func (s *Service) BeforeDelete(ctx context.Context, p Subject, target string) error {
	return s.write(ctx, func(tx Tx) error {
		if err := checkPermission(tx, p, UsersManage); err != nil {
			return err
		}
		if err := remainingChief(tx, target); err != nil {
			return err
		}
		return tx.ReplaceAccountRoles(target, nil)
	})
}
