// 本文件验证敏感预检的拒绝点与宿主字段清单，不执行外部Provider请求。
package host

import (
	"context"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/valyala/fasthttp"
)

func requestCtx(method, path, body string) *fasthttp.RequestCtx {
	c := &fasthttp.RequestCtx{}
	c.Init(&fasthttp.Request{}, nil, nil)
	c.Request.Header.SetMethod(method)
	c.Request.SetRequestURI(path)
	c.Request.Header.SetContentType("application/json")
	c.Request.SetBodyString(body)
	return c
}

func TestProviderMarkerRoundtrip(t *testing.T) {
	old := &configstore.ProviderConfig{NetworkConfig: &schemas.NetworkConfig{BaseURL: "https://example.invalid/v1?key=secret", ExtraHeaders: map[string]string{"Authorization": "secret", "X-Delete": "delete"}}}
	next := &schemas.NetworkConfig{BaseURL: redacted, ExtraHeaders: map[string]string{"Authorization": redacted, "X-New": "new"}}
	if e := restoreProvider(context.Background(), old, next, nil); e != nil {
		t.Fatal(e)
	}
	if next.BaseURL != old.NetworkConfig.BaseURL || next.ExtraHeaders["Authorization"] != "secret" || len(next.ExtraHeaders) != 2 {
		t.Fatal("lost complete replacement semantics")
	}
	if _, ok := old.NetworkConfig.ExtraHeaders["X-New"]; ok {
		t.Fatal("mutated current cache")
	}
	empty := &schemas.NetworkConfig{ExtraHeaders: map[string]string{}}
	if e := restoreProvider(context.Background(), old, empty, nil); e != nil || len(empty.ExtraHeaders) != 0 {
		t.Fatal(e)
	}
	if e := restoreProvider(context.Background(), old, &schemas.NetworkConfig{ExtraHeaders: map[string]string{"missing": redacted}}, nil); e == nil {
		t.Fatal("accepted orphan marker")
	}
}

func TestStrictSensitiveAliases(t *testing.T) {
	for _, raw := range []string{`{"Include_Response":true}`, `{"include_response":false,"include_response":true}`, `{"network_config":{"Base_URL":"x"}}`, `{"config":{},"Config":{"path":"x"}}`} {
		if strictJSON([]byte(raw)) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestSensitiveGuardPreservesDynamicConfigKeys(t *testing.T) {
	if e := strictJSON([]byte(`{"network_config":{"extra_headers":{"URL":"value","Name":"value"}},"metadata":{"key":"value","Key":"value"}}`)); e != nil {
		t.Fatal("dynamic config interpreted as struct aliases", e)
	}
}

func TestVirtualKeyExportRequiresReveal(t *testing.T) {
	for _, tt := range []struct {
		query string
		code  rbac.Permission
		want  error
	}{
		{"?export=true", rbac.VirtualKeysView, rbac.ErrForbidden},
		{"?export=true", rbac.VirtualKeysRevealKey, nil},
		{"?export=false", rbac.VirtualKeysView, nil},
		{"?export=true&export=false", rbac.VirtualKeysRevealKey, rbac.ErrInvalid},
		{"?export=yes", rbac.VirtualKeysRevealKey, rbac.ErrInvalid},
	} {
		c := requestCtx("GET", "/api/governance/virtual-keys"+tt.query, "")
		r := requestAccess{Route: routeEntry{Guard: guardVkExport}, Access: rbac.Access{Permissions: []rbac.Permission{tt.code}}}
		if err := (&Adapter{}).checkSensitiveRequest(c, r); err != tt.want {
			t.Errorf("%s got %v want %v", tt.query, err, tt.want)
		}
	}
}
