// 本文件在响应返回前隐藏敏感字段；不能识别的结构返回503，避免漏出未经检查的内容。
package host

import (
	"bytes"
	"encoding/json"
	"mime"
	"net/url"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	"github.com/maximhq/bifrost/core/providers/utils"
	"github.com/valyala/fasthttp"
)

type object = map[string]any

// projectSensitiveResponse 按接口规则处理响应；提示词和协议内容保持原格式，配置JSON按权限隐藏字段。
func projectSensitiveResponse(c *fasthttp.RequestCtx, r requestAccess) error {
	switch r.Route.Projection {
	case projectionProtocol, projectionProtocolScoped, projectionPromptContent, projectionSkillContent, projectionDebugExplicit, projectionMessageFilter:
		return nil
	}
	c.Response.Header.Set("Cache-Control", "no-store")
	if c.Response.StatusCode() >= fasthttp.StatusBadRequest {
		status := c.Response.StatusCode()
		resetKeepingRequestID(c)
		rbachttp.Error(c, rbachttp.FromStatus(status))
		c.Response.Header.Set("Cache-Control", "no-store")
		return nil
	}
	if c.Response.IsBodyStream() {
		_ = c.Response.CloseBodyStream()
		return rbac.ErrUnavailable
	}
	if c.Response.StatusCode() == fasthttp.StatusNoContent {
		return nil
	}
	media, _, e := mime.ParseMediaType(string(c.Response.Header.ContentType()))
	if e != nil || media != "application/json" {
		return rbac.ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(c.Response.Body()))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil {
		return rbac.ErrUnavailable
	}
	switch value.(type) {
	case map[string]any, []any:
	default:
		return rbac.ErrUnavailable
	}
	if e := validateShape(value, r); e != nil {
		return e
	}
	switch r.Route.Projection {
	case projectionProviderSafe:
		projectProviders(value)
	case projectionKeyMetadata:
		value = projectKeyMetadata(value)
	case projectionVkValues:
		if !r.Access.Allows(rbac.VirtualKeysRevealKey) {
			projectVirtualKeys(value)
		}
	case projectionGovernanceNestedVk:
		projectGovernance(value, r.Access)
	case projectionLogsContent:
		if !r.Access.Allows(rbac.LogsRevealContent) {
			value, e = projectLogs(value, r.Route.Pattern)
			if e != nil {
				return e
			}
		}
	case projectionMcpSafe:
		projectMCP(value)
	case projectionPluginSafe:
		projectPlugins(value, r.Access)
	case projectionSettingsSafe:
		projectSettings(value)
	case projectionRoutingSafe:
		projectRouting(value, r.Access)
	case projectionWebhookSafe:
		projectWebhooks(value, r.Access, r.Route.Pattern)
	case projectionOrdinary, projectionBrandingSafe, projectionNotificationPolicy:
	default:
		return rbac.ErrUnavailable
	}
	body, e := utils.MarshalSorted(value)
	if e != nil {
		return rbac.ErrUnavailable
	}
	c.Response.Header.Del("Content-Length")
	c.Response.Header.Del("Content-Encoding")
	c.Response.Header.Del("ETag")
	c.Response.SetBody(body)
	return nil
}

// objects 只沿指定的列表或子对象处理字段，例如providers中的network_config；不会根据字段名字猜哪里有秘密。
func objects(value any, children []string, fn func(object)) {
	switch v := value.(type) {
	case []any:
		for _, child := range v {
			objects(child, children, fn)
		}
	case map[string]any:
		fn(v)
		for _, key := range children {
			if child, ok := v[key]; ok {
				objects(child, children, fn)
			}
		}
	}
}

func keep(v object, keys []string) object {
	out := object{}
	for _, key := range keys {
		if value, ok := v[key]; ok {
			out[key] = value
		}
	}
	return out
}

func maskHeaders(v any) {
	if headers, ok := v.(map[string]any); ok {
		for key := range headers {
			headers[key] = redacted
		}
	}
}

const redacted = "<redacted>"

func maskURL(v object, key string) {
	maskURLWithParser(v, key, url.Parse)
}

// maskURLWithParser 隐藏解析失败或带认证信息、查询、片段的地址；调用方只选择解析规则。
func maskURLWithParser(v object, key string, parse func(string) (*url.URL, error)) {
	raw, ok := v[key].(string)
	if !ok {
		return
	}
	u, e := parse(raw)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		v[key] = redacted
	}
}

func maskHeaderMaps(value object) {
	for _, key := range []string{"headers", "extra_headers", "trace_headers", "metrics_headers"} {
		maskHeaders(value[key])
	}
}

// projectKeyMetadata 只返回密钥名称、所属厂商和模型等信息，不返回密钥本身。
func projectKeyMetadata(value any) any {
	fields := []string{"key_id", "name", "provider", "models", "blacklisted_models", "weight", "config_hash"}
	switch v := value.(type) {
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if row, ok := item.(map[string]any); ok {
				out = append(out, keep(row, fields))
			}
		}
		return out
	case map[string]any:
		if keys, ok := v["keys"]; ok {
			v["keys"] = projectKeyMetadata(keys)
			return v
		}
		return keep(v, fields)
	}
	return value
}

