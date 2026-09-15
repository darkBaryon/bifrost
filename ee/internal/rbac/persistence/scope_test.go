// 本文件用通道屏障验证跨身份/角色操作共享一把写锁，及生命周期失败整笔回滚。
package persistence

import (
	"context"
	"testing"
	"time"

	auth "github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	authstore "github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"gorm.io/gorm"
)

func TestConcurrentLastChiefAcrossFeatures(t *testing.T) {
	f := setup(t)
	second := f.member(t, "second")
	manager := f.member(t, "manager")
	admin := policy.Subject(f.admin.Principal)
	role, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Manager", PermissionCodes: []rbac.Permission{rbac.UsersManage}})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, admin, manager.Account.ID, []rbac.RoleID{role.ID})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, admin, second.Account.ID, []rbac.RoleID{rbac.ChiefRoleID})
	must(t, e)
	locked, release, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
	first, other := make(chan error, 1), make(chan error, 1)
	go func() {
		first <- f.store.Within(ctx, func(db *gorm.DB, v auth.Queries) error {
			_, e := rbac.New(Bind(db, v)).SetAccountRoles(ctx, policy.Subject(manager.Principal), f.admin.Account.ID, []rbac.RoleID{})
			close(locked)
			<-release
			return e
		})
	}()
	<-locked
	go func() {
		close(started)
		_, e := f.auth.Account.SetAccountStatus(ctx, manager.Principal, second.Account.ID, auth.StatusDisabled)
		other <- e
	}()
	<-started
	close(release)
	must(t, <-first)
	want(t, <-other, auth.ErrConflict)
	// 后进入的状态事务必须看到剩下一名有效chief，而不是读到两名的旧状态。
	a, e := f.rb.Snapshot(ctx, policy.Subject(second.Principal))
	must(t, e)
	if !a.Chief {
		t.Fatal("lost final chief")
	}
}

type failingLifecycle struct {
	auth.AccountPolicy
	initialize, recover *bool
}

func (p failingLifecycle) AfterInitialize(ctx context.Context, state auth.State) error {
	if e := p.AccountPolicy.AfterInitialize(ctx, state); e != nil {
		return e
	}
	if *p.initialize {
		return auth.ErrUnavailable
	}
	return nil
}

func (p failingLifecycle) AfterRecover(ctx context.Context, state auth.State) error {
	if e := p.AccountPolicy.AfterRecover(ctx, state); e != nil {
		return e
	}
	if *p.recover {
		return auth.ErrUnavailable
	}
	return nil
}

func TestLifecycleFailureRollsBackBothFeatures(t *testing.T) {
	db := database(t)
	must(t, MigrateRBAC(testLog{})(ctx, db))
	failInit, failRecover := true, false
	store := authstore.NewStore(db, testLog{}, authstore.WithPolicyFactory(func(tx *gorm.DB, v auth.Queries) (auth.AccountPolicy, error) {
		return failingLifecycle{policy.NewPolicy(rbac.New(Bind(tx, v))), &failInit, &failRecover}, nil
	}))
	rb := rbac.New(NewStore(store.ReadWithin, store.Within))
	svc, e := auth.New(store, hasher.Bcrypt{}, auth.Options{InitialPassword: "123456", SetupToken: "setup", SessionTTL: 24 * time.Hour}, policy.NewPolicy(rb))
	must(t, e)
	_, e = svc.Account.Initialize(ctx, "setup", "admin", password)
	want(t, e, auth.ErrUnavailable)
	state, e := store.State(ctx)
	must(t, e)
	if state.Initialized {
		t.Fatal("failed hook committed state")
	}
	var n int64
	must(t, db.Model(&accountRoleRow{}).Count(&n).Error)
	if n != 0 {
		t.Fatal("failed hook committed relation")
	}
	failInit = false
	_, e = svc.Account.Initialize(ctx, "setup", "admin", password)
	must(t, e)
	session, e := svc.Session.Login(ctx, "admin", password, "admin")
	must(t, e)
	before, e := store.RecordByName(ctx, "admin")
	must(t, e)
	failRecover = true
	want(t, svc.Password.RecoverAdmin(ctx, "New-password-1"), auth.ErrUnavailable)
	after, e := store.RecordByName(ctx, "admin")
	must(t, e)
	if after.PasswordHash != before.PasswordHash || after.AuthVersion != before.AuthVersion {
		t.Fatal("recovery failure changed credential")
	}
	_, e = svc.Session.Authenticate(ctx, session.Token)
	must(t, e)
	events, e := store.Events(ctx, "", auth.Cursor{}, 100)
	must(t, e)
	if len(events) != 0 {
		t.Fatal("recovery failure committed event")
	}
	failRecover = false
	must(t, svc.Password.RecoverAdmin(ctx, "New-password-1"))
	_, e = svc.Session.Authenticate(ctx, session.Token)
	want(t, e, auth.ErrUnauthorized)
}

