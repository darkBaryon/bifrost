// 本文件用数据库写失败验证首绑/旧导入/恢复回滚，并演练迁移中断后的重试。
package persistence

import (
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
)

func rejectBindings(t *testing.T, db *gorm.DB) func() {
	t.Helper()
	var create, drop []string
	if db.Dialector.Name() == "postgres" {
		create = []string{`CREATE FUNCTION rbac_reject_binding() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled RBAC write failure'; END $$`, `CREATE TRIGGER rbac_reject_binding BEFORE INSERT ON ee_rbac_account_roles FOR EACH ROW EXECUTE FUNCTION rbac_reject_binding()`}
		drop = []string{`DROP TRIGGER IF EXISTS rbac_reject_binding ON ee_rbac_account_roles`, `DROP FUNCTION IF EXISTS rbac_reject_binding()`}
	} else {
		create = []string{`CREATE TRIGGER rbac_reject_binding BEFORE INSERT ON ee_rbac_account_roles BEGIN SELECT RAISE(ABORT, 'controlled RBAC write failure'); END`}
		drop = []string{`DROP TRIGGER IF EXISTS rbac_reject_binding`}
	}
	for _, sql := range create {
		must(t, db.Exec(sql).Error)
	}
	clear := func() {
		for _, sql := range drop {
			must(t, db.Exec(sql).Error)
		}
	}
	t.Cleanup(clear)
	return clear
}

func TestFirstBindingDatabaseFailure(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{true: "bootstrap-legacy", false: "initialize"}[legacy], func(t *testing.T) {
			db := database(t)
			must(t, MigrateRBAC(testLog{})(ctx, db))
			f := compose(t, db)
			hash, e := (hasher.Bcrypt{}).Hash(password)
			must(t, e)
			attempt := func() error {
				if legacy {
					return f.auth.Account.BootstrapLegacy(ctx, "admin", hash)
				}
				_, e := f.auth.Account.Initialize(ctx, "setup", "admin", password)
				return e
			}
			clear := rejectBindings(t, db)
			want(t, attempt(), identity.ErrUnavailable)
			state, e := f.store.State(ctx)
			must(t, e)
			if state.Initialized || state.ChiefAccountID != "" {
				t.Fatal("failed binding committed initialization")
			}
			var accounts, bindings int64
			must(t, db.Table("ee_identity_accounts").Count(&accounts).Error)
			must(t, db.Model(&accountRoleRow{}).Count(&bindings).Error)
			if accounts != 0 || bindings != 0 {
				t.Fatal("failed binding left account or relation", accounts, bindings)
			}
			clear()
			must(t, attempt())
			session, e := f.auth.Session.Login(ctx, "admin", password, "peer")
			must(t, e)
			access, e := f.rb.Snapshot(ctx, policy.Subject(session.Principal))
			must(t, e)
			if !access.Chief {
				t.Fatal("retry did not bind chief")
			}
		})
	}
}

func TestRecoveryBindingDatabaseFailure(t *testing.T) {
	f := setup(t)
	second := f.member(t, "second")
	_, e := f.rb.SetAccountRoles(ctx, policy.Subject(f.admin.Principal), second.Account.ID, []rbac.RoleID{rbac.ChiefRoleID})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, policy.Subject(second.Principal), f.admin.Account.ID, []rbac.RoleID{rbac.ReadonlyRoleID})
	must(t, e)
	before, e := f.store.RecordByName(ctx, "admin")
	must(t, e)
	oldEvents, e := f.store.Events(ctx, "", identity.Cursor{}, 100)
	must(t, e)
	clear := rejectBindings(t, f.db)
	want(t, f.auth.Password.RecoverAdmin(ctx, "Recovered-password-2"), identity.ErrUnavailable)
	after, e := f.store.RecordByName(ctx, "admin")
	must(t, e)
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion || before.Status != after.Status {
		t.Fatal("failed recovery changed identity")
	}
	_, e = f.auth.Session.Authenticate(ctx, f.admin.Token)
	must(t, e)
	events, e := f.store.Events(ctx, "", identity.Cursor{}, 100)
	must(t, e)
	if len(events) != len(oldEvents) {
		t.Fatal("failed recovery committed event")
	}
	access, e := f.rb.Snapshot(ctx, policy.Subject(f.admin.Principal))
	must(t, e)
	if access.Chief || len(access.RoleIDs) != 1 || access.RoleIDs[0] != rbac.ReadonlyRoleID {
		t.Fatal("failed recovery changed roles")
	}
	clear()
	must(t, f.auth.Password.RecoverAdmin(ctx, "Recovered-password-2"))
	session, e := f.auth.Session.Login(ctx, "admin", "Recovered-password-2", "peer")
	must(t, e)
	access, e = f.rb.Snapshot(ctx, policy.Subject(session.Principal))
	must(t, e)
	if !access.Chief || len(access.RoleIDs) != 2 {
		t.Fatal("recovery retry lost existing role")
	}
	_, e = f.auth.Session.Authenticate(ctx, f.admin.Token)
	want(t, e, identity.ErrUnauthorized)
}

