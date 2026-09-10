// Package identityhttp 提供身份API、Cookie和同源验证，不承担存储事务。
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

const CookieName = "ee_session"

const (
	legacyCookieName        = "token"
	deleteCookieMaxAge      = -1       // fasthttp用负数表示立即删除Cookie。
	maxIdentityBodyBytes    = 16 << 10 // 身份请求的字节上限，配置接口另用宿主限制。
	maxJSONDepth            = 64       // 包含对象和数组的递归深度上限。
	passwordAuthType        = "password"
	messageLoginSuccessful  = "Login successful"
	messageLogoutSuccessful = "Logout successful"
	messagePasswordChanged  = "Password changed; log in again"
)

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}
type errorResponse struct {
	Error errorDetail `json:"error"`
}
type messageResponse struct {
	Message string `json:"message"`
}
type accountResponse struct {
	Account identity.Account `json:"account"`
}
type statusResponse struct {
	Initialized        bool   `json:"initialized"`
	AuthType           string `json:"auth_type"`
	IsAuthEnabled      bool   `json:"is_auth_enabled"`
	HasValidToken      bool   `json:"has_valid_token"`
	HasValidSession    bool   `json:"has_valid_session"`
	MustChangePassword bool   `json:"must_change_password"`
}
type loginResponse struct {
	Message            string           `json:"message"`
	Account            identity.Account `json:"account"`
	MustChangePassword bool             `json:"must_change_password"`
	ExpiresAt          time.Time        `json:"expires_at"`
}
type resetPasswordResponse struct {
	EventID string `json:"event_id"`
	Result  string `json:"result"`
}
type ticketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}

// Handler 保存已验证的外部origin，拒绝信任请求中的代理头。
type Handler struct {
	Service *identity.Service
	Origin  string
	secure  bool
}

// OperationContext 将安全数据库诊断与现有访问日志的request_id关联。
func OperationContext(c *fasthttp.RequestCtx, operation string) context.Context {
	ctx := identity.WithDiagnosticOperation(c, operation)
	_, id := identity.DiagnosticOperation(ctx)
	c.Request.Header.Set("x-request-id", id)
	c.Response.Header.Set("X-Request-ID", id)
	return ctx
}

func NewHandler(s *identity.Service, origin string) (*Handler, error) {
	u, e := url.Parse(origin)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, identity.ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return nil, identity.ErrInvalid
	}
	return &Handler{Service: s, Origin: u.Scheme + "://" + u.Host, secure: u.Scheme == "https"}, nil
}

// SameOrigin 要求明确匹配部署origin，Origin存在时不回退Referer。
func (h *Handler) SameOrigin(c *fasthttp.RequestCtx) bool {
	if raw := string(c.Request.Header.Peek("Referer")); raw != "" {
		r, e := url.Parse(raw)
		if e != nil || r.User != nil || r.Scheme+"://"+r.Host != h.Origin {
			return false
		}
	}
	if o := string(c.Request.Header.Peek("Origin")); o != "" {
		return o == h.Origin
	}
	return len(c.Request.Header.Peek("Referer")) != 0
}
// SetCookie 写入本次会话Cookie，并删除旧共享管理员Cookie；空token表示退出登录。
func (h *Handler) SetCookie(c *fasthttp.RequestCtx, token string, expires time.Time) {
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
	// 清理旧共享管理员Cookie，绝不读取它作为EE凭据。
	cookie.SetKey(legacyCookieName)
	cookie.SetValue("")
	cookie.SetMaxAge(deleteCookieMaxAge)
	cookie.SetExpire(fasthttp.CookieExpireDelete)
	c.Response.Header.SetCookie(cookie)
}
// JSON 输出JSON响应，序列化失败时返回不含内部细节的固定503。
func JSON(c *fasthttp.RequestCtx, code int, value any) {
	b, e := json.Marshal(value)
	if e != nil {
		code = fasthttp.StatusServiceUnavailable
		// 固定字符串结构不会序列化失败，复用业务错误码，保留原降级响应形状。
		b, _ = json.Marshal(errorResponse{Error: errorDetail{Code: identity.ErrUnavailable.Error()}})
	}
	c.SetStatusCode(code)
	c.SetContentType("application/json")
	c.Response.SetBody(b)
}
// Error 将业务错误映射为HTTP状态，未知故障使用固定错误码。
func Error(c *fasthttp.RequestCtx, e error) {
	e = identity.SafeError(e)
	code := fasthttp.StatusServiceUnavailable
	switch e {
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
	}
	if code == fasthttp.StatusTooManyRequests {
		c.Response.Header.Set("Retry-After", strconv.Itoa(int(identity.LoginRateWindow/time.Second)))
	}
	JSON(c, code, errorResponse{Error: errorDetail{Code: e.Error(), Message: e.Error()}})
}

