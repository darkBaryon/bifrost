// 本文件定义身份操作授权及生命周期接缝；权限策略由装配注入。
package identity

import (
	"context"
	"time"
)

// AccountAction 是身份模块需要策略判定的操作。
type AccountAction string

const (
	ReadAccounts          AccountAction = "accounts.read"
	CreateAccounts        AccountAction = "accounts.create"
	ChangeAccountStatus   AccountAction = "accounts.status"
	ResetAccountPassword  AccountAction = "accounts.reset_password"
	ReadAllPasswordEvents AccountAction = "password_events.read_all"
)

// AccountPolicy 提供操作判权及同事务生命周期约束。
type AccountPolicy interface {
	Authorize(context.Context, State, Principal, AccountAction, string) error
	BeforeStatusChange(context.Context, State, Principal, Account, AccountStatus) error
	AfterInitialize(context.Context, State) error
	AfterRecover(context.Context, State) error
}

// Queries 是不含凭据、只在当前事务内有效的身份读取范围。
type Queries interface {
	State() State
	Revalidate(Principal) (Principal, error)
	LookupAccount(string) (Account, error)
	CountActiveAccounts([]string) (int, error)
}

// ChiefPolicy 保留独立认证模块的恢复锚点管理语义。
type ChiefPolicy struct{}

func (ChiefPolicy) Authorize(_ context.Context, state State, p Principal, action AccountAction, _ string) error {
	switch action {
	case ReadAccounts, CreateAccounts, ChangeAccountStatus, ResetAccountPassword, ReadAllPasswordEvents:
	default:
		return ErrForbidden
	}
	return requireChief(state, p)
}

// requireChief 检查是否为系统初始化时指定的主管理员；票据入口在角色授权接入前沿用此限制。
func requireChief(state State, p Principal) error {
	if !state.Initialized || p.AccountID != state.ChiefAccountID {
		return ErrForbidden
	}
	return nil
}

func (ChiefPolicy) BeforeStatusChange(context.Context, State, Principal, Account, AccountStatus) error {
	return nil
}
func (ChiefPolicy) AfterInitialize(context.Context, State) error { return nil }
func (ChiefPolicy) AfterRecover(context.Context, State) error    { return nil }

// RevalidateRecord 复用会话验证并核对调用方声明的三个标识。
func RevalidateRecord(r AuthRecord, claimed Principal, at time.Time) (Principal, error) {
	actual, err := verify(r, at)
	if err == nil && (actual.AccountID != claimed.AccountID || actual.SessionID != claimed.SessionID || actual.AuthVersion != claimed.AuthVersion) {
		err = ErrUnauthorized
	}
	return actual, err
}
func (s *core) accessPolicy(tx Tx) AccountPolicy {
	if p := tx.AccessPolicy(); p != nil {
		return p
	}
	return s.policy
}
func authorize(ctx context.Context, policy AccountPolicy, state State, p Principal, action AccountAction, target string) error {
	if err := fullSession(p); err != nil {
		return err
	}
	return SafeError(policy.Authorize(ctx, state, p, action, target))
}
func (s *core) authorizeTx(ctx context.Context, tx Tx, p Principal, action AccountAction, target string) error {
	return authorize(ctx, s.accessPolicy(tx), tx.State(), p, action, target)
}
func (s *core) actor(ctx context.Context, tx Tx, p Principal, action AccountAction, target string) (AccountRecord, error) {
	actual, c, err := current(tx.AuthByID, p)
	if err != nil {
		return c, err
	}
	return c, s.authorizeTx(ctx, tx, actual, action, target)
}
func (s *core) requireAction(ctx context.Context, p Principal, action AccountAction, target string) error {
	actual, _, err := s.current(ctx, p)
	if err != nil {
		return err
	}
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	return authorize(ctx, s.policy, state, actual, action, target)
}

// RecoveryAnchor 在同一快照中读取部署恢复锚点的安全信息，不注册公开接口。
func (s *AccountService) RecoveryAnchor(ctx context.Context) (Account, error) {
	var out Account
	err := s.repo.Read(ctx, func(v Queries) error {
		state := v.State()
		if !state.Initialized {
			return ErrNotFound
		}
		var e error
		out, e = v.LookupAccount(state.ChiefAccountID)
		return e
	})
	return out, SafeError(err)
}
