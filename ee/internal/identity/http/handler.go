// Package identityhttp 提供账号管理、初始化和密码 HTTP 接口。
// 登录与会话接口由 auth/http 负责；本文件维护账号路由和入口检查。
package identityhttp

import (
	"github.com/darkBaryon/bifrost/ee/internal/auth"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/auth/http"
	"github.com/darkBaryon/bifrost/ee/internal/httpx"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const messagePasswordChanged = "Password changed; log in again"

// Handler 接收账号和密码业务、认证服务与浏览器安全策略。
type Handler struct {
	svc            *identity.Services
	authentication *auth.Service
	browser        *authhttp.Browser
}

// NewHandler 创建账号管理入口；登录与会话兼容接口由 auth/http 独立注册。
func NewHandler(svc *identity.Services, authentication *auth.Service, browser *authhttp.Browser) (*Handler, error) {
	if svc == nil || svc.Account == nil || svc.Password == nil || authentication == nil || browser == nil {
		return nil, identity.ErrInvalid
	}
	return &Handler{svc: svc, authentication: authentication, browser: browser}, nil
}

// endpoint 解码请求、调用服务并返回状态码与响应体；错误统一由 writeError 映射。
type endpoint func(h *Handler, r request) (int, any, error)

// route 是路由表中的一项：name 用于诊断操作名；默认要求会话，匿名能力须显式声明。
type route struct {
	method, path   string
	name           string
	handle         endpoint
	allowAnonymous bool
}

// routes 只声明账号与密码接口，保留既有客户端路径。
var routes = [...]route{
	{method: fasthttp.MethodPost, path: "/api/identity/initialize", name: "initialize", handle: (*Handler).initialize, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/change-password", name: "change-password", handle: (*Handler).changePassword},
	{method: fasthttp.MethodPost, path: "/api/accounts/create", name: "create", handle: (*Handler).createAccount},
	{method: fasthttp.MethodPost, path: "/api/accounts/delete", name: "delete", handle: (*Handler).deleteAccount},
	{method: fasthttp.MethodPost, path: "/api/accounts/list", name: "list", handle: (*Handler).listAccounts},
	{method: fasthttp.MethodPost, path: "/api/accounts/set-status", name: "set-status", handle: (*Handler).setStatus},
	{method: fasthttp.MethodPost, path: "/api/accounts/reset-password", name: "reset-password", handle: (*Handler).resetPassword},
	{method: fasthttp.MethodPost, path: "/api/identity/password-events", name: "password-events", handle: (*Handler).passwordEvents},
}

// RegisterRoutes 注册账号和密码接口；只接通用中间件，认证由入口检查。
func (h *Handler) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	for _, rt := range routes {
		r.Handle(rt.method, rt.path, lib.ChainMiddlewares(h.serve(rt), m...))
	}
}

// serve 执行端点共同的前置规则：禁止缓存、非 GET 必须同源、需要会话的端点拒绝旧 Authorization 头并验证 Cookie。
func (h *Handler) serve(rt route) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		r := request{ctx: httpx.OperationContext(c, "identity."+rt.name), c: c}
		c.Response.Header.Set("Cache-Control", "no-store")
		if rt.method != fasthttp.MethodGet && !h.browser.SameOrigin(c) {
			writeError(c, identity.ErrForbidden)
			return
		}
		if !rt.allowAnonymous {
			if len(c.Request.Header.Peek("Authorization")) != 0 {
				writeError(c, identity.ErrUnauthorized)
				return
			}
			p, err := h.authentication.Authenticate(r.ctx, r.cookie())
			if err != nil {
				writeError(c, err)
				return
			}
			r.principal = p
		}
		code, body, err := rt.handle(h, r)
		if err != nil {
			writeError(c, err)
			return
		}
		httpx.JSON(c, code, body)
	}
}
