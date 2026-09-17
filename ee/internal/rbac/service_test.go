// 本文件调用实际业务入口，测试角色管理、角色分配和权限检查。
package rbac

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestRoleLifecycleAndProtection(t *testing.T) {
	s, store, admin := ruleFixture()
	ctx := context.Background()
	role, err := s.CreateRole(ctx, admin, RoleInput{Name: " Readers ", PermissionCodes: []Permission{UsersView, UsersView}})
	mustRule(t, err)
	if role.Name != "Readers" || !slices.Equal(role.PermissionCodes, []Permission{UsersView}) {
		t.Fatalf("unexpected normalized role: %+v", role)
	}
	_, err = s.CreateRole(ctx, admin, RoleInput{Name: "Invalid", PermissionCodes: []Permission{"Unknown.View"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown permission accepted", err)
	}
	_, err = s.SetAccountRoles(ctx, admin, "member", []RoleID{role.ID})
	mustRule(t, err)
	store.accounts["member"] = AccountInfo{ID: "member", Active: false}
	var conflict *ConflictError
	if err := s.DeleteRole(ctx, admin, role.ID); !errors.As(err, &conflict) || conflict.AccountCount != 1 {
		t.Fatal("disabled member must still block role deletion", err)
	}
	_, err = s.SetAccountRoles(ctx, admin, "member", nil)
	mustRule(t, err)
	mustRule(t, s.DeleteRole(ctx, admin, role.ID))
	if _, err := s.GetRole(ctx, admin, role.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted role still readable", err)
	}
	for _, preset := range PresetRoles() {
		if err := s.DeleteRole(ctx, admin, preset.ID); !errors.Is(err, ErrConflict) {
			t.Fatal("preset deletion accepted", err)
		}
	}
	_, err = s.UpdateRole(ctx, admin, ChiefRoleID, RoleInput{Name: "Chief", PermissionCodes: []Permission{UsersView}})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("chief permissions changed", err)
	}
	chief, err := s.UpdateRole(ctx, admin, ChiefRoleID, RoleInput{Name: "Renamed", PermissionCodes: Permissions()})
	mustRule(t, err)
	if !slices.Equal(chief.PermissionCodes, Permissions()) || len(store.roles[ChiefRoleID].PermissionCodes) != 0 {
		t.Fatal("chief must derive permissions without storing individual grants")
	}
}

func TestMultipleRolesSourcesAndRevocation(t *testing.T) {
	s, _, admin := ruleFixture()
	ctx := context.Background()
	first, err := s.CreateRole(ctx, admin, RoleInput{Name: "First", PermissionCodes: []Permission{UsersView}})
	mustRule(t, err)
	second, err := s.CreateRole(ctx, admin, RoleInput{Name: "Second", PermissionCodes: []Permission{UsersView, LogsManage}})
	mustRule(t, err)
	eff, err := s.SetAccountRoles(ctx, admin, "member", []RoleID{second.ID, first.ID, first.ID})
	mustRule(t, err)
	if len(eff.Roles) != 2 || len(eff.Permissions) != 2 {
		t.Fatalf("wrong union: %+v", eff)
	}
	found := false
	for _, grant := range eff.Permissions {
		if grant.Code == UsersView {
			found = slices.Equal(grant.RoleIDs, []RoleID{first.ID, second.ID})
		}
	}
	if !found {
		t.Fatal("permission sources lost")
	}
	member := Subject{AccountID: "member"}
	mustRule(t, s.Authorize(ctx, member, LogsManage))
	for _, denied := range []Permission{LogsView, LogsRevealContent, "Unknown.View"} {
		if err := s.Authorize(ctx, member, denied); !errors.Is(err, ErrForbidden) {
			t.Fatal("permission expanded implicitly", denied, err)
		}
	}
	_, err = s.SetAccountRoles(ctx, admin, "member", []RoleID{first.ID})
	mustRule(t, err)
	if err := s.Authorize(ctx, member, LogsManage); !errors.Is(err, ErrForbidden) {
		t.Fatal("next service call retained removed permission", err)
	}
	_, err = s.UpdateRole(ctx, admin, first.ID, RoleInput{Name: "First", PermissionCodes: []Permission{}})
	mustRule(t, err)
	if err := s.Authorize(ctx, member, UsersView); !errors.Is(err, ErrForbidden) {
		t.Fatal("role permission edit not reflected", err)
	}
	if _, err := s.Me(ctx, member); err != nil {
		t.Fatal("own permissions should remain readable", err)
	}
	if _, err := s.GetAccountRoles(ctx, member, "member"); !errors.Is(err, ErrForbidden) {
		t.Fatal("administrative lookup bypassed Users.View", err)
	}
}

func TestChiefAssignmentAndRecoveryRules(t *testing.T) {
	s, store, admin := ruleFixture()
	ctx := context.Background()
	manager, err := s.CreateRole(ctx, admin, RoleInput{Name: "Manager", PermissionCodes: []Permission{UsersManage}})
	mustRule(t, err)
	_, err = s.SetAccountRoles(ctx, admin, "member", []RoleID{manager.ID})
	mustRule(t, err)
	member := Subject{AccountID: "member"}
	if _, err := s.SetAccountRoles(ctx, member, "member", []RoleID{ChiefRoleID}); !errors.Is(err, ErrForbidden) {
		t.Fatal("self-assignment accepted", err)
	}
	if _, err := s.SetAccountRoles(ctx, member, "admin", nil); !errors.Is(err, ErrConflict) {
		t.Fatal("last chief removed", err)
	}
	if err := s.BeforeDisable(ctx, member, "admin"); !errors.Is(err, ErrConflict) {
		t.Fatal("last chief disable accepted", err)
	}
	_, err = s.SetAccountRoles(ctx, admin, "member", []RoleID{manager.ID, ChiefRoleID})
	mustRule(t, err)
	_, err = s.SetAccountRoles(ctx, member, "admin", []RoleID{ReadonlyRoleID})
	mustRule(t, err)
	mustRule(t, s.RestoreChief(ctx, "admin"))
	mustRule(t, s.RestoreChief(ctx, "admin"))
	if !slices.Equal(store.bindings["admin"], []RoleID{ChiefRoleID, ReadonlyRoleID}) {
		t.Fatal("recovery must preserve existing roles and be idempotent")
	}
	if err := s.BindInitialChief(ctx, "member"); !errors.Is(err, ErrForbidden) {
		t.Fatal("non-anchor binding accepted", err)
	}
}

func TestSubjectAndStorageFailuresDenyAccess(t *testing.T) {
	s, store, admin := ruleFixture()
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		account AccountInfo
		want    error
	}{
		{"disabled", AccountInfo{ID: "admin"}, ErrUnauthorized},
		{"must-change-password", AccountInfo{ID: "admin", Active: true, MustChangePassword: true}, ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store.accounts["admin"] = tc.account
			if err := s.Authorize(ctx, admin, UsersManage); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
		})
	}
	store.failure = errors.New("private database detail")
	if _, err := s.Snapshot(ctx, admin); err != ErrUnavailable {
		t.Fatal("storage detail leaked", err)
	}
	if _, err := New(nil).CreateRole(ctx, admin, RoleInput{Name: "Denied"}); err != ErrUnavailable {
		t.Fatal("missing repository accepted", err)
	}
}

