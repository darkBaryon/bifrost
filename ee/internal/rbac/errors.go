// 本文件定义角色权限操作会返回哪些错误，以及如何隐藏数据库内部错误信息。
package rbac

import "errors"

// Error 是可以直接返回给调用方的错误编号，例如 forbidden 表示没有操作权限。
type Error string

// Error 返回错误编号。
func (e Error) Error() string { return string(e) }

const (
	ErrInvalid      Error = "invalid_input"
	ErrUnauthorized Error = "unauthorized"
	ErrForbidden    Error = "forbidden"
	ErrNotFound     Error = "not_found"
	ErrConflict     Error = "conflict"
	ErrUnavailable  Error = "unavailable"
)

// ConflictError 表示角色仍被账号使用，不能删除；AccountCount 是使用它的账号数。
type ConflictError struct{ AccountCount int }

// Error 返回 conflict，表示操作与当前数据状态冲突。
func (e *ConflictError) Error() string { return string(ErrConflict) }

// Unwrap 让调用方可以用 errors.Is 判断这是否属于 ErrConflict。
func (e *ConflictError) Unwrap() error { return ErrConflict }

// SafeError 保留本包定义的错误，其他错误统一返回 unavailable，避免把数据库内部信息传给调用方。
func SafeError(err error) error {
	if err == nil {
		return nil
	}
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return conflict
	}
	var business Error
	if errors.As(err, &business) {
		return business
	}
	return ErrUnavailable
}
