// 本文件是 HTTP 协议管道：部署 origin 校验、同源检查、Cookie、严格 JSON 解码与错误映射；端点按线见 session.go、accounts.go、passwords.go。
package identityhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/valyala/fasthttp"
)

// CookieName 是 EE 会话 Cookie；宿主适配按同一名字读取。
const CookieName = "ee_session"

const (
	legacyCookieName   = "token" // 旧共享管理员 Cookie，只清除，绝不读取
	deleteCookieMaxAge = -1      // fasthttp 用负数表示立即删除 Cookie
	// MaxBodyBytes 是身份和角色接口的正文上限；宿主配置接口另有自己的限制。
	MaxBodyBytes = 16 << 10
	maxJSONDepth = 64 // 对象与数组的嵌套深度上限
)

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
	if len(body) > MaxBodyBytes || !utf8.Valid(body) {
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
