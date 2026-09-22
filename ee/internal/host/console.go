// 本文件向个人会话提供外壳需要的最少宿主状态；完整设置仍由原接口授权。
package host

import (
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const consoleBootstrapPath = "/api/console/bootstrap"

// ConsoleLogger 只记录基础状态读取失败的固定原因及请求编号，由app注入。
type ConsoleLogger interface{ Warn(string, ...any) }

// WithConsoleLogger 注入基础状态诊断日志；缺少logger不影响基础状态响应。
func WithConsoleLogger(log ConsoleLogger) AuthOption {
	return func(a *AuthAdapter) { a.consoleLog = log }
}

// consoleBootstrapResponse 独立于完整配置定义白名单，避免新增宿主字段意外进入外壳响应。
type consoleBootstrapResponse struct {
	IsDBConnected   bool                    `json:"is_db_connected"`
	IsLogsConnected bool                    `json:"is_logs_connected"`
	EnvLabel        *string                 `json:"env_label"`
	RestartRequired *consoleRestartRequired `json:"restart_required,omitempty"`
}

type consoleRestartRequired struct {
	Required bool `json:"required"`
}

func ownsConsoleRoute(method, path string) bool {
	return method == fasthttp.MethodPost && path == consoleBootstrapPath
}

func (a *AuthAdapter) registerConsoleRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	r.POST(consoleBootstrapPath, lib.ChainMiddlewares(a.serveConsoleBootstrap, m...))
}

func (a *AuthAdapter) serveConsoleBootstrap(c *fasthttp.RequestCtx) {
	ctx := identityhttp.OperationContext(c, "console.bootstrap")
	c.Response.Header.Set("Cache-Control", "no-store")
	if !a.http.SameOrigin(c) {
		identityhttp.Error(c, identity.ErrForbidden)
		return
	}
	if len(c.Request.Header.Peek("Authorization")) != 0 {
		identityhttp.Error(c, identity.ErrUnauthorized)
		return
	}
	// 只验证个人会话，不执行完整管理权限或强制改密检查。
	if _, err := a.service.Authenticate(ctx, string(c.Request.Header.Cookie(identityhttp.CookieName))); err != nil {
		identityhttp.Error(c, err)
		return
	}
	if err := identityhttp.DecodeJSONObject(c, &struct{}{}); err != nil {
		identityhttp.Error(c, err)
		return
	}
	if a.host == nil || a.host.Config == nil {
		identityhttp.Error(c, identity.ErrUnavailable)
		return
	}
	// 连接状态与环境标识对齐 transports/bifrost-http/handlers/config.go 的同名字段语义；合并上游时须同步核对。
	config := a.host.Config
	response := consoleBootstrapResponse{IsDBConnected: config.ConfigStore != nil, IsLogsConnected: config.LogsStore != nil}
	if config.EnvLabel != "" {
		label := config.EnvLabel
		response.EnvLabel = &label
	}
	if config.ConfigStore != nil {
		restart, err := config.ConfigStore.GetRestartRequiredConfig(ctx)
		if err != nil {
			// 重启标记是附属状态，读取失败时省略，避免阻挡工作台进入。
			if a.consoleLog != nil {
				_, requestID := identity.DiagnosticOperation(ctx)
				a.consoleLog.Warn("EE console bootstrap reason=restart_read_failed request_id=%s", requestID)
			}
		} else if restart != nil {
			response.RestartRequired = &consoleRestartRequired{Required: restart.Required}
		}
	}
	identityhttp.JSON(c, fasthttp.StatusOK, response)
}
