// 本文件统一RBAC业务错误到HTTP状态的映射与安全输出，同时保留宿主错误状态。
package rbachttp

import (
	"errors"

	authhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/valyala/fasthttp"
)

var errorStatuses = []struct {
	error  rbac.Error
	status int
}{
	{rbac.ErrInvalid, fasthttp.StatusBadRequest},
	{rbac.ErrUnauthorized, fasthttp.StatusUnauthorized},
	{rbac.ErrForbidden, fasthttp.StatusForbidden},
	{rbac.ErrNotFound, fasthttp.StatusNotFound},
	{rbac.ErrConflict, fasthttp.StatusConflict},
	{rbac.ErrUnavailable, fasthttp.StatusServiceUnavailable},
}

const methodNotAllowed = "method_not_allowed"

type statusError struct {
	status int
	code   string
}

func (e statusError) Error() string { return e.code }

// Status 返回业务错误的安全HTTP状态；未分类故障关闭为503。
func Status(err error) int {
	_, status := classifiedError(err)
	return status
}

func classifiedError(err error) (rbac.Error, int) {
	for _, pair := range errorStatuses {
		if errors.Is(err, pair.error) {
			return pair.error, pair.status
		}
	}
	return rbac.ErrUnavailable, fasthttp.StatusServiceUnavailable
}

// Error 统一输出权限业务错误；冲突保留关联计数，宿主错误保留原状态，内部原文不外泄。
func Error(c *fasthttp.RequestCtx, err error) {
	var hostStatus statusError
	if errors.As(err, &hostStatus) {
		authhttp.JSON(c, hostStatus.status, errorResponse{Error: errorDetail{Code: hostStatus.code, Message: hostStatus.code}})
		return
	}
	code, status := classifiedError(err)
	detail := errorDetail{Code: string(code), Message: string(code)}
	var conflict *rbac.ConflictError
	if errors.As(err, &conflict) {
		detail.Details = &conflictDetails{AccountCount: conflict.AccountCount}
	}
	authhttp.JSON(c, status, errorResponse{Error: detail})
}

// FromStatus 保留宿主错误状态，错误正文只使用明确的安全码。
func FromStatus(status int) error {
	if status == fasthttp.StatusMethodNotAllowed {
		return statusError{status, methodNotAllowed}
	}
	for _, pair := range errorStatuses {
		if status == pair.status {
			return statusError{status, string(pair.error)}
		}
	}
	return statusError{status, string(rbac.ErrUnavailable)}
}

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Details *conflictDetails `json:"details,omitempty"`
}

type conflictDetails struct {
	AccountCount int `json:"account_count"`
}
