// 本文件验证旧凭据与新目标的授权组合；只检查配置和拒绝时点，不向外部发送凭据。
package host

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
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

// TestProviderProxyCredentialDestination 单独验证厂商没有Key和请求头时，代理认证仍受保护。
func TestProviderProxyCredentialDestination(t *testing.T) {
	t.Setenv("RBAC_TEST_PROXY_USERNAME", "")
	t.Setenv("RBAC_TEST_PROXY_PASSWORD", "")
	for _, tc := range []struct {
		name          string
		before, after *schemas.ProxyConfig
		denied        bool
	}{
		{"URL authentication", &schemas.ProxyConfig{URL: schemas.NewSecretVar("fixture:synthetic@old.invalid:8080")}, &schemas.ProxyConfig{URL: schemas.NewSecretVar("other:synthetic@new.invalid:8080")}, true},
		{"new password", &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:synthetic@old.invalid")}, &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:new-fixture@new.invalid")}, false},
		{"explicit override", &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:synthetic@old.invalid")}, &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:synthetic@new.invalid"), Username: schemas.NewSecretVar("fixture"), Password: schemas.NewSecretVar("new-fixture")}, false},
		{"partial override", &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:synthetic@old.invalid")}, &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:synthetic@new.invalid"), Username: schemas.NewSecretVar("other")}, true},
		{"unresolved override", &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:synthetic@old.invalid")}, &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:synthetic@new.invalid"), Username: schemas.NewSecretVar("env.RBAC_TEST_PROXY_USERNAME"), Password: schemas.NewSecretVar("env.RBAC_TEST_PROXY_PASSWORD")}, true},
		{"proxy type", &schemas.ProxyConfig{Type: schemas.HTTPProxy, URL: schemas.NewSecretVar("http://fixture:synthetic@proxy.invalid")}, &schemas.ProxyConfig{Type: schemas.Socks5Proxy, URL: schemas.NewSecretVar("http://fixture:synthetic@proxy.invalid")}, true},
		{"no authentication", &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://old.invalid")}, &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://new.invalid")}, false},
		{"invalid old URL", &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://fixture:%zz@old.invalid")}, &schemas.ProxyConfig{URL: schemas.NewSecretVar("http://new.invalid")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.before.Type == "" {
				tc.before.Type = schemas.HTTPProxy
			}
			if tc.after.Type == "" {
				tc.after.Type = schemas.HTTPProxy
			}
			before, after := &configstore.ProviderConfig{ProxyConfig: tc.before}, &configstore.ProviderConfig{ProxyConfig: tc.after}
			err := providerDestination(before, after, rbac.Access{})
			if errors.Is(err, rbac.ErrForbidden) != tc.denied || err != nil && !tc.denied {
				t.Fatal("unexpected proxy authorization", err)
			}
			if err := providerDestination(before, after, credentialAccess()); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		proxy schemas.ProxyConfig
	}{
		{"URL credentials", schemas.ProxyConfig{Type: schemas.HTTPProxy, URL: schemas.NewSecretVar("http://fixture:synthetic@proxy.invalid")}},
		{"URL reference", schemas.ProxyConfig{Type: schemas.HTTPProxy, URL: schemas.NewSecretVar("env.RBAC_TEST_PROXY")}},
		{"password reference", schemas.ProxyConfig{Type: schemas.HTTPProxy, URL: schemas.NewSecretVar("http://proxy.invalid"), Username: schemas.NewSecretVar("fixture"), Password: schemas.NewSecretVar("env.RBAC_TEST_PROXY_PASSWORD")}},
		{"environment proxy", schemas.ProxyConfig{Type: schemas.EnvProxy}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := &configstore.ProviderConfig{ProxyConfig: &tc.proxy}
			after := cloneCredentialConfig(t, *before)
			after.ProxyConfig.CACertPEM = schemas.NewSecretVar("fixture-ca")
			if err := providerDestination(before, &after, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
				t.Fatal(err)
			}
			if err := providerDestination(before, &after, credentialAccess()); err != nil {
				t.Fatal(err)
			}
			if err := providerDestination(before, before, rbac.Access{}); err != nil {
				t.Fatal("unchanged proxy", err)
			}
		})
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
	t.Run("alias names", func(t *testing.T) {
		for _, name := range []string{"value", "client_secret", "endpoint", "aliases"} {
			var old, next schemas.Key
			decode := func(endpoint string, key *schemas.Key) {
				if err := json.Unmarshal([]byte(`{"value":"fixture","aliases":{"`+name+`":{"model_id":"model","endpoint":"`+endpoint+`"}}}`), key); err != nil {
					t.Fatal(err)
				}
			}
			decode("https://old.invalid", &old)
			decode("https://new.invalid", &next)
			if err := providerKeyDestination(&configstore.ProviderConfig{}, &old, &next, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
				t.Fatal(name, err)
			}
			if err := providerKeyDestination(&configstore.ProviderConfig{}, &old, &next, credentialAccess()); err != nil {
				t.Fatal(name, err)
			}
		}
	})
	t.Run("alias inference profile", func(t *testing.T) {
		var before, after schemas.Key
		if err := json.Unmarshal([]byte(`{"value":"fixture","aliases":{"model":{"model_id":"model","inference_profile_arn":"old-profile"}}}`), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(`{"value":"fixture","aliases":{"model":{"model_id":"model","inference_profile_arn":"new-profile"}}}`), &after); err != nil {
			t.Fatal(err)
		}
		if err := providerKeyDestination(&configstore.ProviderConfig{}, &before, &after, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
			t.Fatal(err)
		}
		if err := providerKeyDestination(&configstore.ProviderConfig{}, &before, &after, credentialAccess()); err != nil {
			t.Fatal(err)
		}
	})
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
	if !errors.As(err, &denial) || denial.Status != fasthttp.StatusForbidden {
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
	t.Run("proxy TLS", func(t *testing.T) {
		store := &credentialProxyStore{proxy: &tables.GlobalProxyConfig{URL: "https://old.invalid", Password: "fixture"}}
		a := NewAdapter(nil, &lib.Config{ConfigStore: store}, nil)
		next := *store.proxy
		next.Password, next.SkipTLSVerify = redacted, true
		var denial *handlers.ConsolePolicyError
		if err := a.proxyUpdate(context.Background(), &next, rbac.Access{}); !errors.As(err, &denial) || denial.Status != fasthttp.StatusForbidden {
			t.Fatal("TLS change must require permission", err)
		}
		if err := a.proxyUpdate(context.Background(), &next, credentialAccess()); err != nil {
			t.Fatal(err)
		}
		next.SkipTLSVerify, next.Timeout = false, 60
		if err := a.proxyUpdate(context.Background(), &next, rbac.Access{}); err != nil {
			t.Fatal("ordinary update", err)
		}
	})
	t.Run("URL authentication and TLS", func(t *testing.T) {
		store := &credentialProxyStore{proxy: &tables.GlobalProxyConfig{URL: "https://fixture:synthetic@proxy.invalid"}}
		a := NewAdapter(nil, &lib.Config{ConfigStore: store}, nil)
		next := *store.proxy
		next.URL, next.SkipTLSVerify = redacted, true
		var denial *handlers.ConsolePolicyError
		if err := a.proxyUpdate(context.Background(), &next, rbac.Access{}); !errors.As(err, &denial) || denial.Status != fasthttp.StatusForbidden {
			t.Fatal(err)
		}
		if err := a.proxyUpdate(context.Background(), &next, credentialAccess()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("proxy address forms", func(t *testing.T) {
		for _, tc := range []struct{ name, raw string }{
			{"without scheme", "fixture:synthetic@proxy.invalid:8080"},
			{"http", "http://fixture:synthetic@proxy.invalid:8080"},
			{"https", "https://fixture:synthetic@proxy.invalid:8080"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				store := &credentialProxyStore{proxy: &tables.GlobalProxyConfig{URL: tc.raw}}
				a := NewAdapter(nil, &lib.Config{ConfigStore: store}, nil)
				next := *store.proxy
				next.SkipTLSVerify = true
				var denial *handlers.ConsolePolicyError
				if err := a.proxyUpdate(context.Background(), &next, rbac.Access{}); !errors.As(err, &denial) || denial.Status != fasthttp.StatusForbidden {
					t.Fatal("retained URL authentication", err)
				}
				if err := a.proxyUpdate(context.Background(), &next, credentialAccess()); err != nil {
					t.Fatal(err)
				}
				next.URL = "other:synthetic@new.invalid:8080"
				if err := a.proxyUpdate(context.Background(), &next, rbac.Access{}); !errors.As(err, &denial) || denial.Status != fasthttp.StatusForbidden {
					t.Fatal("same password with new username", err)
				}
				next.URL = "fixture:new-fixture@new.invalid:8080"
				if err := a.proxyUpdate(context.Background(), &next, rbac.Access{}); err != nil {
					t.Fatal("new password", err)
				}
			})
		}
	})
	t.Run("unreadable saved proxy", func(t *testing.T) {
		store := &credentialProxyStore{proxy: &tables.GlobalProxyConfig{URL: "http://fixture:%zz@proxy.invalid"}}
		a := NewAdapter(nil, &lib.Config{ConfigStore: store}, nil)
		next := *store.proxy
		next.URL = "http://proxy.invalid"
		var denial *handlers.ConsolePolicyError
		if err := a.proxyUpdate(context.Background(), &next, rbac.Access{}); !errors.As(err, &denial) || denial.Status != fasthttp.StatusForbidden {
			t.Fatal(err)
		}
		if err := a.proxyUpdate(context.Background(), &next, credentialAccess()); err != nil {
			t.Fatal(err)
		}
	})
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
	if !errors.As(err, &denial) || denial.Status != fasthttp.StatusForbidden {
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
	t.Run("OTEL header targets", func(t *testing.T) {
		var old, next any
		if err := json.Unmarshal([]byte(`{"profiles":[{"collector_url":"https://traces.invalid","metrics_endpoint":"https://metrics.invalid","headers":{"Authorization":"fixture"},"metrics_headers":{"Authorization":"metrics-fixture"}}]}`), &old); err != nil {
			t.Fatal(err)
		}
		next = cloneCredentialConfig(t, old)
		next.(map[string]any)["profiles"].([]any)[0].(map[string]any)["metrics_headers"] = map[string]any{}
		if err := pluginDestination("otel", old, next, rbac.Access{}); !errors.Is(err, rbac.ErrForbidden) {
			t.Fatal(err)
		}
		if err := pluginDestination("otel", old, next, credentialAccess()); err != nil {
			t.Fatal(err)
		}
		if err := pluginDestination("otel", old, old, rbac.Access{}); err != nil {
			t.Fatal("no-op", err)
		}
	})
}

type credentialPluginStore struct {
	configstore.ConfigStore
	plugin *tables.TablePlugin
}

func (s *credentialPluginStore) GetPlugin(context.Context, string) (*tables.TablePlugin, error) {
	if s.plugin == nil {
		return nil, configstore.ErrNotFound
	}
	return s.plugin, nil
}

func TestPluginCredentialDestinationUpdate(t *testing.T) {
	var current map[string]any
	if err := json.Unmarshal([]byte(`{"profiles":[{"collector_url":"https://old.invalid","protocol":"http","headers":{"Authorization":"fixture"}}]}`), &current); err != nil {
		t.Fatal(err)
	}
	store := &credentialPluginStore{plugin: &tables.TablePlugin{Name: "otel", Config: current}}
	a := NewAdapter(nil, &lib.Config{ConfigStore: store}, nil)
	for _, config := range []string{`{}`, `{"plugin_span_filter":{"mode":"exclude","plugins":["logging"]}}`, `{"profiles":[{"collector_url":"https://old.invalid","protocol":"http","headers":{"Authorization":"<REDACTED>"}}]}`} {
		var c fasthttp.RequestCtx
		c.Request.Header.SetMethod(fasthttp.MethodPut)
		c.SetUserValue("name", "otel")
		c.Request.SetBodyString(`{"config":` + config + `}`)
		if err := a.preparePluginUpdate(&c, rbac.Access{}); err != nil {
			t.Fatal(config, err)
		}
		var body map[string]any
		if err := json.Unmarshal(c.PostBody(), &body); err != nil {
			t.Fatal(err)
		}
		expected, err := pluginStoredShape("otel", current)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(body["config"].(map[string]any)["profiles"], expected.(map[string]any)["profiles"]) {
			t.Fatal("partial update changed profiles")
		}
		if current["profiles"].([]any)[0].(map[string]any)["headers"].(map[string]any)["Authorization"] != "fixture" {
			t.Fatal("current config mutated")
		}
	}
	t.Run("first PUT", func(t *testing.T) {
		for _, name := range []string{"otel", "telemetry"} {
			a := NewAdapter(nil, &lib.Config{ConfigStore: &credentialPluginStore{}}, nil)
			var c fasthttp.RequestCtx
			c.Request.Header.SetMethod(fasthttp.MethodPut)
			c.SetUserValue("name", name)
			c.Request.SetBodyString(`{"config":{}}`)
			if err := a.preparePluginUpdate(&c, rbac.Access{}); err != nil {
				t.Fatal("first PUT", name, err)
			}
			c.Request.SetBodyString(`{"config":{"header":"<REDACTED>"}}`)
			if err := a.preparePluginUpdate(&c, rbac.Access{}); !errors.Is(err, rbac.ErrInvalid) {
				t.Fatal("first PUT marker", name, err)
			}
		}
	})
}
