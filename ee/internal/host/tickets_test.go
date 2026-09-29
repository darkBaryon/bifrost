// 本文件用真实账号与角色验证票据消费和通知权限处于同一事务，权限失败可回滚重试。
package host

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbacpolicy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestTicketConsumptionRollsBackWhenPermissionIsRevoked(t *testing.T) {
	a, admin, fixture := testAdapter(t)
	ctx := context.Background()
	member, roleID := notificationMember(t, fixture, admin)
	chief := rbacpolicy.Subject(admin.Principal)
	setRoles := func(ids []rbac.RoleID) {
		t.Helper()
		if _, err := fixture.Permissions.SetAccountRoles(ctx, chief, member.Account.ID, ids); err != nil {
			t.Fatal(err)
		}
	}
	ticket, err := fixture.Tickets.IssueTicket(ctx, member.Principal)
	if err != nil {
		t.Fatal(err)
	}
	consumedAt := func() *time.Time { return ticketConsumedAt(t, fixture, ticket) }
	routes := router.New()
	called := false
	routes.GET("/ws", a.APIMiddleware()(func(c *fasthttp.RequestCtx) {
		called = true
		if principal, ok := c.UserValue(principalKey{}).(session.Principal); !ok || principal != member.Principal {
			t.Error("票据没有交付重新核实的当前身份")
		}
		c.SetStatusCode(fasthttp.StatusNoContent)
	}))
	verifyHostRoutes(t, a, routes)
	handler := fixture.Access.RootGuard(routes.Handler)
	setRoles(nil)
	if raw, err := fixture.Tickets.IssueTicket(ctx, member.Principal); !errors.Is(err, auth.ErrForbidden) || raw != "" {
		t.Fatalf("撤回权限后仍能签票：raw=%q err=%v", raw, err)
	}
	c := consoleRequest(handler, "GET", "/ws?ticket="+ticket, "", "", a.http.Origin(), nil)
	if c.Response.StatusCode() != fasthttp.StatusForbidden || called {
		t.Fatalf("撤回权限后消费结果：status=%d called=%v", c.Response.StatusCode(), called)
	}
	if consumedAt() != nil {
		t.Fatal("通知权限校验失败后，消费标记没有在同一事务回滚")
	}
	// 恢复权限后重试同一张票据，证明失败只回滚消费，没有销毁可重试票据。
	setRoles([]rbac.RoleID{roleID})
	c = consoleRequest(handler, "GET", "/ws?ticket="+ticket, "", "", a.http.Origin(), nil)
	if c.Response.StatusCode() != fasthttp.StatusNoContent || !called || consumedAt() == nil {
		t.Fatalf("恢复权限后同票据重试失败：status=%d called=%v", c.Response.StatusCode(), called)
	}
	called = false
	c = consoleRequest(handler, "GET", "/ws?ticket="+ticket, "", "", a.http.Origin(), nil)
	if c.Response.StatusCode() != fasthttp.StatusUnauthorized || called {
		t.Fatalf("提交消费后仍可重放：status=%d called=%v", c.Response.StatusCode(), called)
	}
}

func TestTicketConsumptionRollsBackWhenSessionBecomesInvalid(t *testing.T) {
	for _, action := range []string{"撤销会话", "停用账号"} {
		t.Run(action, func(t *testing.T) {
			_, admin, fixture := testAdapter(t)
			ctx := context.Background()
			member, _ := notificationMember(t, fixture, admin)
			ticket, err := fixture.Tickets.IssueTicket(ctx, member.Principal)
			if err != nil {
				t.Fatal(err)
			}
			if action == "撤销会话" {
				err = fixture.Auth.Logout(ctx, member.Token)
			} else {
				_, err = fixture.Account.SetAccountStatus(ctx, admin.Principal, member.Account.ID, identity.StatusDisabled)
			}
			if err != nil {
				t.Fatal(err)
			}
			principal, err := fixture.Tickets.ConsumeTicket(ctx, ticket)
			if !errors.Is(err, auth.ErrUnauthorized) || principal != (session.Principal{}) {
				t.Fatalf("失效会话消费结果：principal=%+v err=%v", principal, err)
			}
			if ticketConsumedAt(t, fixture, ticket) != nil {
				t.Fatal("会话复验失败后，消费标记没有在同一事务回滚")
			}
		})
	}
}

func notificationMember(t *testing.T, fixture *hostFixture, admin auth.LoginResult) (auth.LoginResult, rbac.RoleID) {
	t.Helper()
	ctx := context.Background()
	if _, err := fixture.Account.CreateAccount(ctx, admin.Principal, "subscriber", "Subscriber"); err != nil {
		t.Fatal(err)
	}
	member, err := fixture.Auth.Login(ctx, "subscriber", "123456", "peer")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.Password.ChangePassword(ctx, member.Principal, "123456", "Subscriber-password-2"); err != nil {
		t.Fatal(err)
	}
	member, err = fixture.Auth.Login(ctx, "subscriber", "Subscriber-password-2", "peer")
	if err != nil {
		t.Fatal(err)
	}
	chief := rbacpolicy.Subject(admin.Principal)
	role, err := fixture.Permissions.CreateRole(ctx, chief, rbac.RoleInput{Name: "notifications", PermissionCodes: []rbac.Permission{rbac.NotificationsView}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.Permissions.SetAccountRoles(ctx, chief, member.Account.ID, []rbac.RoleID{role.ID}); err != nil {
		t.Fatal(err)
	}
	return member, role.ID
}

func ticketConsumedAt(t *testing.T, fixture *hostFixture, raw string) *time.Time {
	t.Helper()
	var row struct{ ConsumedAt *time.Time }
	if err := fixture.DB.Table("ee_identity_ws_tickets").Where("hash = ?", session.Digest(raw)).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row.ConsumedAt
}