func TestPaginationAndAuthorizationOrder(t *testing.T) {
	s, _, admin := ruleFixture()
	ctx := context.Background()
	first, err := s.ListRoles(ctx, admin, "", 2)
	mustRule(t, err)
	if len(first.Items) != 2 || first.Items[0].ID != ReadonlyRoleID || first.NextCursor == "" {
		t.Fatal(first)
	}
	_, err = s.CreateRole(ctx, admin, RoleInput{Name: "Added later"})
	mustRule(t, err)
	last, err := s.ListRoles(ctx, admin, first.NextCursor, 2)
	mustRule(t, err)
	if len(last.Items) != 1 || last.Items[0].ID != ChiefRoleID || last.NextCursor != "" {
		t.Fatal("new role shifted existing cursor", last)
	}
	if _, err := s.GetRole(ctx, Subject{AccountID: "member"}, 999); !errors.Is(err, ErrForbidden) {
		t.Fatal("existence disclosed before authorization", err)
	}
}

// Snapshot 和直接判权都必须反映最新角色，且不能把管理权限扩展成查看权限。
func TestSnapshotReflectsRoleChanges(t *testing.T) {
	s, _, admin := ruleFixture()
	ctx := context.Background()
	member := Subject{AccountID: "member"}
	role, err := s.CreateRole(ctx, admin, RoleInput{Name: "Log manager", PermissionCodes: []Permission{LogsManage}})
	mustRule(t, err)
	_, err = s.SetAccountRoles(ctx, admin, member.AccountID, []RoleID{role.ID})
	mustRule(t, err)

	access, err := s.Snapshot(ctx, member)
	mustRule(t, err)
	if access.AccountID != member.AccountID || access.Chief || !slices.Equal(access.RoleIDs, []RoleID{role.ID}) ||
		!slices.Equal(access.Permissions, []Permission{LogsManage}) {
		t.Fatalf("wrong snapshot for custom role: %+v", access)
	}
	if !access.Allows(LogsManage) || access.Allows(LogsView) {
		t.Fatal("snapshot expanded management permission")
	}
	mustRule(t, s.Authorize(ctx, member, LogsManage))

	_, err = s.SetAccountRoles(ctx, admin, member.AccountID, []RoleID{ChiefRoleID})
	mustRule(t, err)
	access, err = s.Snapshot(ctx, member)
	mustRule(t, err)
	if !access.Chief || !slices.Equal(access.RoleIDs, []RoleID{ChiefRoleID}) || !access.Allows(UsersManage) {
		t.Fatalf("chief assignment not reflected: %+v", access)
	}
	mustRule(t, s.Authorize(ctx, member, UsersManage))

	_, err = s.SetAccountRoles(ctx, admin, member.AccountID, nil)
	mustRule(t, err)
	access, err = s.Snapshot(ctx, member)
	mustRule(t, err)
	if access.Chief || len(access.RoleIDs) != 0 || len(access.Permissions) != 0 {
		t.Fatalf("snapshot retained revoked permissions: %+v", access)
	}
	if err := s.Authorize(ctx, member, UsersManage); !errors.Is(err, ErrForbidden) {
		t.Fatal("authorization retained revoked permissions", err)
	}
}