func TestMigrationInterruptedWriteCanRetry(t *testing.T) {
	db := database(t)
	triggered := false
	must(t, db.Callback().Create().Before("gorm:create").Register("rbac:interrupt-seed", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_rbac_role_permissions" {
			triggered = true
			tx.AddError(tx.Session(&gorm.Session{NewDB: true}).Exec("INSERT INTO missing_rbac_failure_table VALUES (1)").Error)
		}
	}))
	e := MigrateRBAC(testLog{})(ctx, db)
	must(t, db.Callback().Create().Remove("rbac:interrupt-seed"))
	if e == nil || !triggered {
		t.Fatal("migration failure injection did not execute")
	}
	for _, table := range []string{"ee_rbac_roles", "ee_rbac_role_permissions", "ee_rbac_account_roles"} {
		if db.Migrator().HasTable(table) {
			t.Fatal("interrupted migration left table", table)
		}
	}
	pending, e := migrator.PendingIDs(ctx, db, migrator.DefaultOptions, []string{migrationID})
	must(t, e)
	if len(pending) != 1 {
		t.Fatal("interrupted migration committed marker")
	}
	must(t, MigrateRBAC(testLog{})(ctx, db))
	must(t, RequireComplete(ctx, db))
	var count int64
	must(t, db.Model(&roleRow{}).Count(&count).Error)
	if count != 3 {
		t.Fatal("migration retry did not seed exactly three roles", count)
	}
}

func TestPermissionQueryFailureDoesNotDowngradeOrCommit(t *testing.T) {
	f := setup(t)
	member := f.member(t, "member")
	before, e := f.store.RecordByName(ctx, "member")
	must(t, e)
	eventsBefore, e := f.store.Events(ctx, "", identity.Cursor{}, 100)
	must(t, e)
	// 只破坏本测试隔离库中的权限读取依赖，身份与事件表仍可独立核验。
	must(t, f.db.Migrator().DropTable(&accountRoleRow{}))
	_, e = f.rb.Me(ctx, policy.Subject(f.admin.Principal))
	want(t, e, rbac.ErrUnavailable)
	page, e := f.auth.Password.ListPasswordEvents(ctx, f.admin.Principal, "", "", 20)
	want(t, e, identity.ErrUnavailable)
	if len(page.Items) != 0 {
		t.Fatal("permission query failure downgraded to self events")
	}
	_, e = f.auth.Session.IssueTicket(ctx, f.admin.Principal)
	want(t, e, identity.ErrUnavailable)
	_, e = f.auth.Password.ResetPassword(ctx, f.admin.Principal, member.Account.ID, "2c6585f2-857c-4c27-a0c0-8ee0ff0f1002")
	want(t, e, identity.ErrUnavailable)
	after, e := f.store.RecordByName(ctx, "member")
	must(t, e)
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion {
		t.Fatal("query failure committed reset")
	}
	eventsAfter, e := f.store.Events(ctx, "", identity.Cursor{}, 100)
	must(t, e)
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatal("storage failure committed business failure event")
	}
	_, e = f.auth.Session.Authenticate(ctx, member.Token)
	must(t, e)
}
