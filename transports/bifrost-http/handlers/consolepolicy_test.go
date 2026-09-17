// 本文件验证可选策略的错误隔离、发布前拒绝及消息写锁内过滤。
package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/websocket"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/valyala/fasthttp"
)

type testConsoleNotificationPolicy struct {
	err   error
	calls int
}

func (p *testConsoleNotificationPolicy) AuthorizePublish(context.Context, schemas.NotificationInput) error {
	p.calls++
	return p.err
}
func (p *testConsoleNotificationPolicy) CanView(*schemas.Notification) bool { return true }
func TestConsolePolicyErrorIsSafe(t *testing.T) {
	for _, e := range []error{errors.New("secret-driver-error"), &ConsolePolicyError{Status: 299}, (*ConsolePolicyError)(nil)} {
		c := &fasthttp.RequestCtx{}
		sendConsolePolicyError(c, e)
		if c.Response.StatusCode() != fasthttp.StatusServiceUnavailable {
			t.Fatal(c.Response.StatusCode())
		}
	}
	for _, status := range []int{fasthttp.StatusBadRequest, fasthttp.StatusUnauthorized, fasthttp.StatusForbidden, fasthttp.StatusNotFound, fasthttp.StatusConflict, fasthttp.StatusServiceUnavailable} {
		c := &fasthttp.RequestCtx{}
		sendConsolePolicyError(c, &ConsolePolicyError{Status: status})
		if c.Response.StatusCode() != status {
			t.Fatal(status)
		}
	}
}
func TestConsolePolicyNotificationRejectsBeforeStore(t *testing.T) {
	store := &recordingNotificationStore{}
	service := &NotificationService{store: store, now: func() time.Time { return time.Now().UTC() }}
	c := &fasthttp.RequestCtx{}
	c.Request.SetBodyString(`{"title":"title","message":"message","severity":"info","audience":"all"}`)
	policy := &testConsoleNotificationPolicy{err: &ConsolePolicyError{Status: fasthttp.StatusForbidden}}
	c.SetUserValue(ConsoleNotificationPolicyContextKey, policy)
	service.create(c)
	if policy.calls != 1 || len(store.created) != 0 || c.Response.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("calls=%d rows=%d status=%d", policy.calls, len(store.created), c.Response.StatusCode())
	}
	policy.err = nil
	service.create(c)
	if len(store.created) != 1 || c.Response.StatusCode() != 201 {
		t.Fatal("policy could not publish without localAdmin")
	}
}
func TestConsolePolicyWrongTypeDoesNotFallback(t *testing.T) {
	for _, raw := range []any{"bad", (*testConsoleNotificationPolicy)(nil)} {
		c := &fasthttp.RequestCtx{}
		c.SetUserValue(ConsoleNotificationPolicyContextKey, raw)
		c.SetUserValue(schemas.IsLocalAdminContextKey, true)
		(&NotificationService{}).list(c)
		if c.Response.StatusCode() != fasthttp.StatusServiceUnavailable {
			t.Fatal("invalid policy fell back", c.Response.StatusCode())
		}
	}
}
func TestConsolePolicyWebSocketFilterUsesWriteLock(t *testing.T) {
	h, addr, stop := startWebSocketTestServer(t)
	defer stop()
	ws := dialWebSocket(t, addr)
	defer ws.Close()
	waitForClientCount(t, h, 1)
	client := snapshotClients(h)[0]
	calls := 0
	client.mu.Lock()
	client.filter = func(context.Context, []byte) (bool, error) {
		calls++
		if client.mu.TryLock() {
			client.mu.Unlock()
			t.Error("filter outside write lock")
		}
		return false, nil
	}
	client.mu.Unlock()
	if e := h.sendMessageSafely(client, websocket.TextMessage, []byte(`{"type":"store_update"}`)); e != nil {
		t.Fatal(e)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	client.mu.Lock()
	client.filter = func(context.Context, []byte) (bool, error) { return false, errors.New("revoked") }
	client.mu.Unlock()
	if e := h.sendMessageSafely(client, websocket.TextMessage, []byte(`{"type":"notification"}`)); e == nil {
		t.Fatal("filter failure did not close")
	}
}

// 未注入时允许原handler继续；错误类型或空回调必须拒绝，不能当作没有配置。
func TestProviderPolicyOptionalAndInvalid(t *testing.T) {
	var typedNil ConsoleProviderUpdatePolicy
	valid := ConsoleProviderUpdatePolicy(func(context.Context, *configstore.ProviderConfig, *schemas.NetworkConfig, *schemas.ProxyConfig) error {
		return nil
	})
	for _, tt := range []struct {
		name           string
		value          any
		present, valid bool
	}{
		{"absent", nil, false, true}, {"wrong-type", "invalid", true, false}, {"typed-nil", typedNil, true, false}, {"configured", valid, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := &fasthttp.RequestCtx{}
			if tt.value != nil {
				c.SetUserValue(ConsoleProviderUpdatePolicyContextKey, tt.value)
			}
			_, present, ok := consolePolicy[ConsoleProviderUpdatePolicy](c, ConsoleProviderUpdatePolicyContextKey)
			if present != tt.present || ok != tt.valid {
				t.Fatalf("present=%v valid=%v", present, ok)
			}
			if !ok && c.Response.StatusCode() != fasthttp.StatusServiceUnavailable {
				t.Fatal("invalid policy did not fail closed")
			}
		})
	}
}

