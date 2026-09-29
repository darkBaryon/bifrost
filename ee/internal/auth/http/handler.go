// Package authhttp 提供登录认证的 HTTP 入口与浏览器 Cookie、同源规则；业务判断交给 auth 和显式注入的票据入口。
package authhttp

import (
	"context"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/httpx"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// TicketIssuer 在业务入口的同一事务内核实身份和通知权限后签发票据。
type TicketIssuer interface {
	IssueTicket(context.Context, session.Principal) (string, error)
}

// Handler 注册登录、登出、当前身份与票据端点，并使用显式注入的浏览器协议。
type Handler struct {
	browser        *Browser
	authentication *auth.Service
	tickets        TicketIssuer
	isInitialized  func(context.Context) (bool, error)
}

// NewHandler 显式接收认证、票据、初始化状态查询和浏览器协议；任一依赖缺失都拒绝构造。
func NewHandler(authentication *auth.Service, tickets TicketIssuer, isInitialized func(context.Context) (bool, error), browser *Browser) (*Handler, error) {
	if authentication == nil || tickets == nil || isInitialized == nil || browser == nil {
		return nil, auth.ErrInvalid
	}
	return &Handler{browser: browser, authentication: authentication, tickets: tickets, isInitialized: isInitialized}, nil
}

type endpoint func(h *Handler, r request) (int, any, error)

// route 默认要求会话；匿名与旧接口的空正文兼容需要逐项声明。
type route struct {
	method, path   string
	name           string
	handle         endpoint
	allowAnonymous bool
	allowEmptyBody bool
}

var routes = [...]route{
	{method: fasthttp.MethodPost, path: "/api/identity/status", name: "status", handle: (*Handler).status, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/login", name: "login", handle: (*Handler).login, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/logout", name: "logout", handle: (*Handler).logout, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/me", name: "me", handle: (*Handler).me},
	{method: fasthttp.MethodPost, path: "/api/identity/ws-ticket", name: "ws-ticket", handle: (*Handler).wsTicket},
}

var sessionRoutes = [...]route{
	{method: fasthttp.MethodPost, path: "/api/session/login", name: "login", handle: (*Handler).login, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/session/logout", name: "logout", handle: (*Handler).logout, allowAnonymous: true, allowEmptyBody: true},
	{method: fasthttp.MethodPost, path: "/api/session/ws-ticket", name: "ws-ticket", handle: (*Handler).wsTicket, allowEmptyBody: true},
	{method: fasthttp.MethodGet, path: "/api/session/is-auth-enabled", name: "status", handle: (*Handler).status, allowAnonymous: true},
}

// RegisterRoutes 注册 EE 认证接口，保留已有 URL；传入宿主通用中间件，端点按需验证 Cookie。
func (h *Handler) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	h.registerRoutes(r, routes[:], m...)
}

// RegisterSessionRoutes 替换 BF 的四条会话接口，保留旧 URL、GET 状态查询与已声明的空正文兼容。
func (h *Handler) RegisterSessionRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	h.registerRoutes(r, sessionRoutes[:], m...)
}

func (h *Handler) registerRoutes(r *router.Router, registered []route, m ...schemas.BifrostHTTPMiddleware) {
	for _, rt := range registered {
		r.Handle(rt.method, rt.path, lib.ChainMiddlewares(h.serve(rt), m...))
	}
}

func (h *Handler) serve(rt route) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		// 保留已有诊断操作名，便于沿用审计与故障关联。
		r := request{ctx: httpx.OperationContext(c, "identity."+rt.name), c: c, route: rt}
		c.Response.Header.Set("Cache-Control", "no-store")
		if rt.method != fasthttp.MethodGet && !h.browser.SameOrigin(c) {
			Error(c, auth.ErrForbidden)
			return
		}
		if !rt.allowAnonymous {
			if len(c.Request.Header.Peek("Authorization")) != 0 {
				Error(c, auth.ErrUnauthorized)
				return
			}
			p, err := h.authentication.Authenticate(r.ctx, r.cookie())
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
		httpx.JSON(c, code, body)
	}
}

type request struct {
	ctx       context.Context
	c         *fasthttp.RequestCtx
	principal session.Principal
	route     route
}

func (r request) decode(v any) error {
	if r.route.allowEmptyBody {
		return httpx.DecodeOptionalJSONObject(r.c, v)
	}
	return httpx.DecodeJSONObject(r.c, v)
}

func (r request) cookie() string { return string(r.c.Request.Header.Cookie(CookieName)) }
