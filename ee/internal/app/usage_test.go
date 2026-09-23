// 本文件验证新增模板入口不会覆盖已有角色路由，并被宿主清单识别。
package app

import (
	"testing"

	rbachost "github.com/darkBaryon/bifrost/ee/internal/rbac/host"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	usagehttp "github.com/darkBaryon/bifrost/ee/internal/usage/http"
	"github.com/fasthttp/router"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestConsoleRoutesKeepRolesAndTemplates(t *testing.T) {
	routes := consoleRoutes{rbachttp.NewHandler(nil, nil, nil), usagehttp.NewHandler(nil, nil, nil)}
	r := router.New()
	routes.RegisterRoutes(r)
	require.NoError(t, rbachost.NewAdapter(nil, nil, nil).VerifyRoutes(r))
	for _, path := range []string{"/api/roles/list", "/api/usage/list-templates", "/api/usage/create-template", "/api/usage/update-template", "/api/usage/delete-template"} {
		require.True(t, routes.OwnsRoute("POST", path))
		require.False(t, routes.OwnsRoute("GET", path))
		var c fasthttp.RequestCtx
		c.Request.Header.SetMethod("POST")
		c.Request.SetRequestURI(path)
		r.Handler(&c)
		require.Equal(t, 503, c.Response.StatusCode())
	}
}