// ValidateJSONObject 拒绝重复键和多个JSON值，避免预检与宿主解析器歧义。
func ValidateJSONObject(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > maxJSONDepth {
			return identity.ErrInvalid
		}
		token, e := d.Token()
		if e != nil {
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
				key, e := d.Token()
				if e != nil {
					return identity.ErrInvalid
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return identity.ErrInvalid
				}
				seen[k] = true
				if e = walk(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return identity.ErrInvalid
			}
		case '[':
			for d.More() {
				if e := walk(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return identity.ErrInvalid
			}
		default:
			return identity.ErrInvalid
		}
		return nil
	}
	if len(bytes.TrimSpace(body)) == 0 || bytes.TrimSpace(body)[0] != '{' {
		return identity.ErrInvalid
	}
	if e := walk(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return identity.ErrInvalid
	}
	return nil
}
// Decode 限制JSON大小、深度及字段；allowEmpty仅供已声明的旧会话接口兼容。
func Decode(c *fasthttp.RequestCtx, v any, allowEmpty bool) error {
	if len(c.PostBody()) > maxIdentityBodyBytes || !utf8.Valid(c.PostBody()) {
		return identity.ErrInvalid
	}
	media, _, e := mime.ParseMediaType(string(c.Request.Header.ContentType()))
	if e != nil || media != "application/json" {
		if !allowEmpty || len(c.PostBody()) != 0 {
			return identity.ErrInvalid
		}
	}
	b := c.PostBody()
	if allowEmpty && len(b) == 0 {
		b = []byte("{}")
	}
	if e = ValidateJSONObject(b); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return identity.ErrInvalid
	}
	return nil
}

// operation 只标识本包端点；路径、兼容空体和匿名访问规则在同一张路由表中声明。
type operation string

const (
	operationStatus         operation = "status"
	operationInitialize     operation = "initialize"
	operationLogin          operation = "login"
	operationLogout         operation = "logout"
	operationMe             operation = "me"
	operationChangePassword operation = "change-password"
	operationCreate         operation = "create"
	operationList           operation = "list"
	operationSetStatus      operation = "set-status"
	operationResetPassword  operation = "reset-password"
	operationPasswordEvents operation = "password-events"
	operationWSTicket       operation = "ws-ticket"
)

type routeDefinition struct {
	method, path   string
	operation      operation
	allowAnonymous bool // 默认要求登录；新增路由须显式声明匿名能力。
	allowEmptyBody bool // 仅旧会话兼容接口允许没有请求体。
}

var routes = [...]routeDefinition{
	{method: fasthttp.MethodPost, path: "/api/identity/status", operation: operationStatus, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/initialize", operation: operationInitialize, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/login", operation: operationLogin, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/logout", operation: operationLogout, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/identity/me", operation: operationMe},
	{method: fasthttp.MethodPost, path: "/api/identity/change-password", operation: operationChangePassword},
	{method: fasthttp.MethodPost, path: "/api/accounts/create", operation: operationCreate},
	{method: fasthttp.MethodPost, path: "/api/accounts/list", operation: operationList},
	{method: fasthttp.MethodPost, path: "/api/accounts/set-status", operation: operationSetStatus},
	{method: fasthttp.MethodPost, path: "/api/accounts/reset-password", operation: operationResetPassword},
	{method: fasthttp.MethodPost, path: "/api/identity/password-events", operation: operationPasswordEvents},
	{method: fasthttp.MethodPost, path: "/api/identity/ws-ticket", operation: operationWSTicket},
	{method: fasthttp.MethodPost, path: "/api/session/login", operation: operationLogin, allowAnonymous: true},
	{method: fasthttp.MethodPost, path: "/api/session/logout", operation: operationLogout, allowAnonymous: true, allowEmptyBody: true},
	{method: fasthttp.MethodPost, path: "/api/session/ws-ticket", operation: operationWSTicket, allowEmptyBody: true},
	{method: fasthttp.MethodGet, path: "/api/session/is-auth-enabled", operation: operationStatus, allowAnonymous: true},
}

