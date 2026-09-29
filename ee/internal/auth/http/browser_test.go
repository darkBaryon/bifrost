// 本文件验证部署来源、同源请求和新旧 Cookie 协议。
package authhttp

import (
	"bytes"
	"testing"
	"time"

	"github.com/valyala/fasthttp"
)

func TestDeploymentOrigin(t *testing.T) {
	for _, tc := range []struct {
		raw, want     string
		secure, valid bool
	}{
		{"https://console.example/", "https://console.example", true, true},
		{"http://localhost:8080", "http://localhost:8080", false, true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", false, true},
		{"http://[::1]:8080", "http://[::1]:8080", false, true},
		{"http://console.example", "", false, false},
		{"https://name:password@console.example", "", false, false},
		{"https://console.example/path", "", false, false},
		{"https://console.example?secret=value", "", false, false},
		{"https://console.example#fragment", "", false, false},
		{"//console.example", "", false, false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			origin, secure, err := validateOrigin(tc.raw)
			if (err == nil) != tc.valid || origin != tc.want || secure != tc.secure {
				t.Fatalf("origin=%q secure=%v err=%v", origin, secure, err)
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	browser, err := NewBrowser("https://console.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, origin, referer string
		valid                 bool
	}{
		{"origin", "https://console.example", "", true},
		{"referer fallback", "", "https://console.example/account", true},
		{"both", "https://console.example", "https://console.example/account", true},
		{"missing", "", "", false},
		{"cross origin", "https://evil.example", "", false},
		{"conflicting referer", "https://console.example", "https://evil.example/account", false},
		{"conflicting origin", "https://evil.example", "https://console.example/account", false},
		{"null origin", "null", "https://console.example/account", false},
		{"credential referer", "https://console.example", "https://user@console.example/account", false},
		{"different port", "https://console.example:443", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c fasthttp.RequestCtx
			if tc.origin != "" {
				c.Request.Header.Set("Origin", tc.origin)
			}
			if tc.referer != "" {
				c.Request.Header.Set("Referer", tc.referer)
			}
			c.Request.Header.Set("X-Forwarded-Host", "console.example")
			if got := browser.SameOrigin(&c); got != tc.valid {
				t.Fatalf("same origin=%v", got)
			}
		})
	}
}

func TestSessionCookie(t *testing.T) {
	for _, tc := range []struct {
		origin string
		secure bool
	}{
		{"https://console.example", true}, {"http://localhost:8080", false},
	} {
		t.Run(tc.origin, func(t *testing.T) {
			browser, err := NewBrowser(tc.origin)
			if err != nil {
				t.Fatal(err)
			}
			var c fasthttp.RequestCtx
			expires := time.Now().Add(time.Hour).Truncate(time.Second)
			browser.SetCookie(&c, "private-token", expires)
			var cookie fasthttp.Cookie
			cookie.SetKey("ee_session")
			if !c.Response.Header.Cookie(&cookie) {
				t.Fatal("missing session cookie")
			}
			if string(cookie.Value()) != "private-token" || string(cookie.Path()) != "/" || !cookie.HTTPOnly() || cookie.Secure() != tc.secure || cookie.SameSite() != fasthttp.CookieSameSiteLaxMode || !cookie.Expire().Equal(expires) {
				t.Fatalf("session cookie contract changed: %s", cookie.String())
			}
			cookie.Reset()
			cookie.SetKey("token")
			if !c.Response.Header.Cookie(&cookie) || string(cookie.Value()) != "" || !bytes.Contains(c.Response.Header.PeekCookie("token"), []byte("max-age=0")) {
				t.Fatal("legacy cookie was not removed")
			}
			browser.ClearCookie(&c)
			cookie.Reset()
			cookie.SetKey("ee_session")
			if !c.Response.Header.Cookie(&cookie) || string(cookie.Value()) != "" || !bytes.Contains(c.Response.Header.PeekCookie("ee_session"), []byte("max-age=0")) {
				t.Fatal("session cookie was not removed")
			}
		})
	}
}
