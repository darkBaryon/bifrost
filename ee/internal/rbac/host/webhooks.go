// 本文件保护Webhook（向外部地址发送事件通知）的配置；发送日志正文需要额外权限。
package host

import (
	"context"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
)

var webhookDeliveryFields = []string{"id", "endpoint_id", "event", "request_id", "webhook_id", "async_job_id", "status", "attempt", "attempts", "attempt_count", "created_at", "updated_at", "delivered_at", "last_attempt_at", "next_attempt_at", "http_status", "latency"}

// projectWebhooks 隐藏地址凭据；没有日志正文权限时，只保留投递状态等信息。
func projectWebhooks(value any, access rbac.Access, pattern string) {
	if pattern == "/api/webhooks/{id}/test" {
		if result, ok := value.(map[string]any); ok {
			safe := keep(result, []string{"delivered", "receiver_status_code"})
			if _, failed := result["error"]; failed {
				safe["error"] = "delivery_failed"
			}
			clear(result)
			for key, value := range safe {
				result[key] = value
			}
		}
		return
	}

	objects(value, []string{"endpoints", "endpoint", "webhooks", "webhook"}, func(v object) { maskURL(v, "url") })
	if access.Allows(rbac.LogsRevealContent) {
		return
	}
	objects(value, []string{"result", "results"}, func(v object) {
		if rows, ok := v["deliveries"].([]any); ok {
			out := make([]any, 0, len(rows))
			for _, row := range rows {
				if m, ok := row.(map[string]any); ok {
					out = append(out, keep(m, webhookDeliveryFields))
				}
			}
			v["deliveries"] = out
		}
		if row, ok := v["delivery"].(map[string]any); ok {
			v["delivery"] = keep(row, webhookDeliveryFields)
		}
	})
}

// webhookUpdate 保存或重新投递前检查权限；更新时把隐藏地址换回原值。
func (a *Adapter) webhookUpdate(ctx context.Context, subject rbac.Subject, op handlers.ConsoleWebhookOperation, desired *tables.TableWebhookEndpoint) error {
	if desired == nil {
		return consoleError(rbac.ErrInvalid)
	}
	access, e := a.service.Snapshot(ctx, subject)
	if e != nil {
		return consoleError(e)
	}
	if !access.Allows(rbac.NotificationsManage) || (desired.IncludeResponse && !access.Allows(rbac.LogsRevealContent)) {
		return consoleError(rbac.ErrForbidden)
	}
	if op == handlers.ConsoleWebhookRedeliver {
		return nil
	}
	if op == handlers.ConsoleWebhookCreate {
		if desired.URL == redacted {
			return consoleError(rbac.ErrInvalid)
		}
		return nil
	}
	if a.config == nil {
		return consoleError(rbac.ErrUnavailable)
	}
	current, ok := a.config.WebhookEndpointByID(desired.ID)
	if !ok || current == nil {
		return consoleError(rbac.ErrNotFound)
	}
	if desired.URL == redacted {
		if current.URL == "" || current.URL == redacted {
			return consoleError(rbac.ErrInvalid)
		}
		desired.URL = current.URL
	}
	// 签名密钥只用来计算签名，不随请求发送；需要保护的是会实际发送的自定义认证头。
	if err := credentialDestination(access, retainedValues(secretHeaderValues(current.Headers), secretHeaderValues(desired.Headers)), current.URL, desired.URL); err != nil {
		return consoleError(err)
	}

	return nil
}
