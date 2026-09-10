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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

const CookieName = "ee_session"

// Handler 保存已验证的外部origin，拒绝信任请求中的代理头。
type Handler struct {
	Service *identity.Service
	Origin  string
	secure  bool
}

// OperationContext correlates safe database diagnostics with the existing access log request_id.
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
		cookie.SetMaxAge(-1)
	}
	c.Response.Header.SetCookie(cookie)
	// 清理旧共享管理员Cookie，绝不读取它作为EE凭据。
	cookie.SetKey("token")
	cookie.SetValue("")
	cookie.SetMaxAge(-1)
	cookie.SetExpire(time.Unix(1, 0))
	c.Response.Header.SetCookie(cookie)
}
func JSON(c *fasthttp.RequestCtx, code int, value any) {
	b, e := json.Marshal(value)
	if e != nil {
		code = 503
		b = []byte(`{"error":{"code":"unavailable"}}`)
	}
	c.SetStatusCode(code)
	c.SetContentType("application/json")
	c.Response.SetBody(b)
}
func Error(c *fasthttp.RequestCtx, e error) {
	e = identity.SafeError(e)
	code := 503
	switch e {
	case identity.ErrUnauthorized:
		code = 401
	case identity.ErrForbidden:
		code = 403
	case identity.ErrInvalid:
		code = 400
	case identity.ErrConflict:
		code = 409
	case identity.ErrNotFound:
		code = 404
	case identity.ErrLimited:
		code = 429
	}
	if code == 429 {
		c.Response.Header.Set("Retry-After", "60")
	}
	JSON(c, code, map[string]any{"error": map[string]string{"code": e.Error(), "message": e.Error()}})
}

