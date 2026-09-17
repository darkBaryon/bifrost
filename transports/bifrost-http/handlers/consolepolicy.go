// 本文件提供可选控制台策略接缝，未注入时保持宿主原有行为。
package handlers

import (
	"context"
	"errors"
	"reflect"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/valyala/fasthttp"
)

type consolePolicyKey string

const (
	ConsoleNotificationPolicyContextKey   consolePolicyKey = "console-notification-policy"
	WebSocketMessageFilterContextKey      consolePolicyKey = "console-message-filter"
	ConsoleProviderUpdatePolicyContextKey consolePolicyKey = "console-provider-update"
	ConsoleSettingsUpdatePolicyContextKey consolePolicyKey = "console-settings-update"
	ConsoleProxyUpdatePolicyContextKey    consolePolicyKey = "console-proxy-update"
	ConsoleWebhookPolicyContextKey        consolePolicyKey = "console-webhook-operation"
)

// ConsoleNotificationPolicy 控制发布及当前请求快照中的多角色可见性。
type ConsoleNotificationPolicy interface {
	// AuthorizePublish receives input already validated and normalized by the notification handler.
	AuthorizePublish(context.Context, schemas.NotificationInput) error
	CanView(*schemas.Notification) bool
}

// WebSocketMessageFilter 在每条消息的写锁内判定发送、跳过或关闭连接。
type WebSocketMessageFilter func(context.Context, []byte) (bool, error)

// ConsoleProviderUpdatePolicy 在验证或写入前恢复完整请求里的保留标记。
type ConsoleProviderUpdatePolicy func(context.Context, *configstore.ProviderConfig, *schemas.NetworkConfig, *schemas.ProxyConfig) error

// ConsoleSettingsUpdate 是请求独享的期望配置，策略可恢复保留字段。
type ConsoleSettingsUpdate struct {
	Client    *configstore.ClientConfig
	Framework *tables.TableFrameworkConfig
}

// ConsoleSettingsUpdatePolicy 在配置的任何副作用前执行。
type ConsoleSettingsUpdatePolicy func(context.Context, *ConsoleSettingsUpdate) error

// ConsoleProxyUpdatePolicy 在代理配置校验与保存前执行。
type ConsoleProxyUpdatePolicy func(context.Context, *tables.GlobalProxyConfig) error

// ConsoleWebhookOperation 是Webhook策略允许识别的操作。
type ConsoleWebhookOperation string

const (
	ConsoleWebhookCreate    ConsoleWebhookOperation = "create"
	ConsoleWebhookUpdate    ConsoleWebhookOperation = "update"
	ConsoleWebhookRedeliver ConsoleWebhookOperation = "redeliver"
)

// ConsoleWebhookPolicy 处理期望端点，重投传入的有效端点必须只读。
type ConsoleWebhookPolicy func(context.Context, ConsoleWebhookOperation, *tables.TableWebhookEndpoint) error

// ConsolePolicyError 是策略允许公开的固定错误类别，底层错误不得透传。
type ConsolePolicyError struct {
	Status int
}

func (e *ConsolePolicyError) Error() string { return "console policy rejected operation" }
func sendConsolePolicyError(ctx *fasthttp.RequestCtx, e error) {
	status, code := fasthttp.StatusServiceUnavailable, "unavailable"
	var safe *ConsolePolicyError
	if errors.As(e, &safe) && safe != nil {
		switch safe.Status {
		case fasthttp.StatusBadRequest:
			status, code = fasthttp.StatusBadRequest, "invalid_input"
		case fasthttp.StatusUnauthorized:
			status, code = fasthttp.StatusUnauthorized, "unauthorized"
		case fasthttp.StatusForbidden:
			status, code = fasthttp.StatusForbidden, "forbidden"
		case fasthttp.StatusNotFound:
			status, code = fasthttp.StatusNotFound, "not_found"
		case fasthttp.StatusConflict:
			status, code = fasthttp.StatusConflict, "conflict"
		}
	}
	SendJSONWithStatus(ctx, map[string]any{"error": map[string]string{"code": code, "message": code}}, status)
}
func consolePolicy[T any](ctx *fasthttp.RequestCtx, key consolePolicyKey) (result T, present bool, valid bool) {
	var zero T
	raw := ctx.UserValue(key)
	if raw == nil {
		return zero, false, true
	}
	policy, ok := raw.(T)
	value := reflect.ValueOf(policy)
	typedNil := value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Func || value.Kind() == reflect.Interface) && value.IsNil()
	if !ok || typedNil {
		sendConsolePolicyError(ctx, nil)
		return zero, true, false
	}
	return policy, true, true
}
