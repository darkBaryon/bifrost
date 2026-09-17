// Package identity 将身份动作转换为角色权限，不持有数据库或宿主对象。
package identity

import (
	"context"
	"errors"

	auth "github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
)

// Policy 是普通读取范围或当前事务绑定范围上的身份策略。
type Policy struct{ service *rbac.Service }

// NewPolicy 绑定权限服务，事务策略必须传入绑定当前事务的服务。
func NewPolicy(service *rbac.Service) *Policy { return &Policy{service: service} }

// Subject 只转换会话句柄，不把调用方字段当成已重新验证的身份。
func Subject(p auth.Principal) rbac.Subject {
	return rbac.Subject{AccountID: p.AccountID, SessionID: p.SessionID, AuthVersion: p.AuthVersion}
}

var errorPairs = []struct {
	permission rbac.Error
	identity   auth.Error
}{
	{rbac.ErrUnauthorized, auth.ErrUnauthorized}, {rbac.ErrForbidden, auth.ErrForbidden},
	{rbac.ErrInvalid, auth.ErrInvalid}, {rbac.ErrNotFound, auth.ErrNotFound},
	{rbac.ErrConflict, auth.ErrConflict}, {rbac.ErrUnavailable, auth.ErrUnavailable},
}

// IdentityError 将权限错误转换为身份模块的安全业务错误。
func IdentityError(err error) error {
	if err == nil {
		return nil
	}
	for _, pair := range errorPairs {
		if errors.Is(err, pair.permission) {
			return pair.identity
		}
	}
	return auth.ErrUnavailable
}

// PermissionError 只转换已知身份错误；未映射错误（含限流）在RBAC边界关闭为unavailable。
func PermissionError(err error) error {
	if err == nil {
		return nil
	}
	for _, pair := range errorPairs {
		if errors.Is(err, pair.identity) {
			return pair.permission
		}
	}
	return rbac.ErrUnavailable
}

// Authorize 把身份操作对应到角色权限，并重新核实账号和会话。
func (p *Policy) Authorize(ctx context.Context, _ auth.State, actor auth.Principal, action auth.AccountAction, _ string) error {
	if p == nil || p.service == nil {
		return auth.ErrUnavailable
	}
	var permission rbac.Permission
	switch action {
	case auth.ReadAccounts:
		permission = rbac.UsersView
	case auth.CreateAccounts, auth.ChangeAccountStatus, auth.ResetAccountPassword, auth.ReadAllPasswordEvents:
		permission = rbac.UsersManage
	case auth.OpenConsoleStream:
		permission = rbac.NotificationsView
	default:
		return auth.ErrForbidden
	}
	return IdentityError(p.service.Authorize(ctx, Subject(actor), permission))
}

// BeforeStatusChange 停用前保护最后一个启用的主管理员；启用不需要额外保护，调用方已检查ChangeAccountStatus权限。
func (p *Policy) BeforeStatusChange(ctx context.Context, _ auth.State, actor auth.Principal, target auth.Account, next auth.AccountStatus) error {
	if next != auth.StatusDisabled {
		return nil
	}
	if p == nil || p.service == nil {
		return auth.ErrUnavailable
	}
	return IdentityError(p.service.BeforeDisable(ctx, Subject(actor), target.ID))
}

// AfterInitialize 在初始化账号的同一事务里补上主管理员角色。
func (p *Policy) AfterInitialize(ctx context.Context, state auth.State) error {
	if p == nil || p.service == nil {
		return auth.ErrUnavailable
	}
	return IdentityError(p.service.BindInitialChief(ctx, state.ChiefAccountID))
}

// AfterRecover 在恢复账号的同一事务里补回主管理员角色，并保留其他角色。
func (p *Policy) AfterRecover(ctx context.Context, state auth.State) error {
	if p == nil || p.service == nil {
		return auth.ErrUnavailable
	}
	return IdentityError(p.service.RestoreChief(ctx, state.ChiefAccountID))
}

var _ auth.AccountPolicy = (*Policy)(nil)
