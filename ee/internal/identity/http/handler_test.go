// 本文件验证HTTP边界与对外契约，不从生产路由表生成预期值。
package identityhttp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/valyala/fasthttp"
)

func TestErrorResponseContract(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
		code   string
	}{
		{identity.ErrInvalid, 400, "invalid_input"},
		{identity.ErrUnauthorized, 401, "unauthorized"},
		{identity.ErrForbidden, 403, "forbidden"},
		{identity.ErrNotFound, 404, "not_found"},
		{identity.ErrConflict, 409, "conflict"},
		{identity.ErrLimited, 429, "rate_limited"},
		{errors.New("private database details"), 503, "unavailable"},
	} {
		t.Run(tt.code, func(t *testing.T) {
			var c fasthttp.RequestCtx
			Error(&c, tt.err)
			var body struct {
				Error struct{ Code, Message string }
			}
			if err := json.Unmarshal(c.Response.Body(), &body); err != nil {
				t.Fatal(err)
			}
			if c.Response.StatusCode() != tt.status || body.Error.Code != tt.code || body.Error.Message != tt.code {
				t.Fatal("error contract changed", c.Response.StatusCode(), string(c.Response.Body()))
			}
			retry := string(c.Response.Header.Peek("Retry-After"))
			if tt.status == 429 && retry != "60" {
				t.Fatal("retry window contract changed", retry)
			}
			if tt.status != 429 && retry != "" {
				t.Fatal("unexpected retry header")
			}
		})
	}
}

func TestJSONFailureDoesNotExposeValue(t *testing.T) {
	var c fasthttp.RequestCtx
	JSON(&c, fasthttp.StatusOK, func() {})
	if c.Response.StatusCode() != 503 || string(c.Response.Body()) != `{"error":{"code":"unavailable"}}` {
		t.Fatal("unsafe serialization fallback", string(c.Response.Body()))
	}
}

func TestStrictJSONBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name, body        string
		allowEmpty, valid bool
	}{
		{"body at 16KiB", `{"value":"ok"}` + strings.Repeat(" ", 16384-len(`{"value":"ok"}`)), false, true},
		{"body over 16KiB", `{"value":"ok"}` + strings.Repeat(" ", 16385-len(`{"value":"ok"}`)), false, false},
		{"legacy empty body", "", true, true},
		{"new empty body", "", false, false},
		{"duplicate key", `{"value":"ok","value":"other"}`, false, false},
		{"unknown field", `{"unknown":true}`, false, false},
		{"multiple documents", `{} {}`, false, false},
		{"invalid utf8", "{\"value\":\"\xff\"}", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c fasthttp.RequestCtx
			c.Request.SetBodyString(tt.body)
			if tt.body != "" {
				c.Request.Header.SetContentType("application/json")
			}
			var payload struct {
				Value string `json:"value"`
			}
			if err := decode(&c, &payload, tt.allowEmpty); (err == nil) != tt.valid {
				t.Fatal("unexpected decode result", err)
			}
		})
	}
	for _, tt := range []struct {
		arrays int
		valid  bool
	}{{63, true}, {64, false}} {
		body := `{"value":` + strings.Repeat("[", tt.arrays) + "0" + strings.Repeat("]", tt.arrays) + "}"
		if err := ValidateJSONObject([]byte(body)); (err == nil) != tt.valid {
			t.Fatal("JSON depth boundary changed", tt.arrays, err)
		}
	}
}
