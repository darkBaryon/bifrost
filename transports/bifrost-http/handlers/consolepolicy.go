// 本文件提供可选控制台策略接缝，未注入时保持宿主原有行为。
package handlers

import (
	"context"
	"errors"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/valyala/fasthttp"
	"reflect"
)

type consolePolicyKey string

const ConsoleProviderUpdatePolicyContextKey consolePolicyKey = "console-provider-update"

// ConsoleProviderUpdatePolicy 在校验和保存厂商配置前调用，允许EE保留已隐藏字段的原值。
// 未设置此回调时，厂商更新沿用原有行为。
type ConsoleProviderUpdatePolicy func(context.Context, *configstore.ProviderConfig, *schemas.NetworkConfig, *schemas.ProxyConfig) error

// ConsolePolicyError 是策略允许公开的固定错误类别，底层错误不得透传。
type ConsolePolicyError struct {
	Status        int
	Code, Message string
}

func (e *ConsolePolicyError) Error() string { return "console policy rejected operation" }
func sendConsolePolicyError(ctx *fasthttp.RequestCtx, e error) {
	status, code := 503, "unavailable"
	var safe *ConsolePolicyError
	if errors.As(e, &safe) && safe != nil {
		switch safe.Status {
		case 400:
			status, code = 400, "invalid_input"
		case 401:
			status, code = 401, "unauthorized"
		case 403:
			status, code = 403, "forbidden"
		case 404:
			status, code = 404, "not_found"
		case 409:
			status, code = 409, "conflict"
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
