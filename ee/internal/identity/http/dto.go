// 本文件定义身份接口的请求与响应结构并做业务类型转换，按 共同/会话/账号/密码 四段排列；业务类型本身不参与序列化。
package identityhttp

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
)

// ---- 共同：错误体、消息体、分页壳与空请求 ----
// 请求结构：未知字段一律拒绝；limit 用指针区分省略（取默认）与显式 0（无效）。

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type messageResponse struct {
	Message string `json:"message"`
}

// pageResponse 的 next_cursor 在没有更多时为 null。
type pageResponse[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type emptyRequest struct{}

func pageLimit(v *int) int {
	if v == nil {
		return identity.DefaultPageSize
	}
	return *v
}

// ---- 会话线 ----

// statusResponse 同时服务新旧登录页：has_valid_token 与 is_auth_enabled 是旧字段。
type statusResponse struct {
	Initialized        bool       `json:"initialized"`
	AuthType           string     `json:"auth_type"`
	IsAuthEnabled      bool       `json:"is_auth_enabled"`
	HasValidToken      bool       `json:"has_valid_token"`
	HasValidSession    bool       `json:"has_valid_session"`
	MustChangePassword bool       `json:"must_change_password"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

type loginResponse struct {
	Message            string     `json:"message"`
	Account            accountDTO `json:"account"`
	MustChangePassword bool       `json:"must_change_password"`
	ExpiresAt          time.Time  `json:"expires_at"`
}

type ticketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// ---- 账号线 ----

// accountDTO 是账号的对外字段，不含密码、哈希或版本。
type accountDTO struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"display_name"`
	Status             string     `json:"status"`
	MustChangePassword bool       `json:"must_change_password"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

func toAccountDTO(a identity.Account) accountDTO {
	return accountDTO{ID: a.ID, Username: a.Username, DisplayName: a.DisplayName, Status: string(a.Status), MustChangePassword: a.MustChangePassword, LastLoginAt: a.LastLoginAt}
}

type accountResponse struct {
	Account accountDTO `json:"account"`
}

func toAccountPage(p identity.AccountPage) pageResponse[accountDTO] {
	items := make([]accountDTO, 0, len(p.Items))
	for _, a := range p.Items {
		items = append(items, toAccountDTO(a))
	}
	return pageResponse[accountDTO]{Items: items, NextCursor: optional(p.NextCursor)}
}

type initializeRequest struct {
	SetupToken string `json:"setup_token"`
	Username   string `json:"username"`
	Password   string `json:"password"`
}

type createAccountRequest struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type listRequest struct {
	Cursor string `json:"cursor"`
	Limit  *int   `json:"limit"`
}

type setStatusRequest struct {
	AccountID string `json:"account_id"`
	Status    string `json:"status"`
}

// ---- 密码线 ----

type eventDTO struct {
	ID          string    `json:"id"`
	OperationID string    `json:"operation_id"`
	ActorID     string    `json:"actor_id"`
	TargetID    string    `json:"target_id"`
	ActorName   string    `json:"actor_name"`
	TargetName  string    `json:"target_name"`
	Action      string    `json:"action"`
	Result      string    `json:"result"`
	ReasonCode  string    `json:"reason_code"`
	OccurredAt  time.Time `json:"occurred_at"`
}

func toEventDTO(e identity.PasswordEvent) eventDTO {
	return eventDTO{ID: e.ID, OperationID: e.OperationID, ActorID: e.ActorID, TargetID: e.TargetID, ActorName: e.ActorName,
		TargetName: e.TargetName, Action: string(e.Action), Result: string(e.Result), ReasonCode: e.ReasonCode, OccurredAt: e.OccurredAt}
}

func toEventPage(p identity.EventPage) pageResponse[eventDTO] {
	items := make([]eventDTO, 0, len(p.Items))
	for _, e := range p.Items {
		items = append(items, toEventDTO(e))
	}
	return pageResponse[eventDTO]{Items: items, NextCursor: optional(p.NextCursor)}
}

type resetPasswordResponse struct {
	EventID string `json:"event_id"`
	Result  string `json:"result"`
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

type resetPasswordRequest struct {
	AccountID   string `json:"account_id"`
	OperationID string `json:"operation_id"`
}

type eventsRequest struct {
	TargetID string `json:"target_id"`
	Cursor   string `json:"cursor"`
	Limit    *int   `json:"limit"`
}

// deleteAccountRequest 只接收账号编号；删除权限及保护规则由服务检查。
type deleteAccountRequest struct {
	AccountID string `json:"account_id"`
}
