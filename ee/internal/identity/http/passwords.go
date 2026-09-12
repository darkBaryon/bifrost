// 本文件是密码线端点：本人改密、管理员重置与密码事件查询。
package identityhttp

import (
	"github.com/valyala/fasthttp"
)

// changePassword 成功后清除 Cookie，调用方须重新登录。
func (h *Handler) changePassword(r request) (int, any, error) {
	var q changePasswordRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	if err := h.svc.Password.ChangePassword(r.ctx, r.principal, q.OldPassword, q.NewPassword); err != nil {
		return 0, nil, err
	}
	h.clearCookie(r.c)
	return fasthttp.StatusOK, messageResponse{Message: messagePasswordChanged}, nil
}

func (h *Handler) resetPassword(r request) (int, any, error) {
	var q resetPasswordRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	e, err := h.svc.Password.ResetPassword(r.ctx, r.principal, q.AccountID, q.OperationID)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, resetPasswordResponse{EventID: e.ID, Result: string(e.Result)}, nil
}

func (h *Handler) passwordEvents(r request) (int, any, error) {
	var q eventsRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	p, err := h.svc.Password.ListPasswordEvents(r.ctx, r.principal, q.TargetID, q.Cursor, pageLimit(q.Limit))
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, toEventPage(p), nil
}
