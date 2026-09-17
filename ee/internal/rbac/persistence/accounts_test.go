// 本文件验证账号删除与角色、会话的事务一致性，以及最近登录时间的更新时点。
package persistence

import (
	"errors"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	"testing"
	"time"

	auth "github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"gorm.io/gorm"
)

func TestDeleteAccountLifecycle(t *testing.T) {
	f := setup(t)
	member := f.member(t, "member")
	admin := policy.Subject(f.admin.Principal)
	want(t, f.auth.Account.DeleteAccount(ctx, member.Principal, f.admin.Account.ID), auth.ErrForbidden)
	want(t, f.auth.Account.DeleteAccount(ctx, f.admin.Principal, f.admin.Account.ID), auth.ErrForbidden)
	role, err := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Manager", PermissionCodes: []rbac.Permission{rbac.UsersManage, rbac.NotificationsView}})
	must(t, err)
	_, err = f.rb.SetAccountRoles(ctx, admin, member.Account.ID, []rbac.RoleID{rbac.ChiefRoleID, role.ID})
	must(t, err)
	// 另一个有管理权限的账号也不能删除固定恢复账号。
	want(t, f.auth.Account.DeleteAccount(ctx, member.Principal, f.admin.Account.ID), auth.ErrForbidden)
	_, err = f.rb.SetAccountRoles(ctx, policy.Subject(member.Principal), f.admin.Account.ID, []rbac.RoleID{role.ID})
	must(t, err)
	want(t, f.auth.Account.DeleteAccount(ctx, f.admin.Principal, member.Account.ID), auth.ErrConflict)
	_, err = f.rb.SetAccountRoles(ctx, policy.Subject(member.Principal), f.admin.Account.ID, []rbac.RoleID{rbac.ChiefRoleID})
	must(t, err)
	_, err = f.auth.Session.IssueTicket(ctx, member.Principal)
	must(t, err)
	events, err := f.store.Events(ctx, member.Account.ID, auth.Cursor{}, 100)
	must(t, err)
	if len(events) == 0 {
		t.Fatal("fixture has no history")
	}
	must(t, f.auth.Account.DeleteAccount(ctx, f.admin.Principal, member.Account.ID))
	want(t, f.auth.Account.DeleteAccount(ctx, f.admin.Principal, member.Account.ID), auth.ErrNotFound)
	_, err = f.auth.Session.Authenticate(ctx, member.Token)
	want(t, err, auth.ErrUnauthorized)
	for _, tc := range []struct{ table, column, id string }{
		{"ee_identity_accounts", "id", member.Account.ID},
		{"ee_identity_sessions", "account_id", member.Account.ID},
		{"ee_identity_ws_tickets", "session_id", member.Principal.SessionID},
		{"ee_rbac_account_roles", "account_id", member.Account.ID},
	} {
		var n int64
		must(t, f.db.Table(tc.table).Where(tc.column+" = ?", tc.id).Count(&n).Error)
		if n != 0 {
			t.Fatal("deleted account left rows", tc.table)
		}
	}
	after, err := f.store.Events(ctx, member.Account.ID, auth.Cursor{}, 100)
	must(t, err)
	if len(after) != len(events) {
		t.Fatal("deletion removed password history")
	}
	must(t, f.rb.DeleteRole(ctx, admin, role.ID))
	replacement, err := f.auth.Account.CreateAccount(ctx, f.admin.Principal, "member", "")
	must(t, err)
	if replacement.ID == member.Account.ID || replacement.LastLoginAt != nil {
		t.Fatal("new account inherited deleted identity")
	}
}

func TestDeleteAccountRollback(t *testing.T) {
	f := setup(t)
	member := f.member(t, "member")
	_, err := f.rb.SetAccountRoles(ctx, policy.Subject(f.admin.Principal), member.Account.ID, []rbac.RoleID{rbac.ReadonlyRoleID})
	must(t, err)
	must(t, f.db.Callback().Delete().Before("gorm:delete").Register("fail_account_delete", func(db *gorm.DB) {
		if db.Statement.Table == "ee_identity_accounts" {
			db.AddError(errors.New("fixture failure"))
		}
	}))
	t.Cleanup(func() { f.db.Callback().Delete().Remove("fail_account_delete") })
	want(t, f.auth.Account.DeleteAccount(ctx, f.admin.Principal, member.Account.ID), auth.ErrUnavailable)
	_, err = f.auth.Session.Authenticate(ctx, member.Token)
	must(t, err)
	access, err := f.rb.Snapshot(ctx, policy.Subject(member.Principal))
	must(t, err)
	if len(access.RoleIDs) != 1 || access.RoleIDs[0] != rbac.ReadonlyRoleID {
		t.Fatal("delete failure left role unlink committed")
	}
}

