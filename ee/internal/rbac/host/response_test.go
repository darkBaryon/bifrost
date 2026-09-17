// 本文件用特征秘密检查所有敏感响应入口及未知字段默认隐藏。
package host

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/tidwall/gjson"
)

func TestSensitiveProjectionFixtures(t *testing.T) {
	tests := []struct {
		name       string
		kind       projection
		path, body string
	}{
		{"provider", projectionProviderSafe, "/api/providers", `{"providers":[{"name":"test","network_config":{"base_url":"https://example.invalid/?key=LEAK","extra_headers":{"Authorization":"LEAK"}},"provider_status":"error","description":"LEAK"}]}`},
		{"keys", projectionKeyMetadata, "/api/keys", `[{"key_id":"uuid","provider":"test","value":"LEAK","aws_config":{"secret":"LEAK"}}]`},
		{"vk", projectionVkValues, "/api/governance/virtual-keys", `{"virtual_keys":[{"id":"vk","value":"LEAK"}]}`},
		{"governance", projectionGovernanceNestedVk, "/api/governance/customers", `{"customers":[{"id":"c","virtual_keys":[{"id":"vk","value":"LEAK"}]}]}`},
		{"logs", projectionLogsContent, "/api/logs", `{"logs":[{"id":"log","model":"model","content_summary":"LEAK","metadata":{"value":"LEAK"},"future_payload":"LEAK","error":"LEAK","raw_request":"LEAK"}],"pagination":{"limit":20}}`},
		{"log", projectionLogsContent, "/api/logs/{id}", `{"id":"log","input":"LEAK","plugin_logs":"LEAK","redaction_mapping":"LEAK"}`},
		{"mcp-log", projectionLogsContent, "/api/mcp-logs/{id}", `{"id":"log","tool_name":"tool","arguments":"LEAK","result":"LEAK","user_agent":"LEAK"}`},
		{"mcp", projectionMcpSafe, "/api/mcp/clients", `{"clients":[{"id":"client","stdio_config":{"command":"LEAK","args":["LEAK"],"envs":{"KEY":"LEAK"}},"last_failure":{"message":"LEAK","stage":"connect"},"node_states":{"n":{"last_failure":{"message":"LEAK","stage":"connect"}}}}]}`},
		{"plugin", projectionPluginSafe, "/api/plugins", `{"plugins":[{"name":"custom","config":{"secret":"LEAK"},"status":{"logs":["LEAK"]}}]}`},
		{"settings", projectionSettingsSafe, "/api/config", `{"framework_config":{"pricing_url":"https://example.invalid/?key=LEAK"},"restart_required":{"proxy_config":{"url":"https://user:LEAK@example.invalid"}}}`},
		{"webhook", projectionWebhookSafe, "/api/webhooks/deliveries", `{"deliveries":[{"id":"delivery","http_status":200,"payload":"LEAK","response_body":"LEAK","last_error":"LEAK"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := requestCtx("GET", tt.path, "")
			c.Response.Header.SetContentType("application/json")
			c.Response.SetBodyString(tt.body)
			e := projectSensitiveResponse(c, requestAccess{Route: routeEntry{Pattern: tt.path, Projection: tt.kind}})
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(c.Response.Body()), "LEAK") {
				t.Fatal("projection leaked fixture secret")
			}
			if !json.Valid(c.Response.Body()) {
				t.Fatal("invalid projected JSON")
			}
		})
	}
}

func TestGovernanceProjectionPreservesExplicitReveal(t *testing.T) {
	value := object{"virtual_keys": []any{object{"value": "preserved"}}}
	projectGovernance(value, rbac.Access{Permissions: []rbac.Permission{rbac.VirtualKeysRevealKey}})
	body, _ := json.Marshal(value)
	if !strings.Contains(string(body), "preserved") {
		t.Fatal("reveal lost existing value")
	}
}

func TestInvalidProjectionAndSafeErrors(t *testing.T) {
	for _, body := range []string{`{"total_requests":"LEAK"}`, `[]`} {
		c := requestCtx("GET", "/api/logs/stats", "")
		c.Response.Header.SetContentType("application/json")
		c.Response.SetBodyString(body)
		if e := projectSensitiveResponse(c, requestAccess{Route: routeEntry{Pattern: "/api/logs/stats", Projection: projectionLogsContent}}); e != rbac.ErrUnavailable {
			t.Fatalf("invalid aggregation accepted: %s: %v", body, e)
		}
	}
	c := requestCtx("GET", "/api/providers", "")
	c.SetStatusCode(500)
	c.Response.Header.Set("X-Request-ID", "trace-id")
	c.Response.Header.Set("ETag", "LEAK")
	c.Response.SetBodyString(`{"error":"driver LEAK"}`)
	if e := projectSensitiveResponse(c, requestAccess{Route: routeEntry{Projection: projectionProviderSafe}}); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(c.Response.Body()), "LEAK") || len(c.Response.Header.Peek("ETag")) != 0 || string(c.Response.Header.Peek("X-Request-ID")) != "trace-id" || c.Response.StatusCode() != 500 {
		t.Fatal("unsafe error envelope")
	}
}

func TestRejectMalformedSensitiveContainers(t *testing.T) {
	for _, kind := range []projection{projectionProviderSafe, projectionLogsContent, projectionMcpSafe, projectionPluginSafe, projectionWebhookSafe} {
		c := requestCtx("GET", "/api/test", "")
		c.Response.Header.SetContentType("application/json")
		c.Response.SetBodyString(`{"providers":"LEAK","logs":["LEAK"],"clients":"LEAK","plugins":"LEAK","deliveries":"LEAK"}`)
		if e := projectSensitiveResponse(c, requestAccess{Route: routeEntry{Projection: kind}}); e != rbac.ErrUnavailable {
			t.Fatal("accepted malformed sensitive container", kind, e)
		}
	}
}

func projected(t *testing.T, path string, kind projection, body string, codes ...rbac.Permission) string {
	t.Helper()
	c := requestCtx("GET", path, "")
	c.Response.Header.SetContentType("application/json")
	c.Response.SetBodyString(body)
	if err := projectSensitiveResponse(c, requestAccess{Route: routeEntry{Pattern: path, Projection: kind}, Access: rbac.Access{Permissions: codes}}); err != nil {
		t.Fatal(err)
	}
	return string(c.Response.Body())
}

func assertProjectionFields(t *testing.T, body string, want map[string]string) {
	t.Helper()
	for path, value := range want {
		if actual := gjson.Get(body, path); !actual.Exists() || actual.String() != value {
			t.Fatalf("field %s=%s want=%s body=%s", path, actual.String(), value, body)
		}
	}
}

func TestRoutingStructuresPreserveConfiguration(t *testing.T) {
	tests := []struct {
		path, body string
		want       map[string]string
	}{
		{"/api/routing/rules", `{"rules":[{"id":"rule","name":"route","cel_expression":"model == 'test'","query":{"conditions":["configured"]},"targets":[{"provider":"openai","model":"model","weight":1}],"priority":3}],"count":1,"total_count":4,"offset":0,"limit":20}`, map[string]string{"rules.0.cel_expression": "model == 'test'", "rules.0.targets.0.weight": "1", "rules.0.query.conditions.0": "configured", "total_count": "4", "limit": "20"}},
		{"/api/routing/rules/{rule_id}", `{"rule":{"id":"rule","description":"configured description","cel_expression":"true","targets":[{"provider":"openai","model":"model","weight":1}],"created_at":"2026-09-13T00:00:00Z"}}`, map[string]string{"rule.description": "configured description", "rule.targets.0.model": "model", "rule.created_at": "2026-09-13T00:00:00Z"}},
		{"/api/routing/complexity-analyzer-config", `{"llm":{"provider":"openai","model":"classifier","prompt":"Configured guidance"},"semantic":{"provider":"openai","embedding_model":"embedding","min_similarity":0.8}}`, map[string]string{"llm.prompt": "Configured guidance", "semantic.min_similarity": "0.8"}},
		{"/api/routing/complexity-analyzer-status", `{"state":"ready","loaded":7,"total":8,"cached_phrases":5,"namespace":"generation","llm":{"state":"ready"},"llm_default_prompt":"Default guidance","error":"DIAGNOSTIC"}`, map[string]string{"loaded": "7", "total": "8", "cached_phrases": "5", "llm.state": "ready", "llm_default_prompt": "Default guidance", "namespace": "generation"}},
		{"/api/routing/complexity-analyzer-generations", `{"generations":[{"namespace":"generation","active":true}]}`, map[string]string{"generations.0.namespace": "generation", "generations.0.active": "true"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			for _, codes := range [][]rbac.Permission{nil, {rbac.LogsRevealContent}} {
				body := projected(t, test.path, projectionRoutingSafe, test.body, codes...)
				assertProjectionFields(t, body, test.want)
				if strings.Contains(test.body, "DIAGNOSTIC") && strings.Contains(body, "DIAGNOSTIC") != (len(codes) > 0) {
					t.Fatal("routing diagnostic permission branch")
				}
			}
		})
	}
}

func TestPluginMarkerReplacement(t *testing.T) {
	current := object{"headers": object{"Authorization": "credential", "X-Delete": "old"}, "api_key": "env.MAXIM_KEY"}
	next := object{"headers": object{"Authorization": redacted, "X-New": "new"}, "api_key": object{"value": redacted}}
	value, e := restoreMarkers(next, current)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(value)
	if strings.Contains(string(raw), "X-Delete") || !strings.Contains(string(raw), "credential") || !strings.Contains(string(raw), "env.MAXIM_KEY") {
		t.Fatalf("wrong replacement: %s", raw)
	}
	if _, ok := current["headers"].(object)["X-New"]; ok {
		t.Fatal("mutated stored config")
	}
	if _, e := restoreMarkers(object{"missing": redacted}, current); e != rbac.ErrInvalid {
		t.Fatal("accepted orphan marker", e)
	}
}

func TestBuiltinPluginNameResponses(t *testing.T) {
	for _, path := range []string{"/api/plugins/builtins", "/api/plugins/loaded"} {
		c := requestCtx("GET", path, "")
		c.Response.Header.SetContentType("application/json")
		c.Response.SetBodyString(`{"plugins":["telemetry","logging"]}`)
		if e := projectSensitiveResponse(c, requestAccess{Route: routeEntry{Pattern: path, Projection: projectionPluginSafe}}); e != nil {
			t.Fatalf("plugin name list rejected: %s: %v", path, e)
		}
	}
}

func TestWebhookSyntheticFailureDoesNotRevealURL(t *testing.T) {
	for _, codes := range [][]rbac.Permission{nil, rbac.Permissions()} {
		c := requestCtx("POST", "/api/webhooks/example/test", `{}`)
		c.Response.Header.SetContentType("application/json")
		c.Response.SetBodyString(`{"delivered":false,"error":"Post https://receiver.invalid/?api_key=LEAK: dial failed"}`)
		if e := projectSensitiveResponse(c, requestAccess{Route: routeEntry{Pattern: "/api/webhooks/{id}/test", Projection: projectionWebhookSafe}, Access: rbac.Access{Permissions: codes}}); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(c.Response.Body()), "LEAK") {
			t.Fatal("synthetic test exposed URL credential")
		}
	}
}

// TestProxySettingsProjection 覆盖宿主接受的代理地址形式，认证信息不出现在设置返回中。
func TestProxySettingsProjection(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		hidden    bool
	}{
		{"authority", "proxy.invalid:8080", false},
		{"http", "http://proxy.invalid:8080", false},
		{"authentication without scheme", "fixture:synthetic@proxy.invalid:8080", true},
		{"authentication", "https://fixture:synthetic@proxy.invalid:8080", true},
		{"invalid authentication", "http://fixture:%zz@proxy.invalid", true},
		{"query", "proxy.invalid:8080?token=synthetic", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"proxy_config", "proxy"} {
				row := object{"url": tc.raw}
				projectSettings(object{"restart_required": object{key: row}})
				if (row["url"] == redacted) != tc.hidden {
					t.Fatal("unexpected proxy visibility")
				}
				if !tc.hidden && row["url"] != tc.raw {
					t.Fatal("ordinary proxy address changed")
				}
			}
		})
	}
}
