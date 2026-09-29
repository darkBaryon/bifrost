// 本文件定义认证接口请求、响应及安全的账号资料投影；业务模型不直接序列化。
package authhttp

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
)

const (
	authType                = "password" // 沿用控制台状态响应的认证类型。
	messageLoginSuccessful  = "Login successful"
	messageLogoutSuccessful = "Logout successful"
)

type emptyRequest struct{}

type messageResponse struct {
	Message string `json:"message"`
}

// statusResponse 同时服务新旧登录页，保留旧字段 has_valid_token 与 is_auth_enabled。
type statusResponse struct {
	Initialized        bool   `json:"initialized"`
	AuthType           string `json:"auth_type"`
	IsAuthEnabled      bool   `json:"is_auth_enabled"`
	HasValidToken      bool   `json:"has_valid_token"`
	HasValidSession    bool   `json:"has_valid_session"`
	MustChangePassword bool   `json:"must_change_password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Message            string     `json:"message"`
	Account            accountDTO `json:"account"`
	MustChangePassword bool       `json:"must_change_password"`
	ExpiresAt          time.Time  `json:"expires_at"`
}

type accountResponse struct {
	Account accountDTO `json:"account"`
}

// accountDTO 只公开控制台需要的资料，不包含密码、认证版本、会话编号或 token。
type accountDTO struct {
	ID                 string     `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"display_name"`
	Status             string     `json:"status"`
	MustChangePassword bool       `json:"must_change_password"`
	LastLoginAt        *time.Time `json:"last_login_at"`
}

func toAccountDTO(a auth.User) accountDTO {
	return accountDTO{ID: a.ID, Username: a.Username, DisplayName: a.DisplayName,
		Status: string(a.Status), MustChangePassword: a.MustChangePassword, LastLoginAt: a.LastLoginAt}
}

type ticketResponse struct {
	Ticket    string `json:"ticket"`
	ExpiresIn int    `json:"expires_in"`
}
