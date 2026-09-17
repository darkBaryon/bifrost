// 本文件断言拒绝可关联到安全诊断，正文和内部错误不会进入日志。
package host

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

type diagnosticLog struct{ lines []string }

func (l *diagnosticLog) Warn(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func TestProjectionFailureHasSafeDiagnostic(t *testing.T) {
	log := &diagnosticLog{}
	repository := &routeRepository{codes: []rbac.Permission{rbac.ModelProviderView}}
	adapter := NewAdapter(rbac.New(repository), nil, log)
	routes := router.New()
	routes.GET("/api/providers", func(*fasthttp.RequestCtx) {})
	if e := adapter.VerifyRoutes(routes); e != nil {
		t.Fatal(e)
	}
	c := requestCtx("GET", "/api/providers", "")
	ctx := identity.WithDiagnosticOperation(context.Background(), "rbac.response")
	wrap, e := adapter.Prepare(ctx, c, identity.Principal{AccountID: "member"})
	if e != nil {
		t.Fatal(e)
	}
	wrap(func(c *fasthttp.RequestCtx) {
		c.Response.Header.SetContentType("application/json")
		c.SetBodyString(`{"providers":"RAW_SECRET_BODY"}`)
	})(c)
	if c.Response.StatusCode() != 503 || len(log.lines) != 1 {
		t.Fatal("projection failure did not produce diagnostic", c.Response.StatusCode(), log.lines)
	}
	for _, field := range []string{"operation=rbac.response", "route=/api/providers", "reason=invalid_projection", "incident="} {
		if !strings.Contains(log.lines[0], field) {
			t.Fatal("missing safe diagnostic field", field)
		}
	}
	if strings.Contains(log.lines[0], "RAW_SECRET_BODY") || strings.Contains(string(c.Response.Body()), "RAW_SECRET_BODY") {
		t.Fatal("projection error leaked body")
	}
}

func TestUnknownMessageHasSafeDiagnostic(t *testing.T) {
	log := &diagnosticLog{}
	repository := &routeRepository{codes: []rbac.Permission{rbac.NotificationsView}}
	filter := NewAdapter(rbac.New(repository), nil, log).messageFilter(rbac.Subject{AccountID: "member"})
	ok, e := filter(context.Background(), []byte(`{"type":"future-message","data":{"secret":"RAW_SECRET_BODY"}}`))
	if ok || e != nil || len(log.lines) != 1 {
		t.Fatal("unknown message was not diagnosed and skipped", ok, e, log.lines)
	}
	if !strings.Contains(log.lines[0], "unknown_message_type") || !strings.Contains(log.lines[0], "operation=rbac.websocket") || strings.Contains(log.lines[0], "RAW_SECRET_BODY") {
		t.Fatal("unsafe or incomplete message diagnostic")
	}
	repository.failure = fmt.Errorf("RAW_DRIVER_SECRET")
	ok, e = filter(context.Background(), []byte(`{"type":"heartbeat"}`))
	if ok || e == nil || len(log.lines) != 2 || !strings.Contains(log.lines[1], "message_rejected") || strings.Contains(log.lines[1], "RAW_DRIVER_SECRET") {
		t.Fatal("message query failure not safely diagnosed")
	}
}