// OwnsRoute 精确判断方法与路径，供宿主将请求交给本包端点自行认证。
func OwnsRoute(method, path string) bool {
	for _, route := range routes {
		if route.method == method && route.path == path {
			return true
		}
	}
	return false
}
// RegisterRoutes 从同一路由表注册端点及宿主中间件。
func (h *Handler) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	for _, route := range routes {
		r.Handle(route.method, route.path, lib.ChainMiddlewares(h.endpoint(route), m...))
	}
}
func (h *Handler) status(ctx context.Context, c *fasthttp.RequestCtx) {
	state, e := h.Service.State(ctx)
	if e != nil {
		Error(c, e)
		return
	}
	p, e := h.Service.Authenticate(ctx, string(c.Request.Header.Cookie(CookieName)))
	if e != nil && !errors.Is(e, identity.ErrUnauthorized) {
		Error(c, e)
		return
	}
	JSON(c, fasthttp.StatusOK, statusResponse{Initialized: state.Initialized, AuthType: passwordAuthType, IsAuthEnabled: true, HasValidToken: e == nil, HasValidSession: e == nil, MustChangePassword: e == nil && p.MustChangePassword})
}
func (h *Handler) endpoint(route routeDefinition) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		ctx := OperationContext(c, "identity."+string(route.operation))
		c.Response.Header.Set("Cache-Control", "no-store")
		if route.method != fasthttp.MethodGet && !h.SameOrigin(c) {
			Error(c, identity.ErrForbidden)
			return
		}
		var p identity.Principal
		var e error
		if !route.allowAnonymous {
			if len(c.Request.Header.Peek("Authorization")) != 0 {
				Error(c, identity.ErrUnauthorized)
				return
			}
			p, e = h.Service.Authenticate(ctx, string(c.Request.Header.Cookie(CookieName)))
			if e != nil {
				Error(c, e)
				return
			}
		}
		var result any
		code := fasthttp.StatusOK
		switch route.operation {
		case operationStatus:
			if route.method == fasthttp.MethodGet {
				h.status(ctx, c)
				return
			}
			var q struct{}
			if e = Decode(c, &q, false); e == nil {
				h.status(ctx, c)
				return
			}
		case operationInitialize:
			var q struct {
				SetupToken string `json:"setup_token"`
				Username   string `json:"username"`
				Password   string `json:"password"`
			}
			if e = Decode(c, &q, false); e == nil {
				var a identity.Account
				a, e = h.Service.Initialize(ctx, q.SetupToken, q.Username, q.Password)
				result = accountResponse{Account: a}
				code = fasthttp.StatusCreated
			}
		case operationLogin:
			var q struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if e = Decode(c, &q, false); e == nil {
				var v identity.IssuedSession
				v, e = h.Service.Login(ctx, q.Username, q.Password, c.RemoteIP().String())
				if e == nil {
					var a identity.Account
					a, e = h.Service.Me(ctx, v.Principal)
					if e == nil {
						h.SetCookie(c, v.Token, v.ExpiresAt)
						result = loginResponse{Message: messageLoginSuccessful, Account: a, MustChangePassword: v.Principal.MustChangePassword, ExpiresAt: v.ExpiresAt}
					}
				}
			}
		case operationLogout:
			var q struct{}
			if e = Decode(c, &q, route.allowEmptyBody); e == nil {
				e = h.Service.Logout(ctx, string(c.Request.Header.Cookie(CookieName)))
				if e == nil {
					h.SetCookie(c, "", fasthttp.CookieExpireDelete)
					result = messageResponse{Message: messageLogoutSuccessful}
				}
			}
		case operationMe:
			var q struct{}
			if e = Decode(c, &q, false); e == nil {
				var a identity.Account
				a, e = h.Service.Me(ctx, p)
				result = accountResponse{Account: a}
			}
		case operationChangePassword:
			var q struct {
				Old string `json:"old_password"`
				New string `json:"new_password"`
			}
			if e = Decode(c, &q, false); e == nil {
				e = h.Service.ChangePassword(ctx, p, q.Old, q.New)
				if e == nil {
					h.SetCookie(c, "", fasthttp.CookieExpireDelete)
					result = messageResponse{Message: messagePasswordChanged}
				}
			}
		case operationCreate:
			var q struct {
				Username    string `json:"username"`
				DisplayName string `json:"display_name"`
			}
			if e = Decode(c, &q, false); e == nil {
				var a identity.Account
				a, e = h.Service.CreateAccount(ctx, p, q.Username, q.DisplayName)
				result = accountResponse{Account: a}
				code = fasthttp.StatusCreated
			}
		case operationList:
			var q struct {
				Cursor string `json:"cursor"`
				Limit  int    `json:"limit"`
			}
			if e = Decode(c, &q, false); e == nil {
				result, e = h.Service.ListAccounts(ctx, p, q.Cursor, q.Limit)
			}
		case operationSetStatus:
			var q struct {
				ID     string `json:"account_id"`
				Status string `json:"status"`
			}
			if e = Decode(c, &q, false); e == nil {
				e = h.Service.SetAccountStatus(ctx, p, q.ID, q.Status)
				if e == nil {
					var a identity.Account
					a, e = h.Service.Account(ctx, p, q.ID)
					result = accountResponse{Account: a}
				}
			}
		case operationResetPassword:
			var q struct {
				ID          string `json:"account_id"`
				OperationID string `json:"operation_id"`
			}
			if e = Decode(c, &q, false); e == nil {
				var v identity.PasswordEvent
				v, e = h.Service.ResetPassword(ctx, p, q.ID, q.OperationID)
				result = resetPasswordResponse{EventID: v.ID, Result: v.Result}
			}
		case operationPasswordEvents:
			var q struct {
				Target string `json:"target_id"`
				Cursor string `json:"cursor"`
				Limit  int    `json:"limit"`
			}
			if e = Decode(c, &q, false); e == nil {
				result, e = h.Service.ListPasswordEvents(ctx, p, q.Target, q.Cursor, q.Limit)
			}
		case operationWSTicket:
			var q struct{}
			if e = Decode(c, &q, route.allowEmptyBody); e == nil {
				var token string
				token, e = h.Service.IssueTicket(ctx, p)
				result = ticketResponse{Ticket: token, ExpiresIn: int(identity.WSTicketTTL / time.Second)}
			}
		default:
			e = identity.ErrInvalid
		}
		if e != nil {
			Error(c, e)
			return
		}
		JSON(c, code, result)
	}
}