// ValidateJSONObject 拒绝重复键和多个JSON值，避免预检与宿主解析器歧义。
func ValidateJSONObject(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
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
func Decode(c *fasthttp.RequestCtx, v any, allowEmpty bool) error {
	if len(c.PostBody()) > 16<<10 || !utf8.Valid(c.PostBody()) {
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

var actions = map[string]string{
	"/api/identity/status": "status", "/api/identity/initialize": "initialize", "/api/identity/login": "login", "/api/identity/logout": "logout", "/api/identity/me": "me", "/api/identity/change-password": "change-password", "/api/accounts/create": "create", "/api/accounts/list": "list", "/api/accounts/set-status": "set-status", "/api/accounts/reset-password": "reset-password", "/api/identity/password-events": "password-events", "/api/identity/ws-ticket": "ws-ticket",
	"/api/session/login": "login", "/api/session/logout": "logout", "/api/session/ws-ticket": "ws-ticket",
}

func OwnsRoute(method, path string) bool {
	if method == "GET" && path == "/api/session/is-auth-enabled" {
		return true
	}
	_, ok := actions[path]
	return method == "POST" && ok
}
func (h *Handler) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	for path, action := range actions {
		r.POST(path, lib.ChainMiddlewares(h.endpoint(action, strings.HasPrefix(path, "/api/session/")), m...))
	}
	r.GET("/api/session/is-auth-enabled", lib.ChainMiddlewares(h.status, m...))
}
func (h *Handler) status(c *fasthttp.RequestCtx) {
	ctx := OperationContext(c, "identity.status")
	c.Response.Header.Set("Cache-Control", "no-store")
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
	JSON(c, 200, map[string]any{"initialized": state.Initialized, "auth_type": "password", "is_auth_enabled": true, "has_valid_token": e == nil, "has_valid_session": e == nil, "must_change_password": e == nil && p.MustChangePassword})
}
func (h *Handler) endpoint(action string, legacy bool) fasthttp.RequestHandler {
	return func(c *fasthttp.RequestCtx) {
		ctx := OperationContext(c, "identity."+action)
		c.Response.Header.Set("Cache-Control", "no-store")
		if !h.SameOrigin(c) {
			Error(c, identity.ErrForbidden)
			return
		}
		var p identity.Principal
		var e error
		if action != "status" && action != "login" && action != "initialize" && action != "logout" {
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
		code := 200
		switch action {
		case "status":
			var q struct{}
			if e = Decode(c, &q, false); e == nil {
				h.status(c)
				return
			}
		case "initialize":
			var q struct {
				SetupToken string `json:"setup_token"`
				Username   string `json:"username"`
				Password   string `json:"password"`
			}
			if e = Decode(c, &q, false); e == nil {
				var a identity.Account
				a, e = h.Service.Initialize(ctx, q.SetupToken, q.Username, q.Password)
				result = map[string]any{"account": a}
				code = 201
			}
		case "login":
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
						result = map[string]any{"message": "Login successful", "account": a, "must_change_password": v.Principal.MustChangePassword, "expires_at": v.ExpiresAt}
					}
				}
			}
		case "logout":
			var q struct{}
			if e = Decode(c, &q, legacy); e == nil {
				e = h.Service.Logout(ctx, string(c.Request.Header.Cookie(CookieName)))
				if e == nil {
					h.SetCookie(c, "", time.Unix(1, 0))
					result = map[string]string{"message": "Logout successful"}
				}
			}
		case "me":
			var q struct{}
			if e = Decode(c, &q, false); e == nil {
				var a identity.Account
				a, e = h.Service.Me(ctx, p)
				result = map[string]any{"account": a}
			}
		case "change-password":
			var q struct {
				Old string `json:"old_password"`
				New string `json:"new_password"`
			}
			if e = Decode(c, &q, false); e == nil {
				e = h.Service.ChangePassword(ctx, p, q.Old, q.New)
				if e == nil {
					h.SetCookie(c, "", time.Unix(1, 0))
					result = map[string]string{"message": "Password changed; log in again"}
				}
			}
		case "create":
			var q struct {
				Username    string `json:"username"`
				DisplayName string `json:"display_name"`
			}
			if e = Decode(c, &q, false); e == nil {
				var a identity.Account
				a, e = h.Service.CreateAccount(ctx, p, q.Username, q.DisplayName)
				result = map[string]any{"account": a}
				code = 201
			}
		case "list":
			var q struct {
				Cursor string `json:"cursor"`
				Limit  int    `json:"limit"`
			}
			if e = Decode(c, &q, false); e == nil {
				result, e = h.Service.ListAccounts(ctx, p, q.Cursor, q.Limit)
			}
		case "set-status":
			var q struct {
				ID     string `json:"account_id"`
				Status string `json:"status"`
			}
			if e = Decode(c, &q, false); e == nil {
				e = h.Service.SetAccountStatus(ctx, p, q.ID, q.Status)
				if e == nil {
					var a identity.Account
					a, e = h.Service.Account(ctx, p, q.ID)
					result = map[string]any{"account": a}
				}
			}
		case "reset-password":
			var q struct {
				ID          string `json:"account_id"`
				OperationID string `json:"operation_id"`
			}
			if e = Decode(c, &q, false); e == nil {
				var v identity.PasswordEvent
				v, e = h.Service.ResetPassword(ctx, p, q.ID, q.OperationID)
				result = map[string]string{"event_id": v.ID, "result": v.Result}
			}
		case "password-events":
			var q struct {
				Target string `json:"target_id"`
				Cursor string `json:"cursor"`
				Limit  int    `json:"limit"`
			}
			if e = Decode(c, &q, false); e == nil {
				result, e = h.Service.ListPasswordEvents(ctx, p, q.Target, q.Cursor, q.Limit)
			}
		case "ws-ticket":
			var q struct{}
			if e = Decode(c, &q, legacy); e == nil {
				var token string
				token, e = h.Service.IssueTicket(ctx, p)
				result = map[string]any{"ticket": token, "expires_in": int(identity.WSTicketTTL / time.Second)}
			}
		}
		if e != nil {
			Error(c, e)
			return
		}
		JSON(c, code, result)
	}
}
