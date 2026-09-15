// 本文件隐藏厂商配置中的凭据，并在更新配置时用原值替换隐藏占位文字。
package host

import (
	"context"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// projectProviders 隐藏自定义请求头、带凭据的地址和运行错误原文，保留可展示的厂商配置。
func projectProviders(value any) {
	objects(value, []string{"providers", "provider", "keys", "key"}, func(v object) {
		if network, ok := v["network_config"].(map[string]any); ok {
			maskHeaders(network["extra_headers"])
			maskURL(network, "base_url")
		}
		// 这些是运行诊断而非用户填写的配置说明。
		if _, ok := v["provider_status"]; ok {
			delete(v, "description")
			if status, ok := v["provider_status"].(map[string]any); ok {
				delete(status, "error")
				delete(status, "message")
			}
		}
	})
}

// restoreProvider 将更新请求中的<redacted>换回原值；找不到对应旧值时拒绝保存。
// 请求头仍按整份替换：没有提交的头会删除，传空对象就是清空。
func restoreProvider(_ context.Context, current *configstore.ProviderConfig, network *schemas.NetworkConfig, _ *schemas.ProxyConfig) error {
	if network == nil {
		return nil
	}
	for key, value := range network.ExtraHeaders {
		if value == redacted {
			if current == nil || current.NetworkConfig == nil {
				return consoleError(rbac.ErrInvalid)
			}
			old, ok := current.NetworkConfig.ExtraHeaders[key]
			if !ok || old == redacted {
				return consoleError(rbac.ErrInvalid)
			}
			network.ExtraHeaders[key] = old
		}
	}
	if network.BaseURL == redacted {
		if current == nil || current.NetworkConfig == nil || current.NetworkConfig.BaseURL == "" || current.NetworkConfig.BaseURL == redacted {
			return consoleError(rbac.ErrInvalid)
		}
		network.BaseURL = current.NetworkConfig.BaseURL
	}
	return nil
}

// checkProviderInput 不允许创建厂商时使用<redacted>，因为新厂商没有可以保留的旧值。
func checkProviderInput(c *fasthttp.RequestCtx, r requestAccess) error {
	if !c.IsPost() || r.Route.Pattern != "/api/providers" {
		return nil
	}
	body := c.PostBody()
	if gjson.GetBytes(body, "network_config.base_url").String() == redacted {
		return rbac.ErrInvalid
	}
	invalid := false
	gjson.GetBytes(body, "network_config.extra_headers").ForEach(func(_, value gjson.Result) bool {
		invalid = value.String() == redacted
		return !invalid
	})
	if invalid {
		return rbac.ErrInvalid
	}
	return nil
}
