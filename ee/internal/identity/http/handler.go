// Package identityhttp 提供身份 API、Cookie 和同源验证，不承担存储事务。
// 本文件维护路由表、端点共同的前置规则与各端点适配；协议管道（origin、Cookie、JSON、错误映射）见 protocol.go。
package identityhttp

import (
	"errors"
	"time"

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

// status 供登录页判断是否初始化及当前 Cookie 是否有效；没有会话时不暴露账号信息。
func (h *Handler) status(r request) (int, any, error) {
	if !r.c.IsGet() {
		if err := r.decode(&emptyRequest{}); err != nil {
			return 0, nil, err
		}
	}
	state, err := h.svc.Session.State(r.ctx)
	if err != nil {
		return 0, nil, err
	}
	p, err := h.svc.Session.Authenticate(r.ctx, r.cookie())
	if err != nil && !errors.Is(err, identity.ErrUnauthorized) {
		return 0, nil, err
	}
	valid := err == nil
	return fasthttp.StatusOK, statusResponse{Initialized: state.Initialized, AuthType: authType, IsAuthEnabled: true,
		HasValidToken: valid, HasValidSession: valid, MustChangePassword: valid && p.MustChangePassword}, nil
}

func (h *Handler) initialize(r request) (int, any, error) {
	var q initializeRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	a, err := h.svc.Account.Initialize(r.ctx, q.SetupToken, q.Username, q.Password)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusCreated, accountResponse{Account: toAccountDTO(a)}, nil
}

// login 用真实 TCP 来源地址参与限流，不信任代理头；成功后只通过 Cookie 交付 token。
func (h *Handler) login(r request) (int, any, error) {
	var q loginRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	v, err := h.svc.Session.Login(r.ctx, q.Username, q.Password, r.c.RemoteIP().String())
	if err != nil {
		return 0, nil, err
	}
	h.setCookie(r.c, v.Token, v.ExpiresAt)
	return fasthttp.StatusOK, loginResponse{Message: messageLoginSuccessful, Account: toAccountDTO(v.Account),
		MustChangePassword: v.Principal.MustChangePassword, ExpiresAt: v.ExpiresAt}, nil
}

// logout 无论 Cookie 是否有效都返回成功并清除 Cookie。
func (h *Handler) logout(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	if err := h.svc.Session.Logout(r.ctx, r.cookie()); err != nil {
		return 0, nil, err
	}
	h.clearCookie(r.c)
	return fasthttp.StatusOK, messageResponse{Message: messageLogoutSuccessful}, nil
}

func (h *Handler) me(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	a, err := h.svc.Session.Me(r.ctx, r.principal)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, accountResponse{Account: toAccountDTO(a)}, nil
}

// changePassword 成功后清除 Cookie，调用方须重新登录。
func (h *Handler) changePassword(r request) (int, any, error) {
	var q changePasswordRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	if err := h.svc.Password.ChangePassword(r.ctx, r.principal, q.OldPassword, q.NewPassword); err != nil {
		return 0, nil, err
	}
	h.clearCookie(r.c)
	return fasthttp.StatusOK, messageResponse{Message: messagePasswordChanged}, nil
}

func (h *Handler) createAccount(r request) (int, any, error) {
	var q createAccountRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	a, err := h.svc.Account.CreateAccount(r.ctx, r.principal, q.Username, q.DisplayName)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusCreated, accountResponse{Account: toAccountDTO(a)}, nil
}

func (h *Handler) listAccounts(r request) (int, any, error) {
	var q listRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	p, err := h.svc.Account.ListAccounts(r.ctx, r.principal, q.Cursor, pageLimit(q.Limit))
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, toAccountPage(p), nil
}

func (h *Handler) setStatus(r request) (int, any, error) {
	var q setStatusRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	a, err := h.svc.Account.SetAccountStatus(r.ctx, r.principal, q.AccountID, identity.AccountStatus(q.Status))
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, accountResponse{Account: toAccountDTO(a)}, nil
}

func (h *Handler) resetPassword(r request) (int, any, error) {
	var q resetPasswordRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	e, err := h.svc.Password.ResetPassword(r.ctx, r.principal, q.AccountID, q.OperationID)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, resetPasswordResponse{EventID: e.ID, Result: string(e.Result)}, nil
}

func (h *Handler) passwordEvents(r request) (int, any, error) {
	var q eventsRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	p, err := h.svc.Password.ListPasswordEvents(r.ctx, r.principal, q.TargetID, q.Cursor, pageLimit(q.Limit))
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, toEventPage(p), nil
}

func (h *Handler) wsTicket(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	ticket, err := h.svc.Session.IssueTicket(r.ctx, r.principal)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, ticketResponse{Ticket: ticket, ExpiresIn: int(identity.WSTicketTTL / time.Second)}, nil
}
