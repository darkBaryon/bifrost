// 本文件定义浏览器同源和会话 Cookie 规则，认证、账号修改与宿主入口共享同一个实例。
package authhttp

import (
	"net"
	"net/url"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/valyala/fasthttp"
)

// CookieName 是 EE 会话 Cookie，所有 HTTP 入口按同一名字读取。
const CookieName = "ee_session"

const (
	legacyCookieName   = "token" // 旧共享管理员 Cookie，只清除，绝不读取。
	deleteCookieMaxAge = -1      // fasthttp 用负数表示立即删除 Cookie。
)

// Browser 持有可信部署 origin，判断请求来源并签发会话 Cookie；不依赖请求中的代理头。
type Browser struct {
	origin string
	secure bool
}

// NewBrowser 绑定经过验证的部署 origin。
func NewBrowser(origin string) (*Browser, error) {
	o, secure, err := validateOrigin(origin)
	if err != nil {
		return nil, err
	}
	return &Browser{origin: o, secure: secure}, nil
}

// validateOrigin 拒绝路径、查询、片段和凭据；必须使用 HTTPS，只有回环地址允许 HTTP。
// 返回规范化的 origin 与 Cookie 是否应带 Secure。
func validateOrigin(raw string) (origin string, secure bool, err error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", false, auth.ErrInvalid
	}
	ip := net.ParseIP(u.Hostname())
	loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", false, auth.ErrInvalid
	}
	return u.Scheme + "://" + u.Host, u.Scheme == "https", nil
}

// Origin 返回已验证的部署 origin，供 WebSocket 握手等来源检查使用。
func (b *Browser) Origin() string { return b.origin }

// SameOrigin 要求 Origin 与部署 origin 精确一致；没有 Origin 时须有同源 Referer，两者冲突拒绝。
func (b *Browser) SameOrigin(c *fasthttp.RequestCtx) bool {
	if raw := string(c.Request.Header.Peek("Referer")); raw != "" {
		r, err := url.Parse(raw)
		if err != nil || r.User != nil || r.Scheme+"://"+r.Host != b.origin {
			return false
		}
	}
	if o := string(c.Request.Header.Peek("Origin")); o != "" {
		return o == b.origin
	}
	return len(c.Request.Header.Peek("Referer")) != 0
}

// SetCookie 写入当前会话 Cookie，并删除旧共享管理员 Cookie；空 token 表示退出登录。
func (b *Browser) SetCookie(c *fasthttp.RequestCtx, token string, expires time.Time) {
	cookie := fasthttp.AcquireCookie()
	defer fasthttp.ReleaseCookie(cookie)
	cookie.SetKey(CookieName)
	cookie.SetValue(token)
	cookie.SetPath("/")
	cookie.SetHTTPOnly(true)
	cookie.SetSecure(b.secure)
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

// ClearCookie 清除当前与旧版 Cookie，不修改服务端会话。
func (b *Browser) ClearCookie(c *fasthttp.RequestCtx) {
	b.SetCookie(c, "", fasthttp.CookieExpireDelete)
}
