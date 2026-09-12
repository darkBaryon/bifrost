// Package identityhttp 提供身份 API、Cookie 和同源验证，不承担存储事务。
// 本文件维护路由表、请求解码、同源与 Cookie 规则、错误映射以及各端点的适配逻辑。
package identityhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

// CookieName 是 EE 会话 Cookie；宿主适配按同一名字读取。
const CookieName = "ee_session"

const (
	legacyCookieName   = "token"    // 旧共享管理员 Cookie，只清除，绝不读取
	deleteCookieMaxAge = -1         // fasthttp 用负数表示立即删除 Cookie
	maxBodyBytes       = 16 << 10   // 身份请求的字节上限，配置接口另用宿主限制
	maxJSONDepth       = 64         // 对象与数组的嵌套深度上限
	authType           = "password" // 状态响应中的认证类型

	messageLoginSuccessful  = "Login successful"
	messageLogoutSuccessful = "Logout successful"
	messagePasswordChanged  = "Password changed; log in again"
)

// Handler 持有身份服务与已验证的部署 origin；不信任请求中的代理头。
type Handler struct {
	service *identity.Service
	origin  string
	secure  bool
}

// ValidateOrigin 解析部署的外部 origin：不含路径、查询、片段与凭据；必须是 https，只有回环地址允许 http。
// 返回规范化的 origin 以及 Cookie 是否应带 Secure。
func ValidateOrigin(raw string) (origin string, secure bool, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false, identity.ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", false, identity.ErrInvalid
	}
	return u.Scheme + "://" + u.Host, u.Scheme == "https", nil
}

// NewHandler 绑定身份服务与部署 origin。
func NewHandler(service *identity.Service, origin string) (*Handler, error) {
	o, secure, err := ValidateOrigin(origin)
	if err != nil {
		return nil, err
	}
	return &Handler{service: service, origin: o, secure: secure}, nil
}

// Origin 返回已验证的部署 origin，供宿主适配做 WebSocket 握手的来源检查。
func (h *Handler) Origin() string { return h.origin }

// OperationContext 为一次入口操作分配关联 ID，并写入请求与响应头，与宿主访问日志的 request_id 对应。
func OperationContext(c *fasthttp.RequestCtx, operation string) context.Context {
	ctx := identity.WithDiagnosticOperation(c, operation)
	_, id := identity.DiagnosticOperation(ctx)
	c.Request.Header.Set("x-request-id", id)
	c.Response.Header.Set("X-Request-ID", id)
	return ctx
}

// SameOrigin 要求 Origin 与部署 origin 精确一致；没有 Origin 时须有同源 Referer，两者冲突拒绝。
func (h *Handler) SameOrigin(c *fasthttp.RequestCtx) bool {
	if raw := string(c.Request.Header.Peek("Referer")); raw != "" {
		r, err := url.Parse(raw)
		if err != nil || r.User != nil || r.Scheme+"://"+r.Host != h.origin {
			return false
		}
	}
	if o := string(c.Request.Header.Peek("Origin")); o != "" {
		return o == h.origin
	}
	return len(c.Request.Header.Peek("Referer")) != 0
}

// setCookie 写入本次会话 Cookie 并删除旧共享管理员 Cookie；空 token 表示退出登录。
func (h *Handler) setCookie(c *fasthttp.RequestCtx, token string, expires time.Time) {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey(CookieName)
	cookie.SetValue(token)
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSecure(h.secure)
	cookie.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	cookie.SetExpire(expires)
	if token == "" {
		cookie.SetMaxAge(deleteCookieMaxAge)
	}
	c.Response.Header.SetCookie(cookie)
	cookie.SetKey(legacyCookieName)
	cookie.SetValue("")
	cookie.SetMaxAge(deleteCookieMaxAge)
	cookie.SetExpire(fasthttp.CookieExpireDelete)
	c.Response.Header.SetCookie(cookie)
}

func (h *Handler) clearCookie(c *fasthttp.RequestCtx) {
	h.setCookie(c, "", fasthttp.CookieExpireDelete)
}

// JSON 输出 JSON 响应；序列化失败时返回固定的 503 错误体，不暴露内部细节。
func JSON(c *fasthttp.RequestCtx, code int, value any) {
	b, err := json.Marshal(value)
	if err != nil {
		code = fasthttp.StatusServiceUnavailable
		b, _ = json.Marshal(errorResponse{Error: errorDetail{Code: identity.ErrUnavailable.Error()}})
	}
	c.SetStatusCode(code)
	c.SetContentType("application/json")
	c.Response.SetBody(b)
}