// projectVirtualKeys 将响应中的虚拟密钥明文替换为<redacted>。
func projectVirtualKeys(value any) {
	objects(value, []string{"virtual_keys", "virtual_key", "keys", "results", "rotated_keys", "successful"}, func(v object) {
		if _, ok := v["value"]; ok {
			v["value"] = redacted
		}
	})
}

// projectGovernance 处理团队、客户等响应里附带的密钥和厂商配置，避免从关联数据中看到秘密。
func projectGovernance(value any, access rbac.Access) {
	objects(value, []string{"customers", "customer", "teams", "team", "virtual_keys", "virtual_key", "providers", "provider"}, func(v object) {
		if !access.Allows(rbac.VirtualKeysRevealKey) {
			if _, ok := v["value"]; ok {
				v["value"] = redacted
			}
		}
		projectProviders(v)
	})
}

// projectMCP 隐藏工具进程的命令、环境变量和故障原文；仍保留故障发生阶段和时间。
func projectMCP(value any) {
	objects(value, []string{"clients", "client", "client_configs", "client_config", "mcp_clients", "config", "sessions", "session", "oauth_config"}, func(v object) {
		delete(v, "stdio_config")
		if failure, ok := v["last_failure"].(map[string]any); ok {
			v["last_failure"] = keep(failure, []string{"stage", "at", "since"})
		}
		if nodes, ok := v["node_states"].(map[string]any); ok {
			for _, state := range nodes {
				if node, ok := state.(map[string]any); ok {
					if failure, ok := node["last_failure"].(map[string]any); ok {
						node["last_failure"] = keep(failure, []string{"stage", "at", "since"})
					}
				}
			}
		}
	})
}

// projectRouting 保留路由配置；没有日志正文权限时，移除诊断中的请求、响应和错误原文。
func projectRouting(value any, access rbac.Access) {
	projectProviders(value)
	if !access.Allows(rbac.LogsRevealContent) {
		objects(value, []string{"rules", "rule", "status", "result", "results", "diagnostics"}, func(v object) {
			for _, key := range []string{"request", "response", "input", "output", "error", "logs", "trace", "metadata"} {
				delete(v, key)
			}
		})
	}
}

// validateShape 检查预期的对象和列表是否仍是原来的结构；结构变了就拒绝返回，避免跳过字段隐藏。
func validateShape(value any, r requestAccess) error {
	if r.Route.Projection == projectionPluginSafe && (r.Route.Pattern == "/api/plugins/builtins" || r.Route.Pattern == "/api/plugins/loaded") {
		return validatePluginNames(value)
	}

	var failure error
	// 这些列表里的每一项都应是对象；结构变了就不能假定敏感字段已被处理。
	collections := map[projection][]string{
		projectionProviderSafe:       {"providers", "keys"},
		projectionKeyMetadata:        {"keys"},
		projectionVkValues:           {"virtual_keys", "results", "rotated_keys", "successful"},
		projectionGovernanceNestedVk: {"customers", "teams", "virtual_keys", "providers"},
		projectionLogsContent:        {"logs", "items"},
		projectionMcpSafe:            {"clients", "client_configs", "mcp_clients", "sessions"},
		projectionPluginSafe:         {"plugins"},
		projectionWebhookSafe:        {"endpoints", "webhooks", "deliveries"},
	}
	children := collections[r.Route.Projection]
	objects(value, children, func(v object) {
		for _, key := range children {
			if raw, exists := v[key]; exists && raw != nil {
				rows, ok := raw.([]any)
				if !ok {
					failure = rbac.ErrUnavailable
					continue
				}
				for _, row := range rows {
					if _, ok := row.(map[string]any); !ok {
						failure = rbac.ErrUnavailable
					}
				}
			}
		}
	})
	checkObject := func(v object, key string) {
		if child, exists := v[key]; exists && child != nil {
			if _, ok := child.(map[string]any); !ok {
				failure = rbac.ErrUnavailable
			}
		}
	}
	switch r.Route.Projection {
	case projectionProviderSafe:
		objects(value, []string{"providers", "provider", "keys", "key"}, func(v object) {
			checkObject(v, "network_config")
			checkObject(v, "proxy_config")
			if network, ok := v["network_config"].(map[string]any); ok {
				checkObject(network, "extra_headers")
			}
		})
	case projectionSettingsSafe:
		objects(value, []string{"restart_required", "config"}, func(v object) {
			for _, key := range []string{"client_config", "framework_config", "proxy_config"} {
				checkObject(v, key)
			}
		})
	case projectionVkValues:
		objects(value, []string{"virtual_keys", "virtual_key", "results"}, func(v object) {
			if raw, ok := v["value"]; ok && raw != nil {
				if _, ok := raw.(string); !ok {
					failure = rbac.ErrUnavailable
				}
			}
		})
	}
	return failure
}
