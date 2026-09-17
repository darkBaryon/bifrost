// 本文件通过真实数据库验证角色、身份策略与迁移的集成语义。
package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	auth "github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	authstore "github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testLog struct{}

func (testLog) Error(string, ...any) {}

type fixture struct {
	db    *gorm.DB
	auth  *auth.Services
	rb    *rbac.Service
	admin auth.IssuedSession
	store *authstore.Store
}

var ctx = context.Background()

const password = "Admin-password-1"

func database(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	var db *gorm.DB
	var e error
	if dsn := os.Getenv("RBAC_TEST_POSTGRES_DSN"); dsn != "" {
		schema := fmt.Sprintf("rbac_%d", time.Now().UnixNano())
		control, err := gorm.Open(postgres.Open(dsn), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if e = control.Exec("CREATE SCHEMA " + schema).Error; e != nil {
			t.Fatal(e)
		}
		db, e = gorm.Open(postgres.Open(dsn+" search_path="+schema), cfg)
		t.Cleanup(func() { control.Exec("DROP SCHEMA " + schema + " CASCADE"); conn, _ := control.DB(); conn.Close() })
	} else {
		db, e = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rbac.db")+"?_busy_timeout=1000&_journal_mode=WAL&_foreign_keys=on"), cfg)
	}
	if e != nil {
		t.Fatal(e)
	}
	conn, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	must(t, authstore.MigrateIdentity(testLog{})(ctx, db))
	return db
}

func compose(t *testing.T, db *gorm.DB) *fixture {
	t.Helper()
	store := authstore.NewStore(db, testLog{}, authstore.WithPolicyFactory(func(tx *gorm.DB, v auth.Queries) (auth.AccountPolicy, error) {
		return policy.NewPolicy(rbac.New(Bind(tx, v))), nil
	}))
	rb := rbac.New(NewStore(store.ReadWithin, store.Within))
	svc, e := auth.New(store, hasher.Bcrypt{}, auth.Options{InitialPassword: "123456", SetupToken: "setup", SessionTTL: 24 * time.Hour}, policy.NewPolicy(rb))
	must(t, e)
	return &fixture{db: db, auth: svc, rb: rb, store: store}
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db := database(t)
	must(t, MigrateRBAC(testLog{})(ctx, db))
	f := compose(t, db)
	_, e := f.auth.Account.Initialize(ctx, "setup", "admin", password)
	must(t, e)
	f.admin, e = f.auth.Session.Login(ctx, "admin", password, "admin")
	must(t, e)
	return f
}

func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

func want(t *testing.T, e, expected error) {
	t.Helper()
	if !errors.Is(e, expected) {
		t.Fatalf("got %v, want %v", e, expected)
	}
}

func (f *fixture) member(t *testing.T, name string) auth.IssuedSession {
	t.Helper()
	_, e := f.auth.Account.CreateAccount(ctx, f.admin.Principal, name, "")
	must(t, e)
	session, e := f.auth.Session.Login(ctx, name, "123456", name)
	must(t, e)
	must(t, f.auth.Password.ChangePassword(ctx, session.Principal, "123456", password))
	session, e = f.auth.Session.Login(ctx, name, password, name)
	must(t, e)
	return session
}

func TestRolesAndIdentityPolicy(t *testing.T) {
	f := setup(t)
	admin := policy.Subject(f.admin.Principal)
	member := f.member(t, "member")
	p := policy.Subject(member.Principal)
	a, e := f.rb.Snapshot(ctx, admin)
	must(t, e)
	if !a.Chief || len(a.Permissions) != 30 {
		t.Fatalf("bad chief: %+v", a)
	}
	want(t, f.rb.Authorize(ctx, p, rbac.UsersView), rbac.ErrForbidden)
	role, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: " Readers ", PermissionCodes: []rbac.Permission{rbac.UsersView, rbac.UsersView}})
	must(t, e)
	if role.ID != 4 || role.Name != "Readers" || len(role.PermissionCodes) != 1 {
		t.Fatalf("bad role: %+v", role)
	}
	_, e = f.rb.SetAccountRoles(ctx, admin, member.Account.ID, []rbac.RoleID{role.ID, role.ID})
	must(t, e)
	_, e = f.auth.Account.ListAccounts(ctx, member.Principal, "", 20)
	must(t, e)
	_, e = f.auth.Account.CreateAccount(ctx, member.Principal, "denied", "")
	want(t, e, auth.ErrForbidden)
	_, e = f.auth.Session.IssueTicket(ctx, member.Principal)
	want(t, e, auth.ErrForbidden)
	want(t, f.rb.DeleteRole(ctx, admin, role.ID), rbac.ErrConflict)
	old := role
	role, e = f.rb.UpdateRole(ctx, admin, role.ID, rbac.RoleInput{Name: role.Name, PermissionCodes: role.PermissionCodes})
	must(t, e)
	if !role.UpdatedAt.Equal(old.UpdatedAt) {
		t.Fatal("no-op changed timestamp")
	}
	_, e = f.rb.UpdateRole(ctx, admin, role.ID, rbac.RoleInput{Name: role.Name, PermissionCodes: []rbac.Permission{rbac.NotificationsView}})
	must(t, e)
	_, e = f.auth.Account.ListAccounts(ctx, member.Principal, "", 20)
	want(t, e, auth.ErrForbidden)
	// 有通知权限的普通账号可以建立连接，票据仍然只能用一次。
	ticket, e := f.auth.Session.IssueTicket(ctx, member.Principal)
	must(t, e)
	_, e = f.auth.Session.ConsumeTicket(ctx, ticket)
	must(t, e)
	_, e = f.auth.Session.ConsumeTicket(ctx, ticket)
	want(t, e, auth.ErrUnauthorized)
	_, e = f.rb.SetAccountRoles(ctx, admin, member.Account.ID, []rbac.RoleID{})
	must(t, e)
	must(t, f.rb.DeleteRole(ctx, admin, role.ID))
	next, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Next"})
	must(t, e)
	if next.ID <= role.ID {
		t.Fatal("role ID reused")
	}
}