func TestConcurrentDeleteAndAssign(t *testing.T) {
	for _, deleteFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "delete-first", false: "assign-first"}[deleteFirst], func(t *testing.T) {
			f := setup(t)
			member := f.member(t, "member")
			admin := policy.Subject(f.admin.Principal)
			role, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Concurrent"})
			must(t, e)
			locked, release := make(chan struct{}), make(chan struct{})
			first, second := make(chan error, 1), make(chan error, 1)
			go func() {
				first <- f.store.Within(ctx, func(db *gorm.DB, v auth.Queries) error {
					service := rbac.New(Bind(db, v))
					var e error
					if deleteFirst {
						e = service.DeleteRole(ctx, admin, role.ID)
					} else {
						_, e = service.SetAccountRoles(ctx, admin, member.Account.ID, []rbac.RoleID{role.ID})
					}
					close(locked)
					<-release
					return e
				})
			}()
			<-locked
			go func() {
				var e error
				if deleteFirst {
					_, e = f.rb.SetAccountRoles(ctx, admin, member.Account.ID, []rbac.RoleID{role.ID})
				} else {
					e = f.rb.DeleteRole(ctx, admin, role.ID)
				}
				second <- e
			}()
			close(release)
			must(t, <-first)
			if deleteFirst {
				want(t, <-second, rbac.ErrNotFound)
			} else {
				want(t, <-second, rbac.ErrConflict)
			}
			access, e := f.rb.Snapshot(ctx, policy.Subject(member.Principal))
			must(t, e)
			if deleteFirst && len(access.RoleIDs) != 0 {
				t.Fatal("assignment survived deleted role")
			}
			if !deleteFirst && (len(access.RoleIDs) != 1 || access.RoleIDs[0] != role.ID) {
				t.Fatal("delete removed assigned role")
			}
		})
	}
}

// pausedHasher 在昂贵哈希阶段停住，使撤权发生在预检与最终事务判定之间。
type pausedHasher struct {
	auth.PasswordHasher
	entered, release chan struct{}
}

func (h pausedHasher) Hash(password string) (string, error) {
	if password == "123456" {
		close(h.entered)
		<-h.release
	}
	return h.PasswordHasher.Hash(password)
}

func TestResetRechecksPermissionAfterHash(t *testing.T) {
	f := setup(t)
	manager, target := f.member(t, "manager"), f.member(t, "target")
	admin := policy.Subject(f.admin.Principal)
	role, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Manager", PermissionCodes: []rbac.Permission{rbac.UsersManage}})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, admin, manager.Account.ID, []rbac.RoleID{role.ID})
	must(t, e)
	entered, release := make(chan struct{}), make(chan struct{})
	service, e := auth.New(f.store, pausedHasher{hasher.Bcrypt{}, entered, release}, auth.Options{InitialPassword: "123456", SessionTTL: 24 * time.Hour}, policy.NewPolicy(f.rb))
	must(t, e)
	before, e := f.store.RecordByName(ctx, "target")
	must(t, e)
	done := make(chan error, 1)
	const operation = "2c6585f2-857c-4c27-a0c0-8ee0ff0f1001"
	go func() {
		_, e := service.Password.ResetPassword(ctx, manager.Principal, target.Account.ID, operation)
		done <- e
	}()
	<-entered
	_, e = f.rb.SetAccountRoles(ctx, admin, manager.Account.ID, []rbac.RoleID{})
	must(t, e)
	close(release)
	want(t, <-done, auth.ErrForbidden)
	after, e := f.store.RecordByName(ctx, "target")
	must(t, e)
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion {
		t.Fatal("revoked reset changed target")
	}
	_, e = f.auth.Session.Authenticate(ctx, target.Token)
	must(t, e)
	events, e := f.store.Events(ctx, "", auth.Cursor{}, 100)
	must(t, e)
	found := false
	for _, event := range events {
		if event.OperationID == operation {
			found = true
			if event.Result != auth.ResultFailure || event.ReasonCode != "forbidden" {
				t.Fatal("wrong failure event", event.Result, event.ReasonCode)
			}
		}
	}
	if !found {
		t.Fatal("revoked reset lost failure event")
	}
}
