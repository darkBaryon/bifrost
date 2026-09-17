// 本文件按内置插件真实配置形状核对安全字段、凭据及畸形类型回退。
package host

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/tidwall/gjson"
)

func TestBuiltinPluginConfigStructures(t *testing.T) {
	tests := []struct {
		name, config, bad string
		want              map[string]string
	}{
		{"telemetry", `{"custom_labels":["team"],"push_gateway":{"enabled":true,"job_name":"gateway","push_interval":17,"push_gateway_url":"https://host/?key=SECRET","basic_auth":{"username":"SECRET","password":"SECRET"}}}`, `{"custom_labels":3}`, map[string]string{"custom_labels.0": "team", "push_gateway.job_name": "gateway", "push_gateway.push_interval": "17", "push_gateway.basic_auth.password.value": "<REDACTED>"}},
		{"prompts", `{}`, `{"anything":"ignored"}`, map[string]string{}},
		{"logging", `{"disable_content_logging":true,"retain_content_in_object_storage":false,"logging_headers":["x-request-*"],"writer":{"max_batch_size":17,"batch_interval":"1s"}}`, `{"writer":3}`, map[string]string{"disable_content_logging": "true", "writer.max_batch_size": "17", "logging_headers.0": "x-request-*"}},
		{"governance", `{"is_vk_mandatory":true,"required_headers":["x-bf-vk"],"is_enterprise":false,"disable_auto_tool_inject":true}`, `{"is_enterprise":"bad"}`, map[string]string{"is_vk_mandatory": "true", "required_headers.0": "x-bf-vk", "disable_auto_tool_inject": "true"}},
		{"otel", `{"profiles":[{"service_name":"trace-a","collector_url":"https://host/?key=SECRET","headers":{"Authorization":"SECRET"},"trace_headers":{"X-Trace":"SECRET"},"metrics_headers":{"X-Metric":"SECRET"},"disable_content_logging":true},{"service_name":"trace-b","enabled":false}]}`, `{"profiles":3}`, map[string]string{"profiles.0.service_name": "trace-a", "profiles.1.service_name": "trace-b", "profiles.0.headers.Authorization": "<redacted>", "profiles.0.trace_headers.X-Trace": "<redacted>", "profiles.0.disable_content_logging": "true"}},
		{"semantic_cache", `{"provider":"openai","embedding_model":"embedding","dimension":64,"ttl":"1m","threshold":0.8}`, `{"dimension":"bad"}`, map[string]string{"provider": "openai", "embedding_model": "embedding", "dimension": "64", "threshold": "0.8", "ttl": "1m"}},
		{"compat", `{"should_drop_params":false}`, `{"should_drop_params":"bad"}`, map[string]string{"should_drop_params": "false", "convert_text_to_chat": "true", "azure_deepseek": "true"}},
		{"maxim", `{"api_key":"SECRET","log_repo_id":"repository","request_headers":["x-request-*"]}`, `{"api_key":3}`, map[string]string{"log_repo_id": "repository", "api_key": "<redacted>"}},
		{"routing", `{"routing_chain_max_depth":7,"complexity_analyzer_config":{"llm":{"provider":"openai","model":"classifier","prompt":"Classify this request"},"semantic":{"provider":"openai","embedding_model":"embedding"}}}`, `{"routing_chain_max_depth":"bad"}`, map[string]string{"routing_chain_max_depth": "7", "complexity_analyzer_config.llm.prompt": "Classify this request", "complexity_analyzer_config.semantic.embedding_model": "embedding"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, codes := range [][]rbac.Permission{nil, rbac.Permissions()} {
				input := `{"name":"` + test.name + `","enabled":true,"config":` + test.config + `,"status":{"status":"active","logs":["DIAGNOSTIC"]}}`
				body := projected(t, "/api/plugins/{name}", projectionPluginSafe, input, codes...)
				cfg := gjson.Get(body, "config").Raw
				assertProjectionFields(t, cfg, test.want)
				if strings.Contains(body, "SECRET") {
					t.Fatal("builtin secret survived")
				}
				assertProjectionFields(t, body, map[string]string{"name": test.name, "enabled": "true", "status.status": "active"})
				if len(codes) == 0 && strings.Contains(body, "DIAGNOSTIC") {
					t.Fatal("plugin logs survived without reveal")
				}
				if len(codes) > 0 && !strings.Contains(body, "DIAGNOSTIC") {
					t.Fatal("explicit log reveal lost content")
				}
				if test.name == "maxim" {
					exposed := gjson.Get(cfg, "request_headers.0").Exists()
					if exposed != (len(codes) > 0) {
						t.Fatal("content config permission branch")
					}
				}
			}
			invalid := projected(t, "/api/plugins/{name}", projectionPluginSafe, `{"name":"`+test.name+`","config":`+test.bad+`}`)
			if gjson.Get(invalid, "config").Raw != "{}" {
				t.Fatal("malformed typed config did not close", invalid)
			}
		})
	}
}

func TestLegacyOTELAndCustomPluginStructures(t *testing.T) {
	body := projected(t, "/api/plugins/{name}", projectionPluginSafe, `{"name":"otel","config":{"service_name":"legacy","collector_url":"https://host/?key=SECRET","headers":{"Authorization":"SECRET"}}}`)
	assertProjectionFields(t, body, map[string]string{"config.profiles.0.service_name": "legacy", "config.profiles.0.collector_url.value": "<REDACTED>", "config.profiles.0.headers.Authorization": "<redacted>"})
	for _, codes := range [][]rbac.Permission{nil, {rbac.PluginsLoadNative}} {
		body := projected(t, "/api/plugins/{name}", projectionPluginSafe, `{"name":"custom","config":{"custom_setting":"native-owned"}}`, codes...)
		if len(codes) == 0 && gjson.Get(body, "config").Raw != "{}" {
			t.Fatal("custom config escaped without native permission")
		}
		if len(codes) > 0 {
			assertProjectionFields(t, body, map[string]string{"config.custom_setting": "native-owned"})
		}
	}
	// 输出仍可作为完整JSON结构处理；不用空响应冒充配置保留。
	if !json.Valid([]byte(body)) {
		t.Fatal("invalid normalized plugin JSON")
	}
}

func TestSemanticCacheTTLRemainsWritebackCompatible(t *testing.T) {
	for _, ttl := range []string{`"1m"`, `60`} {
		input := `{"name":"semantic_cache","config":{"provider":"openai","dimension":64,"ttl":` + ttl + `}}`
		body := projected(t, "/api/plugins/{name}", projectionPluginSafe, input)
		if gjson.Get(body, "config.ttl").Raw != ttl {
			t.Fatalf("TTL units changed: %s -> %s", ttl, gjson.Get(body, "config.ttl").Raw)
		}
	}
}
