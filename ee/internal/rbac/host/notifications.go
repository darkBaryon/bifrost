// 本文件按账号角色筛选通知；长连接每次发送消息时都重新检查权限。
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
)

type notificationPolicy struct {
	service *rbac.Service
	subject rbac.Subject
	access  rbac.Access
}

// visible 检查通知是否发给所有人，或发给当前账号拥有的角色。
func visible(access rbac.Access, n *schemas.Notification) bool {
	if n == nil || !access.Allows(rbac.NotificationsView) {
		return false
	}
	if access.Chief {
		return true
	}
	if n.Audience == schemas.NotificationAudienceAll {
		return true
	}
	if n.Audience != schemas.NotificationAudienceRoles {
		return false
	}
	for _, id := range n.RoleIDs {
		if slices.Contains(access.RoleIDs, rbac.RoleID(id)) {
			return true
		}
	}
	return false
}

func (p notificationPolicy) CanView(n *schemas.Notification) bool { return visible(p.access, n) }

// AuthorizePublish 检查发布权限和角色是否存在；通知格式已由原handler校验并规范化。
func (p notificationPolicy) AuthorizePublish(ctx context.Context, input schemas.NotificationInput) error {
	ids := []rbac.RoleID{}
	for _, id := range input.RoleIDs {
		ids = append(ids, rbac.RoleID(id))
	}
	return consoleError(p.service.ValidateNotificationAudience(ctx, p.subject, ids))
}

// messageFilter 每次发送前重新读取账号权限；无权接收的通知跳过，登录或权限失效则断开。
func (a *Adapter) messageFilter(subject rbac.Subject) handlers.WebSocketMessageFilter {
	return func(ctx context.Context, body []byte) (allowed bool, err error) {
		ctx = identity.WithDiagnosticOperation(ctx, "rbac.websocket")
		defer func() {
			if err != nil {
				a.diagnostic(ctx, "/ws", "message_rejected")
			}
		}()

		access, e := a.service.Snapshot(ctx, subject)
		if e != nil {
			return false, e
		}
		if !access.Allows(rbac.NotificationsView) {
			return false, rbac.ErrForbidden
		}
		if authhttp.ValidateJSONObject(body) != nil {
			return false, rbac.ErrInvalid
		}
		var envelope struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(body, &envelope) != nil || envelope.Type == "" {
			return false, rbac.ErrInvalid
		}
		switch envelope.Type {
		case "heartbeat":
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&envelope) != nil || (len(envelope.Data) > 0 && string(envelope.Data) != "null") {
				return false, rbac.ErrInvalid
			}
			return true, nil
		case "notification":
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&envelope) != nil {
				return false, rbac.ErrInvalid
			}
			var n schemas.Notification
			decoder = json.NewDecoder(bytes.NewReader(envelope.Data))
			decoder.DisallowUnknownFields()
			if decoder.Decode(&n) != nil || n.ID == "" || (n.Audience != schemas.NotificationAudienceAll && n.Audience != schemas.NotificationAudienceRoles) {
				return false, rbac.ErrInvalid
			}
			return visible(access, &n), nil
		default:
			a.diagnostic(ctx, "/ws", "unknown_message_type")
			return false, nil
		}
	}
}