// Error 把业务错误映射为 HTTP 状态；未识别的故障统一为 503 unavailable，限流附带 Retry-After。
func Error(c *fasthttp.RequestCtx, err error) {
	err = identity.SafeError(err)
	code := fasthttp.StatusServiceUnavailable
	switch err {
	case identity.ErrUnauthorized:
		code = fasthttp.StatusUnauthorized
	case identity.ErrForbidden:
		code = fasthttp.StatusForbidden
	case identity.ErrInvalid:
		code = fasthttp.StatusBadRequest
	case identity.ErrConflict:
		code = fasthttp.StatusConflict
	case identity.ErrNotFound:
		code = fasthttp.StatusNotFound
	case identity.ErrLimited:
		code = fasthttp.StatusTooManyRequests
		c.Response.Header.Set("Retry-After", strconv.Itoa(int(identity.LoginRateWindow/time.Second)))
	}
	JSON(c, code, errorResponse{Error: errorDetail{Code: err.Error(), Message: err.Error()}})
}

// ValidateJSONObject 要求正文是单个 JSON 对象，拒绝重复键、多个值和过深嵌套，避免与宿主解析器产生歧义。
func ValidateJSONObject(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return identity.ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(depth int) error
	walk = func(depth int) error {
		if depth > maxJSONDepth {
			return identity.ErrInvalid
		}
		token, err := d.Token()
		if err != nil {
			return identity.ErrInvalid
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return identity.ErrInvalid
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return identity.ErrInvalid
				}
				seen[k] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return identity.ErrInvalid
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return identity.ErrInvalid
			}
		default:
			return identity.ErrInvalid
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return identity.ErrInvalid
	}
	return nil
}

// decode 限制正文大小、要求 JSON 媒体类型并拒绝未知字段；allowEmpty 只对已声明的旧会话接口把空正文视为 {}。
func decode(c *fasthttp.RequestCtx, v any, allowEmpty bool) error {
	body := c.PostBody()
	if len(body) > maxBodyBytes || !utf8.Valid(body) {
		return identity.ErrInvalid
	}
	media, _, err := mime.ParseMediaType(string(c.Request.Header.ContentType()))
	if err != nil || media != "application/json" {
		if !allowEmpty || len(body) != 0 {
			return identity.ErrInvalid
		}
	}
	if allowEmpty && len(body) == 0 {
		body = []byte("{}")
	}
	if err := ValidateJSONObject(body); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return identity.ErrInvalid
	}
	return nil
}

// request 是一次端点调用的上下文：诊断 context、fasthttp 请求、已验证身份（匿名端点为零值）及所属路由。
type request struct {
	ctx       context.Context
	c         *fasthttp.RequestCtx
	principal identity.Principal
	route     route
}

func (r request) decode(v any) error { return decode(r.c, v, r.route.allowEmptyBody) }

func (r request) cookie() string { return string(r.c.Request.Header.Cookie(CookieName)) }

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
			p, err := h.service.Authenticate(r.ctx, r.cookie())
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
	state, err := h.service.State(r.ctx)
	if err != nil {
		return 0, nil, err
	}
	p, err := h.service.Authenticate(r.ctx, r.cookie())
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
	a, err := h.service.Initialize(r.ctx, q.SetupToken, q.Username, q.Password)
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
	v, err := h.service.Login(r.ctx, q.Username, q.Password, r.c.RemoteIP().String())
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
	if err := h.service.Logout(r.ctx, r.cookie()); err != nil {
		return 0, nil, err
	}
	h.clearCookie(r.c)
	return fasthttp.StatusOK, messageResponse{Message: messageLogoutSuccessful}, nil
}

func (h *Handler) me(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	a, err := h.service.Me(r.ctx, r.principal)
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
	if err := h.service.ChangePassword(r.ctx, r.principal, q.OldPassword, q.NewPassword); err != nil {
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
	a, err := h.service.CreateAccount(r.ctx, r.principal, q.Username, q.DisplayName)
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
	p, err := h.service.ListAccounts(r.ctx, r.principal, q.Cursor, pageLimit(q.Limit))
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
	a, err := h.service.SetAccountStatus(r.ctx, r.principal, q.AccountID, identity.AccountStatus(q.Status))
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
	e, err := h.service.ResetPassword(r.ctx, r.principal, q.AccountID, q.OperationID)
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
	p, err := h.service.ListPasswordEvents(r.ctx, r.principal, q.TargetID, q.Cursor, pageLimit(q.Limit))
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, toEventPage(p), nil
}

func (h *Handler) wsTicket(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	ticket, err := h.service.IssueTicket(r.ctx, r.principal)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, ticketResponse{Ticket: ticket, ExpiresIn: int(identity.WSTicketTTL / time.Second)}, nil
}
