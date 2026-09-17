// 本文件隐藏厂商配置中的凭据，并在更新配置时用原值替换隐藏占位文字。
package host

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

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
		}{n.BaseURL, n.InsecureSkipVerify, schemas.SecretVarAsString(n.CACertPEM), proxyTarget(c.ProxyConfig), base, paths}
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
	}{p.Type, schemas.SecretVarAsString(p.URL), schemas.SecretVarAsString(p.CACertPEM)}
}

func proxyCredentials(p *schemas.ProxyConfig) []string {
	if p == nil {
		return nil
	}
	return []string{schemas.SecretVarAsString(p.Username), schemas.SecretVarAsString(p.Password)}
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
		// 继承的厂商请求头仍随新Key发送，非空值视为保留的凭据。
		retained = retained || slices.ContainsFunc(headerValues(provider.NetworkConfig.ExtraHeaders), func(v string) bool { return v != "" })
	}
	return credentialDestination(access, retained, before, after)
}

// keyConnection 从Key的实际类型读取目标和凭据，包含别名下的地址；不把模型名、权重等普通字段算作目标。
// SecretVar用存储表达比较，避免环境变量引用与API对象格式不同；不记录这些值到日志或错误。
func keyConnection(key *schemas.Key) (map[string]string, []string, error) {
	data, err := json.Marshal(key)
	if err != nil {
		return nil, nil, rbac.ErrUnavailable
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return nil, nil, rbac.ErrUnavailable
	}
	targets := map[string]string{}
	var credentials []string
	var walk func(any, string) error
	walk = func(value any, path string) error {
		m, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		for name, child := range m {
			field := path + "/" + name
			switch name {
			case "aliases":
				// 别名是用户填写的模型名，不是配置字段；直接检查每个别名的配置。
				aliases, _ := child.(map[string]any)
				for alias, config := range aliases {
					if err := walk(config, field+"/"+alias); err != nil {
						return err
					}
				}
			case "use_anthropic_endpoints", "use_deployments_endpoint", "force_single_region":
				if flag, ok := child.(bool); ok {
					targets[field] = strconv.FormatBool(flag)
				}
			case "endpoint", "url", "workspace_url", "github_domain", "region", "arn", "role_arn", "project_id", "project_number", "runtime", "control_plane", "mantle", "agent_runtime", "s3":
				text, err := configSecretText(child)
				if err != nil {
					return err
				}
				if text != "" {
					targets[field] = text
				}
			case "value", "client_secret", "auth_credentials", "access_key", "secret_key", "session_token", "private_key":
				text, err := configSecretText(child)
				if err != nil {
					return err
				}
				credentials = append(credentials, text)
			default:
				if err := walk(child, field); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(value, ""); err != nil {
		return nil, nil, err
	}
	// 云厂商还可使用服务器身份；没有显式密钥不等于没有需要保护的凭据。
	if key != nil {
		if key.BedrockKeyConfig != nil && schemas.SecretVarAsString(&key.BedrockKeyConfig.SecretKey) == "" && schemas.SecretVarAsString(&key.Value) == "" {
			credentials = append(credentials, "ambient:bedrock")
		}
		if key.BedrockMantleKeyConfig != nil && schemas.SecretVarAsString(&key.BedrockMantleKeyConfig.SecretKey) == "" && schemas.SecretVarAsString(&key.Value) == "" {
			credentials = append(credentials, "ambient:bedrock-mantle")
		}
		if key.VertexKeyConfig != nil && schemas.SecretVarAsString(&key.VertexKeyConfig.AuthCredentials) == "" {
			credentials = append(credentials, "ambient:vertex")
		}
	}
	return targets, credentials, nil
}

// configSecretText 读取Key字段的字符串或SecretVar对象，用存储表达比较环境变量引用。
func configSecretText(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", rbac.ErrUnavailable
	}
	var secret schemas.SecretVar
	if json.Unmarshal(data, &secret) != nil {
		return "", rbac.ErrInvalid
	}
	return schemas.SecretVarAsString(&secret), nil
}
