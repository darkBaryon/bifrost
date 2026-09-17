// Package identityhttp 提供身份 API、Cookie 和同源验证，不承担存储事务。
// 本文件维护路由表与端点共同的前置规则；端点按线见 session.go、accounts.go、passwords.go，协议管道见 protocol.go。
package identityhttp

import (
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const (
	authType = "password" // 状态响应中的认证类型

	messageLoginSuccessful  = "Login successful"
	messageLogoutSuccessful = "Logout successful"
	messagePasswordChanged  = "Password changed; log in again"
)

// Handler 持有三条线的身份服务与已验证的部署 origin；不信任请求中的代理头。
type Handler struct {
	svc    *identity.Services
	origin string
	secure bool
}

// NewHandler 绑定身份服务与部署 origin。
func NewHandler(svc *identity.Services, origin string) (*Handler, error) {
	o, secure, err := ValidateOrigin(origin)
	if err != nil {
		return nil, err
	}
	return &Handler{svc: svc, origin: o, secure: secure}, nil
}

// Origin 返回已验证的部署 origin，供宿主适配做 WebSocket 握手的来源检查。
func (h *Handler) Origin() string { return h.origin }

// endpoint 解码请求、调用服务并返回状态码与响应体；错误统一由 Error 映射。
type endpoint func(h *Handler, r request) (int, any, error)

// route 是路由表中的一项：name 用于诊断操作名；默认要求会话，匿名与空正文能力须显式声明。
type route struct {
	method, path   string
	name           string
	handle         endpoint
	allowAnonymous bool
	allowEmptyBody bool // 仅旧会话接口允许没有请求体
}

// routes 是路由的唯一来源，同时供 OwnsRoute 与 RegisterRoutes 使用；旧 /api/session 路径复用同一端点。
var routes = [...]route{
	{method: fasthttp.MethodPost, path: "/api/identity/status", name: "status", handle: (*Handler).status, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/initialize", name: "initialize", handle: (*Handler).initialize, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/login", name: "login", handle: (*Handler).login, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/logout", name: "logout", handle: (*Handler).logout, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/me", name: "me", handle: (*Handler).me},
	{method: fasthttp.MethodPost, path: "/api/identity/change-password", name: "change-password", handle: (*Handler).changePassword},
	{method: fasthttp.MethodPost, path: "/api/accounts/create", name: "create", handle: (*Handler).createAccount},
	{method: fasthttp.MethodPost, path: "/api/accounts/delete", name: "delete", handle: (*Handler).deleteAccount},
	{method: fasthttp.MethodPost, path: "/api/accounts/list", name: "list", handle: (*Handler).listAccounts},
	{method: fasthttp.MethodPost, path: "/api/accounts/set-status", name: "set-status", handle: (*Handler).setStatus},
	{method: fasthttp.MethodPost, path: "/api/accounts/reset-password", name: "reset-password", handle: (*Handler).resetPassword},
	{method: fasthttp.MethodPost, path: "/api/identity/password-events", name: "password-events", handle: (*Handler).passwordEvents},
	{method: fasthttp.MethodPost, path: "/api/identity/ws-ticket", name: "ws-ticket", handle: (*Handler).wsTicket},
	{method: fasthttp.MethodPost, path: "/api/session/login", name: "login", handle: (*Handler).login, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/session/logout", name: "logout", handle: (*Handler).logout, allowAnonymous: true, allowEmptyBody: true},
	{method: fasthttp.MethodPost, path: "/api/session/ws-ticket", name: "ws-ticket", handle: (*Handler).wsTicket, allowEmptyBody: true},
	{method: fasthttp.MethodGet, path: "/api/session/is-auth-enabled", name: "status", handle: (*Handler).status, allowAnonymous: true},
}

// OwnsRoute 精确判断方法与路径是否属于本包，宿主据此把请求交给端点自行认证。
func OwnsRoute(method, path string) bool {
	for _, rt := range routes {
		if rt.method == method && rt.path == path {
			return true
		}
	}
	return false
}

// RegisterRoutes 按路由表注册端点，并套上宿主提供的中间件。
func (h *Handler) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	for _, rt := range routes {
		r.Handle(rt.method, rt.path, lib.ChainMiddlewares(h.serve(rt), m...))
	}
}

// serve 执行端点共同的前置规则：禁止缓存、非 GET 必须同源、需要会话的端点拒绝旧 Authorization 头并验证 Cookie。
func (h *Handler) serve(rt route) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		r := request{ctx: OperationContext(c, "identity."+rt.name), c: c, route: rt}
		c.Response.Header.Set("Cache-Control", "no-store")
		if rt.method != fasthttp.MethodGet && !h.SameOrigin(c) {
			Error(c, identity.ErrForbidden)
			return
		}
		if !rt.allowAnonymous {
			if len(c.Request.Header.Peek("Authorization")) != 0 {
				Error(c, identity.ErrUnauthorized)
				return
			}
			p, err := h.svc.Session.Authenticate(r.ctx, r.cookie())
			if err != nil {
				Error(c, err)
				return
			}
			r.principal = p
		}
		code, body, err := rt.handle(h, r)
		if err != nil {
			Error(c, err)
			return
		}
		JSON(c, code, body)
	}
}
