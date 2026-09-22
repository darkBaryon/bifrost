// 本文件验证宿主复用的严格对象解码不会继承旧会话接口的空正文兼容。
package identityhttp

import (
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/valyala/fasthttp"
)

func TestDecodeJSONObjectContract(t *testing.T) {
	for _, tt := range []struct {
		name, body, media string
		valid             bool
	}{
		{"object", `{"value":"ok"}`, "application/json", true},
		{"media parameters", `{}`, "application/json; charset=utf-8", true},
		{"empty", "", "application/json", false},
		{"missing media", `{}`, "", false},
		{"wrong media", `{}`, "text/plain", false},
		{"unknown", `{"other":1}`, "application/json", false},
		{"duplicate", `{"value":"one","value":"two"}`, "application/json", false},
		{"non object", `[]`, "application/json", false},
		{"null", `null`, "application/json", false},
		{"multiple", `{} {}`, "application/json", false},
		{"invalid utf8", "{\"value\":\"\xff\"}", "application/json", false},
		{"limit", `{}` + strings.Repeat(" ", MaxBodyBytes-2), "application/json", true},
		{"over limit", `{}` + strings.Repeat(" ", MaxBodyBytes-1), "application/json", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c fasthttp.RequestCtx
			c.Request.SetBodyString(tt.body)
			c.Request.Header.SetContentType(tt.media)
			var payload struct {
				Value string `json:"value"`
			}
			err := DecodeJSONObject(&c, &payload)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
			if err != nil && err != identity.ErrInvalid {
				t.Fatal("unexpected error", err)
			}
		})
	}
}
