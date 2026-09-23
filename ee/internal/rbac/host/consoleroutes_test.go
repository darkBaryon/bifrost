// 本文件确认基础状态只登记一个精确POST，原根路由保护继续拒绝方法和路径别名。
package host

import (
	"testing"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

func TestConsoleRouteContract(t *testing.T) {
	count := 0
	for _, entry := range manifest() {
		if entry.Kind == kindConsoleService {
			count++
			if entry.Method != "POST" || entry.Pattern != "/api/console/bootstrap" || len(entry.Permissions) != 0 || entry.Guard != guardNone || entry.Projection != projectionProtocol {
				t.Fatalf("unexpected console route: %+v", entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("console routes=%d", count)
	}
	r := router.New()
	r.POST("/api/console/bootstrap", func(c *fasthttp.RequestCtx) { c.SetStatusCode(204) })
	a := NewAdapter(nil, nil, nil)
	if err := a.VerifyRoutes(r); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/api/console/bootstrap", 204},
		{"GET", "/api/console/bootstrap", 405},
		{"PUT", "/api/console/bootstrap", 405},
		{"OPTIONS", "/api/console/bootstrap", 405},
		{"POST", "/api/console/bootstrap/", 400},
		{"POST", "/api/console//bootstrap", 400},
		{"POST", "/api/console/../console/bootstrap", 400},
		{"POST", "/api/console/%2fbootstrap", 400},
		{"POST", "/api/Console/bootstrap", 404},
		{"POST", "/api/console/bootstrap/extra", 404},
		{"POST", "/api/console/bootstraps", 404},
	} {
		c := requestCtx(tc.method, tc.path, "{}")
		a.RootGuard(r.Handler)(c)
		if c.Response.StatusCode() != tc.status {
			t.Errorf("%s %s=%d want=%d", tc.method, tc.path, c.Response.StatusCode(), tc.status)
		}
		if tc.status == 405 && string(c.Response.Header.Peek("Allow")) != "POST" {
			t.Error("wrong allowed methods")
		}
	}
}
