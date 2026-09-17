// 本文件分派各类请求检查，并让原handler在保存或发送前调用对应检查。
package host

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/valyala/fasthttp"
)

// requirePermissions 检查这次权限结果是否包含所有指定权限；缺少任意一个就拒绝。
func requirePermissions(access rbac.Access, codes ...rbac.Permission) error {
	for _, p := range codes {
		if !access.Allows(p) {
			return rbac.ErrForbidden
		}
	}
	return nil
}

// strictJSON 拒绝重复JSON字段和安全字段的大小写变体，避免权限检查与业务解析读到不同内容。
func strictJSON(body []byte) error {
	if authhttp.ValidateJSONObject(body) != nil {
		return rbac.ErrInvalid
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return rbac.ErrInvalid
	}
	names := []string{"name", "path", "config", "network_config", "extra_headers", "base_url", "include_response", "client_config", "framework_config", "auth_config", "permission_codes", "role_ids", "audience", "headers", "url", "disable_content_logging", "retain_content_in_object_storage", "request_headers", "logging_headers"}
	var walk func(any) error
	walk = func(v any) error {
		switch v := v.(type) {
		case map[string]any:
			for key, child := range v {
				lower := strings.ToLower(key)
				for _, known := range names {
					if lower == known && key != known {
						return rbac.ErrInvalid
					}
				}
				switch key {
				case "headers", "extra_headers", "trace_headers", "metrics_headers", "metadata", "additional_attributes", "envs":
					continue // 任意键的配置映射不是可大小写折叠的Go结构字段。
				}
				if e := walk(child); e != nil {
					return e
				}
			}
		case []any:
			for _, child := range v {
				if e := walk(child); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return walk(value)
}

// queryBool 只接受单个true或false；重复参数也报错，防止检查与实际执行读到不同值。
func queryBool(c *fasthttp.RequestCtx, key string) (bool, error) {
	all := c.QueryArgs().PeekMulti(key)
	if len(all) > 1 {
		return false, rbac.ErrInvalid
	}
	if len(all) == 0 {
		return false, nil
	}
	switch string(all[0]) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, rbac.ErrInvalid
	}
}

var jsonInputGuards = []guard{guardProviderInput, guardPluginMutation, guardSettingsInput, guardWebhookInput, guardNotificationPolicy}

// checkSensitiveRequest 在原handler执行前检查特殊参数，例如导出虚拟密钥需要额外的明文读取权限。
func (a *Adapter) checkSensitiveRequest(c *fasthttp.RequestCtx, r requestAccess) error {
	if slices.Contains(jsonInputGuards, r.Route.Guard) && len(c.PostBody()) > 0 {
		if err := strictJSON(c.PostBody()); err != nil {
			return err
		}
	}
	switch r.Route.Guard {
	case guardVkExport:
		enabled, err := queryBool(c, "export")
		if err != nil {
			return err
		}
		if enabled {
			return requirePermissions(r.Access, rbac.VirtualKeysRevealKey)
		}
	case guardLogsQuery:
		return checkLogQuery(c, r)
	case guardProviderInput:
		return checkProviderInput(c, r)
	case guardPluginMutation:
		if err := checkPluginMutation(c, r.Access); err != nil {
			return err
		}
		return a.restorePlugin(c)
	case guardSettingsInput:
		if r.Route.Pattern == "/api/config" {
			return checkSettingsInput(c)
		}
	}
	return nil
}

// consoleError 将EE错误转换成Bifrost扩展点认识的HTTP错误，不传递内部错误原文。
func consoleError(err error) error {
	if err == nil {
		return nil
	}
	return &handlers.ConsolePolicyError{Status: rbachttp.Status(err)}
}

// installPolicies 把本次请求需要的检查传给原handler，包括保存配置、发布通知和长连接发消息。
func (a *Adapter) installPolicies(c *fasthttp.RequestCtx, r requestAccess) {
	subject := r.Subject
	c.SetUserValue(handlers.ConsoleNotificationPolicyContextKey, notificationPolicy{a.service, subject, r.Access})
	if r.Access.Chief {
		c.SetUserValue(schemas.IsLocalAdminContextKey, true)
	}
	if r.Route.Guard == guardWebsocket {
		c.SetUserValue(handlers.WebSocketAuthorizeContextKey, func(ctx context.Context) error { return a.service.Authorize(ctx, subject, rbac.NotificationsView) })
		c.SetUserValue(handlers.WebSocketMessageFilterContextKey, a.messageFilter(subject))
	}
	c.SetUserValue(handlers.ConsoleProviderUpdatePolicyContextKey, handlers.ConsoleProviderUpdatePolicy(restoreProvider))
	c.SetUserValue(handlers.ConsoleSettingsUpdatePolicyContextKey, handlers.ConsoleSettingsUpdatePolicy(func(ctx context.Context, desired *handlers.ConsoleSettingsUpdate) error {
		return consoleError(a.settingsUpdate(ctx, desired, r.Access))
	}))
	c.SetUserValue(handlers.ConsoleProxyUpdatePolicyContextKey, handlers.ConsoleProxyUpdatePolicy(a.proxyUpdate))
	c.SetUserValue(handlers.ConsoleWebhookPolicyContextKey, handlers.ConsoleWebhookPolicy(func(ctx context.Context, op handlers.ConsoleWebhookOperation, endpoint *tables.TableWebhookEndpoint) error {
		return a.webhookUpdate(ctx, subject, op, endpoint)
	}))
}
