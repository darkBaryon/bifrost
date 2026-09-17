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

// providerDestination 在宿主完成占位符恢复后检查；当前Key会原样保留，停用的Key也不能借机改到新地址。
func providerDestination(current, desired *configstore.ProviderConfig, access rbac.Access) error {
	if current == nil || desired == nil {
		return rbac.ErrUnavailable
	}
	oldNetwork, nextNetwork := schemas.NetworkConfig{}, schemas.NetworkConfig{}
	if current.NetworkConfig != nil {
		oldNetwork = *current.NetworkConfig
	}
	if desired.NetworkConfig != nil {
		nextNetwork = *desired.NetworkConfig
	}
	retained := len(current.Keys) > 0 || retainedValues(headerValues(oldNetwork.ExtraHeaders), headerValues(nextNetwork.ExtraHeaders))
	target := func(c *configstore.ProviderConfig, n schemas.NetworkConfig) any {
		base := schemas.ModelProvider("")
		var paths map[schemas.RequestType]string
		if c.CustomProviderConfig != nil {
			base = c.CustomProviderConfig.BaseProviderType
			paths = c.CustomProviderConfig.RequestPathOverrides
		}
		return struct {
			URL      string
			Insecure bool
			CA       string
			Proxy    any
			Base     schemas.ModelProvider
			Paths    map[schemas.RequestType]string
		}{n.BaseURL, n.InsecureSkipVerify, secretText(n.CACertPEM), proxyTarget(c.ProxyConfig), base, paths}
	}
	if err := credentialDestination(access, retained, target(current, oldNetwork), target(desired, nextNetwork)); err != nil {
		return err
	}
	return credentialDestination(access, retainedValues(proxyCredentials(current.ProxyConfig), proxyCredentials(desired.ProxyConfig)), proxyTarget(current.ProxyConfig), proxyTarget(desired.ProxyConfig))
}

func proxyTarget(p *schemas.ProxyConfig) any {
	if p == nil {
		p = &schemas.ProxyConfig{}
	}
	return struct {
		Type    schemas.ProxyType
		URL, CA string
	}{p.Type, secretText(p.URL), secretText(p.CACertPEM)}
}

func proxyCredentials(p *schemas.ProxyConfig) []string {
	if p == nil {
		return nil
	}
	return []string{secretText(p.Username), secretText(p.Password)}
}

// providerKeyDestination 同时考虑该Key保留的秘密和仍会随请求发送的厂商级请求头。
func providerKeyDestination(provider *configstore.ProviderConfig, current, desired *schemas.Key, access rbac.Access) error {
	if provider == nil || desired == nil {
		return rbac.ErrUnavailable
	}
	if current == nil {
		current = &schemas.Key{}
	}
	before, oldSecrets, err := keyConnection(current)
	if err != nil {
		return err
	}
	after, newSecrets, err := keyConnection(desired)
	if err != nil {
		return err
	}
	retained := retainedValues(oldSecrets, newSecrets)
	if provider.NetworkConfig != nil {
		retained = retained || retainedValues(headerValues(provider.NetworkConfig.ExtraHeaders), headerValues(provider.NetworkConfig.ExtraHeaders))
	}
	return credentialDestination(access, retained, before, after)
}
