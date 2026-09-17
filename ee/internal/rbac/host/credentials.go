// 本文件检查旧凭据是否被用于新的地址或连接方式；各功能在恢复隐藏字段后调用。
package host

import (
	"net/url"
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
		values = append(values, schemas.SecretVarAsString(&value))
	}
	return values
}

// parseProxyURL 按宿主httpproxy的规则解析代理地址，兼容省略http://的写法。
// 上游解析函数未导出；读取时隐藏认证信息、更新时比较旧凭据共用这里，不改写保存的地址。
func parseProxyURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if fallback, fallbackErr := url.Parse("http://" + raw); fallbackErr == nil {
			return fallback, nil
		}
	}
	return u, err
}

// proxyURLCredential 取代理地址实际携带的认证值；没有认证时返回空字符串。
// 有非空密码时返回密码，只改用户名不会换掉此值；否则返回用户名，宿主仍会发送只含用户名的认证。
func proxyURLCredential(raw string) (string, error) {
	u, err := parseProxyURL(raw)
	if err != nil {
		return "", err
	}
	if u.User == nil {
		return "", nil
	}
	if password, ok := u.User.Password(); ok && password != "" {
		return password, nil
	}
	return u.User.Username(), nil
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
		}{c.InsecureSkipVerify, schemas.SecretVarAsString(c.CACertPEM)}
	}
	if err := credentialDestination(access, retained, tls(current.TLSConfig), tls(desired.TLSConfig)); err != nil {
		return err
	}
	if oldOAuth != nil && nextOAuth != nil {
		registration := ""
		if oldOAuth.RegistrationURL != nil {
			registration = *oldOAuth.RegistrationURL
		}
		retained := retainedValues([]string{schemas.SecretVarAsString(oldOAuth.ClientSecret)}, []string{schemas.SecretVarAsString(nextOAuth.ClientSecret)})
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