func TestLastChiefAndRecovery(t *testing.T) {
	f := setup(t)
	admin := policy.Subject(f.admin.Principal)
	manager := f.member(t, "manager")
	p := policy.Subject(manager.Principal)
	role, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Account manager", PermissionCodes: []rbac.Permission{rbac.UsersManage}})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, admin, manager.Account.ID, []rbac.RoleID{role.ID})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, p, f.admin.Account.ID, []rbac.RoleID{})
	want(t, e, rbac.ErrConflict)
	_, e = f.auth.Account.SetAccountStatus(ctx, manager.Principal, f.admin.Account.ID, auth.StatusDisabled)
	want(t, e, auth.ErrConflict)
	_, e = f.rb.SetAccountRoles(ctx, admin, manager.Account.ID, []rbac.RoleID{rbac.ChiefRoleID, role.ID})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, p, f.admin.Account.ID, []rbac.RoleID{rbac.ReadonlyRoleID})
	must(t, e)
	_, e = f.auth.Account.SetAccountStatus(ctx, manager.Principal, f.admin.Account.ID, auth.StatusDisabled)
	must(t, e)
	must(t, MigrateRBAC(testLog{})(ctx, f.db))
	must(t, RequireComplete(ctx, f.db))
	must(t, f.auth.Password.RecoverAdmin(ctx, "Recovered-password-1"))
	session, e := f.auth.Session.Login(ctx, "admin", "Recovered-password-1", "recovered")
	must(t, e)
	a, e := f.rb.Snapshot(ctx, policy.Subject(session.Principal))
	must(t, e)
	if !a.Chief || !slices.Contains(a.RoleIDs, rbac.ReadonlyRoleID) {
		t.Fatalf("recovery lost roles: %+v", a)
	}
	_, e = f.rb.SetAccountRoles(ctx, p, manager.Account.ID, []rbac.RoleID{})
	want(t, e, rbac.ErrForbidden)
}

func TestUpgradeSeedsOnlyOnce(t *testing.T) {
	db := database(t)
	store := authstore.NewStore(db, testLog{})
	svc, e := auth.New(store, hasher.Bcrypt{}, auth.Options{InitialPassword: "123456", SetupToken: "setup", SessionTTL: 24 * time.Hour}, nil)
	must(t, e)
	_, e = svc.Account.Initialize(ctx, "setup", "admin", password)
	must(t, e)
	want(t, RequireComplete(ctx, db), rbac.ErrUnavailable)
	must(t, MigrateRBAC(testLog{})(ctx, db))
	f := compose(t, db)
	session, e := svc.Session.Login(ctx, "admin", password, "admin")
	must(t, e)
	p := policy.Subject(session.Principal)
	a, e := f.rb.Snapshot(ctx, p)
	must(t, e)
	if !a.Chief {
		t.Fatal("upgrade did not bind existing anchor")
	}
	_, e = f.rb.UpdateRole(ctx, p, rbac.DeveloperRoleID, rbac.RoleInput{Name: "Edited", PermissionCodes: []rbac.Permission{rbac.UsersView}})
	must(t, e)
	must(t, MigrateRBAC(testLog{})(ctx, db))
	role, e := f.rb.GetRole(ctx, p, rbac.DeveloperRoleID)
	must(t, e)
	if role.Name != "Edited" || !slices.Equal(role.PermissionCodes, []rbac.Permission{rbac.UsersView}) {
		t.Fatal("restart overwrote preset")
	}
}

// 首版合同要求 chief 不存逐项权限，改名称后仍只在读模型中展开全目录。
func TestChiefPermissionsRemainDerived(t *testing.T) {
	f := setup(t)
	subject := policy.Subject(f.admin.Principal)
	checkRows := func() {
		t.Helper()
		var count int64
		must(t, f.db.Model(&permissionRow{}).Where("role_id = ?", uint64(rbac.ChiefRoleID)).Count(&count).Error)
		if count != 0 {
			t.Fatalf("chief stored %d derived permission rows", count)
		}
	}
	checkRows()
	role, err := f.rb.GetRole(ctx, subject, rbac.ChiefRoleID)
	must(t, err)
	if !slices.Equal(role.PermissionCodes, rbac.Permissions()) {
		t.Fatal("chief read missing catalogue")
	}
	updated, err := f.rb.UpdateRole(ctx, subject, role.ID, rbac.RoleInput{Name: "Renamed chief", Description: "Updated", PermissionCodes: role.PermissionCodes})
	must(t, err)
	checkRows()
	if !slices.Equal(updated.PermissionCodes, rbac.Permissions()) {
		t.Fatal("chief update response missing catalogue")
	}
	effective, err := f.rb.Me(ctx, subject)
	must(t, err)
	if len(effective.Permissions) != len(rbac.Permissions()) {
		t.Fatal("chief lost effective permissions")
	}
}