func TestProviderPolicyErrorsHideInternalText(t *testing.T) {
	for _, tt := range []struct {
		err    error
		status int
	}{
		{errors.New("private-credential"), fasthttp.StatusServiceUnavailable}, {&ConsolePolicyError{Status: fasthttp.StatusBadRequest}, fasthttp.StatusBadRequest},
	} {
		c := &fasthttp.RequestCtx{}
		sendConsolePolicyError(c, tt.err)
		if c.Response.StatusCode() != tt.status || strings.Contains(string(c.Response.Body()), "private-credential") {
			t.Fatal("unsafe policy error")
		}
	}
}

// Destination denial precedes even the temporary network verification, not just persistence.
func TestMCPDestinationPolicyBeforeVerification(t *testing.T) {
	SetLogger(&mockLogger{})
	old := &schemas.MCPClientConfig{ID: "client", Name: "Fixture", ConnectionType: schemas.MCPConnectionTypeHTTP, AuthType: schemas.MCPAuthTypeHeaders, Headers: map[string]schemas.SecretVar{"Authorization": *schemas.NewSecretVar("fixture")}}
	cfg := &mockUpdateConfigStore{}
	store := &lib.Config{ConfigStore: cfg, ClientConfig: &configstore.ClientConfig{}, MCPConfig: &schemas.MCPConfig{ClientConfigs: []*schemas.MCPClientConfig{old}}}
	manager := &fakeMCPManagerVerifyOnly{}
	handler := &MCPHandler{store: store, mcpManager: manager}
	c := &fasthttp.RequestCtx{}
	c.SetUserValue("id", old.ID)
	c.Request.SetBodyString(`{"headers":{"Authorization":"new-fixture"},"tls_config":{"insecure_skip_verify":true}}`)
	called := false
	c.SetUserValue(ConsoleMCPUpdatePolicyContextKey, ConsoleMCPUpdatePolicy(func(_ context.Context, before, after *schemas.MCPClientConfig, _ *tables.TableOauthConfig, _ *configstore.MCPOAuthConfigFields) error {
		called = true
		if after.TLSConfig == nil || !after.TLSConfig.InsecureSkipVerify || after.Headers["Authorization"].Val != "new-fixture" {
			t.Fatal("not resolved")
		}
		return &ConsolePolicyError{Status: fasthttp.StatusForbidden}
	}))
	handler.updateMCPClient(c)
	if !called || c.Response.StatusCode() != 403 || manager.verifyCalls != 0 || manager.updateCalls != 0 || cfg.updates != 0 {
		t.Fatal("denial reached side effects", c.Response.StatusCode(), string(c.Response.Body()))
	}
}
