// 本文件验证旧凭据与新目标的授权组合；只检查配置和拒绝时点，不向外部发送凭据。
package host

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
)

func credentialAccess() rbac.Access {
	return rbac.Access{Permissions: []rbac.Permission{rbac.SecurityChangeCredentialDestination}}
}
func cloneCredentialConfig[T any](t *testing.T, v T) T {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestProviderCredentialDestination(t *testing.T) {
	old := configstore.ProviderConfig{NetworkConfig: &schemas.NetworkConfig{BaseURL: "https://old.invalid/v1", ExtraHeaders: map[string]string{"Authorization": "fixture"}}}
	for _, tc := range []struct {
		name   string
		change func(*configstore.ProviderConfig)
		denied bool
	}{
		{"timeout", func(n *configstore.ProviderConfig) { n.NetworkConfig.MaxRetries++ }, false},
		{"address", func(n *configstore.ProviderConfig) { n.NetworkConfig.BaseURL = "https://new.invalid/v1" }, true},
		{"path", func(n *configstore.ProviderConfig) { n.NetworkConfig.BaseURL += "/other" }, true},
		{"tls", func(n *configstore.ProviderConfig) { n.NetworkConfig.InsecureSkipVerify = true }, true},
		{"proxy", func(n *configstore.ProviderConfig) {
			n.ProxyConfig = &schemas.ProxyConfig{URL: schemas.NewSecretVar("https://proxy.invalid")}
		}, true},
		{"request override", func(n *configstore.ProviderConfig) {
			n.CustomProviderConfig = &schemas.CustomProviderConfig{RequestPathOverrides: map[schemas.RequestType]string{schemas.ChatCompletionRequest: "https://new.invalid/chat"}}
		}, true},
		{"new credentials", func(n *configstore.ProviderConfig) {
			n.NetworkConfig.BaseURL = "https://new.invalid"
			n.NetworkConfig.ExtraHeaders = map[string]string{"Authorization": "new-fixture"}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := cloneCredentialConfig(t, old)
			tc.change(&next)
			err := providerDestination(&old, &next, rbac.Access{})
			if errors.Is(err, rbac.ErrForbidden) != tc.denied || err != nil && !tc.denied {
				t.Fatal("unexpected authorization", err)
			}
			if err := providerDestination(&old, &next, credentialAccess()); err != nil {
				t.Fatal(err)
			}
		})
	}
	old.Keys = []schemas.Key{{Value: *schemas.NewSecretVar("key-fixture")}}
	next := cloneCredentialConfig(t, old)
	next.NetworkConfig.BaseURL = "https://new.invalid"
	next.NetworkConfig.ExtraHeaders = nil
	if err := providerDestination(&old, &next, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatal("retained provider keys", err)
	}
}

func TestProviderKeyCredentialDestination(t *testing.T) {
	old := schemas.Key{Value: *schemas.NewSecretVar("env.RBAC_TEST_CREDENTIAL"), AzureKeyConfig: &schemas.AzureKeyConfig{Endpoint: *schemas.NewSecretVar("https://old.invalid")}}
	next := cloneCredentialConfig(t, old)
	next.AzureKeyConfig.Endpoint = *schemas.NewSecretVar("https://new.invalid")
	provider := &configstore.ProviderConfig{}
	if err := providerKeyDestination(provider, &old, &next, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatal("same reference", err)
	}
	next.Value = *schemas.NewSecretVar("new-fixture")
	if err := providerKeyDestination(provider, &old, &next, rbac.Access{}); err != nil {
		t.Fatal("new credentials", err)
	}
	provider.NetworkConfig = &schemas.NetworkConfig{ExtraHeaders: map[string]string{"Authorization": "inherited-fixture"}}
	if err := providerKeyDestination(provider, &old, &next, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatal("inherited header", err)
	}
	if err := providerKeyDestination(provider, &old, &next, credentialAccess()); err != nil {
		t.Fatal(err)
	}
	// 端点覆盖、云服务端点与别名必须进入同一目标比较，不能只认最外层URL。
	for _, field := range []string{"azure_key_config", "bedrock_key_config", "aliases"} {
		before := `{"value":"fixture","` + field + `":{}}`
		after := map[string]string{"azure_key_config": `{"endpoint":"https://new.invalid"}`, "bedrock_key_config": `{"endpoints":{"runtime":"new.invalid"}}`, "aliases": `{"model":{"endpoint":"https://new.invalid"}}`}[field]
		var a, b schemas.Key
		if json.Unmarshal([]byte(before), &a) != nil || json.Unmarshal([]byte(`{"value":"fixture","`+field+`":`+after+`}`), &b) != nil {
			t.Fatal("fixture")
		}
		if err := providerKeyDestination(&configstore.ProviderConfig{}, &a, &b, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
			t.Fatal(field, err)
		}
	}
}

type credentialProxyStore struct {
	configstore.ConfigStore
	proxy *tables.GlobalProxyConfig
}

func (s *credentialProxyStore) GetProxyConfig(context.Context) (*tables.GlobalProxyConfig, error) {
	return s.proxy, nil
}
func TestProxyCredentialDestination(t *testing.T) {
	store := &credentialProxyStore{proxy: &tables.GlobalProxyConfig{URL: "https://old.invalid", Password: "fixture"}}
	a := NewAdapter(nil, &lib.Config{ConfigStore: store}, nil)
	next := *store.proxy
	next.URL = "https://new.invalid"
	next.Password = redacted
	err := a.proxyUpdate(context.Background(), &next, rbac.Access{})
	var denial *handlers.ConsolePolicyError
	if !errors.As(err, &denial) || denial.Status != 403 {
		t.Fatal(err)
	}
	if err = a.proxyUpdate(context.Background(), &next, credentialAccess()); err != nil {
		t.Fatal(err)
	}
	next.Password = "new-fixture"
	if err = a.proxyUpdate(context.Background(), &next, rbac.Access{}); err != nil {
		t.Fatal(err)
	}
	if store.proxy.URL != "https://old.invalid" {
		t.Fatal("current mutated")
	}
}

func TestWebhookCredentialDestination(t *testing.T) {
	repo := &routeRepository{codes: []rbac.Permission{rbac.NotificationsManage}}
	config := &lib.Config{}
	old := &tables.TableWebhookEndpoint{ID: "fixture", URL: "https://old.invalid", Headers: map[string]schemas.SecretVar{"Authorization": *schemas.NewSecretVar("fixture")}}
	config.SetWebhookEndpoint(old)
	a := NewAdapter(rbac.New(repo), config, nil)
	next := cloneCredentialConfig(t, *old)
	next.URL = "https://new.invalid"
	err := a.webhookUpdate(context.Background(), rbac.Subject{}, handlers.ConsoleWebhookUpdate, &next)
	var denial *handlers.ConsolePolicyError
	if !errors.As(err, &denial) || denial.Status != 403 {
		t.Fatal(err)
	}
	repo.codes = append(repo.codes, rbac.SecurityChangeCredentialDestination)
	if err = a.webhookUpdate(context.Background(), rbac.Subject{}, handlers.ConsoleWebhookUpdate, &next); err != nil {
		t.Fatal(err)
	}
	repo.codes = repo.codes[:1]
	next.Headers = nil
	if err = a.webhookUpdate(context.Background(), rbac.Subject{}, handlers.ConsoleWebhookUpdate, &next); err != nil {
		t.Fatal(err)
	}
}

func TestMCPCredentialDestination(t *testing.T) {
	old := &schemas.MCPClientConfig{AuthType: schemas.MCPAuthTypePerUserOauth}
	next := *old
	next.TLSConfig = &schemas.MCPTLSConfig{InsecureSkipVerify: true}
	if err := mcpDestination(old, &next, nil, nil, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatal("stored user token", err)
	}
	if err := mcpDestination(old, &next, nil, nil, credentialAccess()); err != nil {
		t.Fatal(err)
	}
	before := &tables.TableOauthConfig{ClientSecret: schemas.NewSecretVar("env.RBAC_TEST_OAUTH"), TokenURL: "https://old.invalid"}
	after := &configstore.MCPOAuthConfigFields{ClientSecret: schemas.NewSecretVar("env.RBAC_TEST_OAUTH"), TokenURL: "https://new.invalid"}
	if err := mcpDestination(old, old, before, after, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatal("OAuth secret", err)
	}
	after.ClientSecret = schemas.NewSecretVar("new-fixture")
	if err := mcpDestination(old, old, before, after, rbac.Access{}); err != nil {
		t.Fatal(err)
	}
	target := "https://new.invalid"
	next = *old
	next.TokenExchange = &schemas.MCPTokenExchangeConfig{AuthorizationServerURL: &target, UseIdPCredentials: true}
	if err := mcpDestination(old, &next, nil, nil, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
		t.Fatal("deployment credentials", err)
	}
}

func TestPluginCredentialDestination(t *testing.T) {
	for _, tc := range []struct{ name, before, after string }{
		{"telemetry", `{"push_gateway":{"push_gateway_url":"https://old.invalid","basic_auth":{"password":"fixture"}}}`, `{"push_gateway":{"push_gateway_url":"https://new.invalid","basic_auth":{"password":"fixture"}}}`},
		{"otel", `{"collector_url":"https://old.invalid","headers":{"Authorization":"fixture"}}`, `{"profiles":[{"collector_url":"https://new.invalid","headers":{"Authorization":"fixture"}}]}`},
	} {
		var before, after any
		json.Unmarshal([]byte(tc.before), &before)
		json.Unmarshal([]byte(tc.after), &after)
		if err := pluginDestination(tc.name, before, after, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
			t.Fatal(tc.name, err)
		}
		if err := pluginDestination(tc.name, before, after, credentialAccess()); err != nil {
			t.Fatal(err)
		}
		if err := pluginDestination(tc.name, before, before, rbac.Access{}); err != nil {
			t.Fatal("no-op", err)
		}
	}
	// 同一凭据原已用于多个profile，普通保存不能被误判为新增目标。
	var multi any
	json.Unmarshal([]byte(`{"profiles":[{"collector_url":"https://a.invalid","headers":{"Authorization":"fixture"}},{"collector_url":"https://b.invalid","headers":{"Authorization":"fixture"}}]}`), &multi)
	if err := pluginDestination("otel", multi, multi, rbac.Access{}); err != nil {
		t.Fatal(err)
	}
}
