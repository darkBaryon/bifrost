// 本文件检查旧凭据是否被用于新的地址或连接方式；各功能在恢复隐藏字段后调用。
package host

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// credentialDestination 不限制普通配置更新；有旧凭据且发送目标改变时才需要敏感权限。
// 地址按完整值比较，路径、查询参数和TLS信任变化也算改变，避免只比较主机名留下缺口。
func credentialDestination(access rbac.Access, retained bool, before, after any) error {
	if retained && !reflect.DeepEqual(before, after) {
		return requirePermissions(access, rbac.SecurityChangeCredentialDestination)
	}
	return nil
}

func secretText(v *schemas.SecretVar) string {
	return schemas.SecretVarAsString(v)
}

func retainedValues(before, after []string) bool {
	for _, value := range before {
		if value != "" && slices.Contains(after, value) {
			return true
		}
	}
	return false
}

func headerValues(headers map[string]string) []string {
	values := make([]string, 0, len(headers))
	for _, value := range headers {
		values = append(values, value)
	}
	return values
}

func secretHeaderValues(headers map[string]schemas.SecretVar) []string {
	values := make([]string, 0, len(headers))
	for _, value := range headers {
		values = append(values, secretText(&value))
	}
	return values
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
		if key.BedrockKeyConfig != nil && secretText(&key.BedrockKeyConfig.SecretKey) == "" && secretText(&key.Value) == "" {
			credentials = append(credentials, "ambient:bedrock")
		}
		if key.BedrockMantleKeyConfig != nil && secretText(&key.BedrockMantleKeyConfig.SecretKey) == "" && secretText(&key.Value) == "" {
			credentials = append(credentials, "ambient:bedrock-mantle")
		}
		if key.VertexKeyConfig != nil && secretText(&key.VertexKeyConfig.AuthCredentials) == "" {
			credentials = append(credentials, "ambient:vertex")
		}
	}
	return targets, credentials, nil
}

// configSecretText 用于插件的存储JSON和API中的SecretVar两种输入；不是通用配置字段解析器。
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
	return secretText(&secret), nil
}

// mcpDestination 检查MCP可修改的TLS及OAuth目标。连接URL本身由宿主固定，不能通过更新接口修改。
func mcpDestination(current, desired *schemas.MCPClientConfig, oldOAuth *tables.TableOauthConfig, nextOAuth *configstore.MCPOAuthConfigFields, access rbac.Access) error {
	if current == nil || desired == nil {
		return rbac.ErrUnavailable
	}
	retained := retainedValues(secretHeaderValues(current.Headers), secretHeaderValues(desired.Headers))
	switch current.AuthType {
	case schemas.MCPAuthTypeOauth, schemas.MCPAuthTypePerUserOauth, schemas.MCPAuthTypePerUserHeaders, schemas.MCPAuthTypeTokenExchange:
		retained = true // 用户凭据或已保存令牌仍可能用于连接，不能只检查本次可见的headers。
	}
	tls := func(c *schemas.MCPTLSConfig) any {
		if c == nil {
			c = &schemas.MCPTLSConfig{}
		}
		return struct {
			Insecure bool
			CA       string
		}{c.InsecureSkipVerify, secretText(c.CACertPEM)}
	}
	if err := credentialDestination(access, retained, tls(current.TLSConfig), tls(desired.TLSConfig)); err != nil {
		return err
	}
	if oldOAuth != nil && nextOAuth != nil {
		registration := ""
		if oldOAuth.RegistrationURL != nil {
			registration = *oldOAuth.RegistrationURL
		}
		retained := retainedValues([]string{secretText(oldOAuth.ClientSecret)}, []string{secretText(nextOAuth.ClientSecret)})
		before := []string{oldOAuth.AuthorizeURL, oldOAuth.TokenURL, registration}
		after := []string{nextOAuth.AuthorizeURL, nextOAuth.TokenURL, nextOAuth.RegistrationURL}
		if err := credentialDestination(access, retained, before, after); err != nil {
			return err
		}
	}
	if desired.TokenExchange != nil {
		target := func(c *schemas.MCPTokenExchangeConfig) string {
			if c == nil || c.AuthorizationServerURL == nil {
				return ""
			}
			return *c.AuthorizationServerURL
		}
		// 交换请求还会携带调用者的身份令牌；换一个client_secret也不能把该令牌交给新的服务器。
		if err := credentialDestination(access, true, target(current.TokenExchange), target(desired.TokenExchange)); err != nil {
			return err
		}
	}
	return nil
}
