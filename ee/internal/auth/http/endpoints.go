// 本文件处理状态查询、登录、登出、本人资料与 WebSocket 票据请求。
package authhttp

import (
	"errors"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/valyala/fasthttp"
)

// status 供登录页判断是否初始化及当前 Cookie 是否有效；没有会话时不暴露账号信息。
func (h *Handler) status(r request) (int, any, error) {
	if !r.c.IsGet() {
		if err := r.decode(&emptyRequest{}); err != nil {
			return 0, nil, err
		}
	}
	initialized, err := h.isInitialized(r.ctx)
	if err != nil {
		return 0, nil, err
	}
	p, err := h.authentication.Authenticate(r.ctx, r.cookie())
	if err != nil && !errors.Is(err, auth.ErrUnauthorized) {
		return 0, nil, err
	}
	valid := err == nil
	return fasthttp.StatusOK, statusResponse{Initialized: initialized, AuthType: authType, IsAuthEnabled: true,
		HasValidToken: valid, HasValidSession: valid, MustChangePassword: valid && p.MustChangePassword}, nil
}

// login 用真实 TCP 来源地址参与限流，不信任代理头；成功后只通过 Cookie 交付 token。
func (h *Handler) login(r request) (int, any, error) {
	var q loginRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	v, err := h.authentication.Login(r.ctx, q.Username, q.Password, r.c.RemoteIP().String())
	if err != nil {
		return 0, nil, err
	}
	h.browser.SetCookie(r.c, v.Token, v.ExpiresAt)
	return fasthttp.StatusOK, loginResponse{Message: messageLoginSuccessful, Account: toAccountDTO(v.Account),
		MustChangePassword: v.Principal.MustChangePassword, ExpiresAt: v.ExpiresAt}, nil
}

// logout 无论 Cookie 是否有效都清除 Cookie；存储故障仍返回错误。
func (h *Handler) logout(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	if err := h.authentication.Logout(r.ctx, r.cookie()); err != nil {
		return 0, nil, err
	}
	h.browser.ClearCookie(r.c)
	return fasthttp.StatusOK, messageResponse{Message: messageLogoutSuccessful}, nil
}

func (h *Handler) me(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	a, err := h.authentication.Me(r.ctx, r.principal)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, accountResponse{Account: toAccountDTO(a)}, nil
}

func (h *Handler) wsTicket(r request) (int, any, error) {
	if err := r.decode(&emptyRequest{}); err != nil {
		return 0, nil, err
	}
	ticket, err := h.tickets.IssueTicket(r.ctx, r.principal)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, ticketResponse{Ticket: ticket, ExpiresIn: int(session.TicketTTL / time.Second)}, nil
}
