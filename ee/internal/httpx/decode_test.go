// 本文件验证严格对象解码与旧会话接口的空正文兼容边界。
package httpx

import (
	"strings"
	"testing"

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
		{"limit", `{}` + strings.Repeat(" ", 16384-2), "application/json", true},
		{"over limit", `{}` + strings.Repeat(" ", 16385-2), "application/json", false},
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
			if err != nil && err != ErrInvalid {
				t.Fatal("unexpected error", err)
			}
		})
	}
}

func TestDecodeOptionalJSONObjectContract(t *testing.T) {
	for _, tt := range []struct {
		name, body, media string
		valid             bool
	}{
		{"legacy empty without media", "", "", true},
		{"legacy empty with media", "", "application/json", true},
		{"legacy empty with non json media", "", "text/plain", true},
		{"nonempty object", `{}`, "application/json", true},
		{"nonempty missing media", `{}`, "", false},
		{"whitespace is not empty", " ", "", false},
		{"duplicate", `{"value":"one","value":"two"}`, "application/json", false},
		{"unknown", `{"other":1}`, "application/json", false},
		{"invalid utf8", "{\"value\":\"\xff\"}", "application/json", false},
		{"over limit", `{}` + strings.Repeat(" ", 16385-2), "application/json", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c fasthttp.RequestCtx
			c.Request.SetBodyString(tt.body)
			c.Request.Header.SetContentType(tt.media)
			var payload struct {
				Value string `json:"value"`
			}
			if err := DecodeOptionalJSONObject(&c, &payload); (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
}

func TestValidateJSONObjectStructure(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		valid      bool
	}{
		{"nested objects", `{"outer":{"value":true}}`, true},
		{"nested array", `{"items":[1,{"value":null},false]}`, true},
		{"nested duplicate", `{"outer":{"value":1,"value":2}}`, false},
		{"escaped duplicate", `{"value":1,"\u0076alue":2}`, false},
		{"same field in separate objects", `{"items":[{"value":1},{"value":2}]}`, true},
		{"large number", `{"value":123456789012345678901234567890}`, true},
		{"unfinished object", `{"value":1`, false},
		{"trailing value", `{} 1`, false},
		{"root array", `[]`, false},
		{"root scalar", `"value"`, false},
		{"depth boundary", `{"value":` + strings.Repeat("[", 63) + "0" + strings.Repeat("]", 63) + "}", true},
		{"depth exceeded", `{"value":` + strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64) + "}", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateJSONObject([]byte(tt.body)); (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
}
