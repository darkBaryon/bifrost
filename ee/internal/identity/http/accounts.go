// 本文件是账号线端点：初始化、建号、列表与启停。
package identityhttp

import (
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/valyala/fasthttp"
)

func (h *Handler) initialize(r request) (int, any, error) {
	var q initializeRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	a, err := h.svc.Account.Initialize(r.ctx, q.SetupToken, q.Username, q.Password)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusCreated, accountResponse{Account: toAccountDTO(a)}, nil
}

func (h *Handler) createAccount(r request) (int, any, error) {
	var q createAccountRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	a, err := h.svc.Account.CreateAccount(r.ctx, r.principal, q.Username, q.DisplayName)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusCreated, accountResponse{Account: toAccountDTO(a)}, nil
}

func (h *Handler) listAccounts(r request) (int, any, error) {
	var q listRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	p, err := h.svc.Account.ListAccounts(r.ctx, r.principal, q.Cursor, pageLimit(q.Limit))
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, toAccountPage(p), nil
}

func (h *Handler) setStatus(r request) (int, any, error) {
	var q setStatusRequest
	if err := r.decode(&q); err != nil {
		return 0, nil, err
	}
	a, err := h.svc.Account.SetAccountStatus(r.ctx, r.principal, q.AccountID, identity.AccountStatus(q.Status))
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, accountResponse{Account: toAccountDTO(a)}, nil
}