func TestLastLoginAtomicity(t *testing.T) {
	f := setup(t)
	account, err := f.auth.Account.CreateAccount(ctx, f.admin.Principal, "fresh", "")
	must(t, err)
	if account.LastLoginAt != nil {
		t.Fatal("new account already has login time")
	}
	_, err = f.auth.Session.Login(ctx, "fresh", "wrong-password", "fresh")
	want(t, err, auth.ErrUnauthorized)
	before, err := f.store.RecordByName(ctx, "fresh")
	must(t, err)
	if before.LastLoginAt != nil {
		t.Fatal("failed login changed timestamp")
	}
	logged, err := f.auth.Session.Login(ctx, "fresh", "123456", "fresh")
	must(t, err)
	if logged.Account.LastLoginAt == nil {
		t.Fatal("successful login missing timestamp")
	}
	stored, err := f.store.RecordByName(ctx, "fresh")
	must(t, err)
	if stored.LastLoginAt == nil || !stored.LastLoginAt.Equal(*logged.Account.LastLoginAt) {
		t.Fatal("response and stored timestamp differ")
	}
	var sessions int64
	must(t, f.db.Table("ee_identity_sessions").Where("account_id = ?", account.ID).Count(&sessions).Error)
	must(t, f.db.Callback().Update().Before("gorm:update").Register("fail_login_timestamp", func(db *gorm.DB) {
		if db.Statement.Table == "ee_identity_accounts" {
			db.AddError(errors.New("fixture failure"))
		}
	}))
	t.Cleanup(func() { f.db.Callback().Update().Remove("fail_login_timestamp") })
	_, err = f.auth.Session.Login(ctx, "fresh", "123456", "fresh")
	want(t, err, auth.ErrUnavailable)
	after, err := f.store.RecordByName(ctx, "fresh")
	must(t, err)
	if !after.LastLoginAt.Equal(*stored.LastLoginAt) {
		t.Fatal("failed transaction changed timestamp")
	}
	var count int64
	must(t, f.db.Table("ee_identity_sessions").Where("account_id = ?", account.ID).Count(&count).Error)
	if count != sessions {
		t.Fatal("failed timestamp save committed a session")
	}
}

func TestLastChiefDeleteBeforeDisable(t *testing.T) {
	f := setup(t)
	second, manager := f.member(t, "second"), f.member(t, "manager")
	admin := policy.Subject(f.admin.Principal)
	role, err := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Manager", PermissionCodes: []rbac.Permission{rbac.UsersManage}})
	must(t, err)
	_, err = f.rb.SetAccountRoles(ctx, admin, manager.Account.ID, []rbac.RoleID{role.ID})
	must(t, err)
	_, err = f.rb.SetAccountRoles(ctx, admin, second.Account.ID, []rbac.RoleID{rbac.ChiefRoleID})
	must(t, err)
	locked, release := make(chan struct{}), make(chan struct{})
	services, err := auth.New(pausedTransaction{f.store, locked, release}, hasher.Bcrypt{}, auth.Options{InitialPassword: "123456", SessionTTL: 24 * time.Hour}, policy.NewPolicy(f.rb))
	must(t, err)
	first, other := make(chan error, 1), make(chan error, 1)
	go func() { first <- services.Account.DeleteAccount(ctx, manager.Principal, second.Account.ID) }()
	<-locked
	go func() {
		_, err := f.auth.Account.SetAccountStatus(ctx, manager.Principal, f.admin.Account.ID, auth.StatusDisabled)
		other <- err
	}()
	close(release)
	must(t, <-first)
	want(t, <-other, auth.ErrConflict)
	_, err = f.auth.Session.Authenticate(ctx, second.Token)
	want(t, err, auth.ErrUnauthorized)
	access, err := f.rb.Snapshot(ctx, admin)
	must(t, err)
	if !access.Chief {
		t.Fatal("lost final chief")
	}
}
