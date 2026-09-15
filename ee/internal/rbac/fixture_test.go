// 本文件用内存数据支持规则测试，不模拟数据库的锁、回滚或真实登录会话。
package rbac

import (
	"context"
	"slices"
	"testing"
)

type ruleStore struct {
	accounts map[string]AccountInfo
	roles    map[RoleID]Role
	bindings map[string][]RoleID
	next     RoleID
	failure  error
}

func ruleFixture() (*Service, *ruleStore, Subject) {
	r := &ruleStore{
		accounts: map[string]AccountInfo{
			"admin":  {ID: "admin", Active: true},
			"member": {ID: "member", Active: true},
		},
		roles: map[RoleID]Role{}, bindings: map[string][]RoleID{"admin": {ChiefRoleID}}, next: 4,
	}
	for _, role := range PresetRoles() {
		r.roles[role.ID] = role
	}
	return New(r), r, Subject{AccountID: "admin"}
}

func (r *ruleStore) Read(_ context.Context, fn func(Queries) error) error {
	if r.failure != nil {
		return r.failure
	}
	return fn(r)
}

func (r *ruleStore) Write(_ context.Context, fn func(Tx) error) error {
	if r.failure != nil {
		return r.failure
	}
	return fn(r)
}

func (r *ruleStore) State() State                              { return State{Initialized: true, ChiefAccountID: "admin"} }
func (r *ruleStore) Revalidate(p Subject) (AccountInfo, error) { return r.Account(p.AccountID) }

func (r *ruleStore) Account(id string) (AccountInfo, error) {
	a, ok := r.accounts[id]
	if !ok {
		return AccountInfo{}, ErrNotFound
	}
	return a, nil
}

func (r *ruleStore) CountActiveAccounts(ids []string) (int, error) {
	seen := map[string]bool{}
	for _, id := range ids {
		if r.accounts[id].Active {
			seen[id] = true
		}
	}
	return len(seen), nil
}

func (r *ruleStore) Role(id RoleID) (Role, error) {
	role, ok := r.roles[id]
	if !ok {
		return Role{}, ErrNotFound
	}
	role.PermissionCodes = slices.Clone(role.PermissionCodes)
	ids, _ := r.RoleAccountIDs(id)
	role.AccountCount = len(ids)
	return role, nil
}

func (r *ruleStore) Roles(before RoleID, limit int) ([]Role, error) {
	ids := []RoleID{}
	for id := range r.roles {
		if before == 0 || id < before {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	slices.Reverse(ids)
	if len(ids) > limit {
		ids = ids[:limit]
	}
	roles := []Role{}
	for _, id := range ids {
		role, _ := r.Role(id)
		roles = append(roles, role)
	}
	return roles, nil
}

func (r *ruleStore) AccountRoles(id string) ([]Role, error) {
	roles := []Role{}
	for _, roleID := range r.bindings[id] {
		role, err := r.Role(roleID)
		if err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}

func (r *ruleStore) RoleAccountIDs(id RoleID) ([]string, error) {
	ids := []string{}
	for account, bindings := range r.bindings {
		if slices.Contains(bindings, id) {
			ids = append(ids, account)
		}
	}
	return ids, nil
}

func (r *ruleStore) InsertRole(in RoleInput) (Role, error) {
	id := r.next
	r.next++
	r.roles[id] = Role{ID: id}
	return r.UpdateRole(id, in)
}

func (r *ruleStore) UpdateRole(id RoleID, in RoleInput) (Role, error) {
	role := r.roles[id]
	role.Name, role.Description = in.Name, in.Description
	role.PermissionCodes = slices.Clone(in.PermissionCodes)
	r.roles[id] = role
	return r.Role(id)
}

func (r *ruleStore) DeleteRole(id RoleID) error { delete(r.roles, id); return nil }

func (r *ruleStore) ReplaceAccountRoles(id string, roles []RoleID) error {
	r.bindings[id] = slices.Clone(roles)
	return nil
}

func (r *ruleStore) AddAccountRole(id string, role RoleID) error {
	if !slices.Contains(r.bindings[id], role) {
		r.bindings[id] = append(r.bindings[id], role)
		slices.Sort(r.bindings[id])
	}
	return nil
}

func mustRule(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
