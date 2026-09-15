// 本文件补足身份状态先提交的逆序竞争，证明角色写入使用同一全局状态锁。
package persistence

import (
	"context"
	"fmt"
	"testing"
	"time"

	auth "github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"gorm.io/gorm"
)

type pausedTransaction struct {
	auth.Repository
	locked, release chan struct{}
}

func (p pausedTransaction) Transaction(ctx context.Context, fn func(auth.Tx) error) error {
	return p.Repository.Transaction(ctx, func(tx auth.Tx) error {
		if e := fn(tx); e != nil {
			return e
		}
		close(p.locked)
		<-p.release
		return nil
	})
}

func TestLastChiefDisableBeforeUnassign(t *testing.T) {
	f := setup(t)
	second, manager := f.member(t, "second"), f.member(t, "manager")
	admin := policy.Subject(f.admin.Principal)
	role, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Manager", PermissionCodes: []rbac.Permission{rbac.UsersManage}})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, admin, manager.Account.ID, []rbac.RoleID{role.ID})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, admin, second.Account.ID, []rbac.RoleID{rbac.ChiefRoleID})
	must(t, e)
	locked, release := make(chan struct{}), make(chan struct{})
	services, e := auth.New(pausedTransaction{f.store, locked, release}, hasher.Bcrypt{}, auth.Options{InitialPassword: "123456", SessionTTL: 24 * time.Hour}, policy.NewPolicy(f.rb))
	must(t, e)
	first, other := make(chan error, 1), make(chan error, 1)
	go func() {
		_, e := services.Account.SetAccountStatus(ctx, manager.Principal, second.Account.ID, auth.StatusDisabled)
		first <- e
	}()
	<-locked
	secondBeforeLock, secondLocked := make(chan struct{}), make(chan struct{})
	secondStore := NewStore(f.store.ReadWithin, func(callCtx context.Context, fn func(*gorm.DB, auth.Queries) error) error {
		close(secondBeforeLock)
		return f.store.Within(callCtx, func(db *gorm.DB, view auth.Queries) error {
			close(secondLocked)
			current, err := view.LookupAccount(second.Account.ID)
			if err != nil {
				return err
			}
			if current.Status != auth.StatusDisabled {
				return fmt.Errorf("second lock observed pre-commit chief state")
			}
			return fn(db, view)
		})
	})
	secondService := rbac.New(secondStore)
	go func() {
		_, e := secondService.SetAccountRoles(ctx, policy.Subject(manager.Principal), f.admin.Account.ID, []rbac.RoleID{})
		other <- e
	}()
	<-secondBeforeLock
	select {
	case <-secondLocked:
		t.Fatal("second writer entered while first transaction held the state lock")
	default:
	}
	close(release)
	must(t, <-first)
	want(t, <-other, rbac.ErrConflict)
	access, e := f.rb.Snapshot(ctx, admin)
	must(t, e)
	if !access.Chief {
		t.Fatal("inverse race removed final active chief")
	}
	_, e = f.auth.Session.Authenticate(ctx, second.Token)
	want(t, e, auth.ErrUnauthorized)
}

func TestMultiRoleUnionAndSources(t *testing.T) {
	f := setup(t)
	member := f.member(t, "member")
	admin := policy.Subject(f.admin.Principal)
	subject := policy.Subject(member.Principal)
	first, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "First", PermissionCodes: []rbac.Permission{rbac.UsersView, rbac.LogsView}})
	must(t, e)
	second, e := f.rb.CreateRole(ctx, admin, rbac.RoleInput{Name: "Second", PermissionCodes: []rbac.Permission{rbac.UsersView, rbac.NotificationsView}})
	must(t, e)
	_, e = f.rb.SetAccountRoles(ctx, admin, member.Account.ID, []rbac.RoleID{second.ID, first.ID, second.ID})
	must(t, e)
	effective, e := f.rb.Me(ctx, subject)
	must(t, e)
	if len(effective.Roles) != 2 || effective.Roles[0].ID != first.ID || effective.Roles[1].ID != second.ID {
		t.Fatal("roles not deduplicated in ID order")
	}
	shared := false
	for _, grant := range effective.Permissions {
		if grant.Code == rbac.UsersView {
			shared = true
			if len(grant.RoleIDs) != 2 || grant.RoleIDs[0] != first.ID || grant.RoleIDs[1] != second.ID {
				t.Fatal("lost grant sources")
			}
		}
	}
	if !shared || len(effective.Permissions) != 3 {
		t.Fatal("wrong permission union")
	}
	_, e = f.rb.UpdateRole(ctx, admin, first.ID, rbac.RoleInput{Name: "First", PermissionCodes: []rbac.Permission{rbac.LogsView}})
	must(t, e)
	must(t, f.rb.Authorize(ctx, subject, rbac.UsersView))
	_, e = f.rb.SetAccountRoles(ctx, admin, member.Account.ID, []rbac.RoleID{first.ID})
	must(t, e)
	want(t, f.rb.Authorize(ctx, subject, rbac.UsersView), rbac.ErrForbidden)
	must(t, f.rb.Authorize(ctx, subject, rbac.LogsView))
}
