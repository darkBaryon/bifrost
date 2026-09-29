// 本文件统一验证账号状态、会话有效期和认证版本；不检查角色权限或强制改密限制。
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/session"
	"gorm.io/gorm"
)

// ValidateRecord 验证同一快照中的账号与会话；强制改密会话仍有效，由具体业务限制可执行操作。
func ValidateRecord(record Record, at time.Time) (session.Principal, error) {
	account, current := record.Account, record.Session
	if account.ID == "" || account.Status != StatusActive || current.ID == "" || current.AccountID != account.ID ||
		current.RevokedAt != nil || !current.ExpiresAt.After(at) || account.AuthVersion != current.IssuedAuthVersion {
		return session.Principal{}, ErrUnauthorized
	}
	return session.Principal{AccountID: account.ID, SessionID: current.ID,
		AuthVersion: account.AuthVersion, MustChangePassword: account.MustChangePassword}, nil
}

// RevalidateRecord 复验记录，并核对调用方声明的账号、会话和版本；不信任声明的改密标记。
func RevalidateRecord(record Record, claimed session.Principal, at time.Time) (session.Principal, error) {
	actual, err := ValidateRecord(record, at)
	if err != nil {
		return session.Principal{}, err
	}
	if actual.AccountID != claimed.AccountID || actual.SessionID != claimed.SessionID || actual.AuthVersion != claimed.AuthVersion {
		return session.Principal{}, ErrUnauthorized
	}
	return actual, nil
}

func resolve(record Record, err error) (session.Principal, error) {
	if errors.Is(err, ErrNotFound) {
		return session.Principal{}, ErrUnauthorized
	}
	if err != nil {
		return session.Principal{}, SafeError(err)
	}
	return ValidateRecord(record, time.Now().UTC())
}

// Authenticate 按 Cookie 原始 token 读取并验证身份；形状不对时不查库。
func (s *Service) Authenticate(ctx context.Context, raw string) (session.Principal, error) {
	if !session.ValidToken(raw) {
		return session.Principal{}, ErrUnauthorized
	}
	return resolve(s.store.AuthByHash(ctx, session.Digest(raw)))
}

// ValidateSession 按会话编号复验身份，供 WebSocket 等长连接使用，不保留 HTTP 请求对象。
func (s *Service) ValidateSession(ctx context.Context, id string) (session.Principal, error) {
	return resolve(s.store.AuthByID(ctx, id))
}

// ValidateSessionInTransaction 在调用方事务内按会话编号验证身份，供票据消费后继续复核。
// 事务不能为空；身份无效时调用方须回滚票据消费等同事务操作。
func (s *Service) ValidateSessionInTransaction(ctx context.Context, tx *gorm.DB, id string) (session.Principal, error) {
	if tx == nil {
		return session.Principal{}, ErrInvalid
	}
	record, err := s.store.WithTx(tx).AuthByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return session.Principal{}, ErrUnauthorized
	}
	if err != nil {
		// 锁冲突等驱动错误留给事务拥有方重试；不能在提交边界之前折叠。
		return session.Principal{}, err
	}
	return ValidateRecord(record, time.Now().UTC())
}

// RevalidateSession 使用调用方的事务重新读取身份；事务不能为空，也不会另开事务。
func (s *Service) RevalidateSession(ctx context.Context, tx *gorm.DB, claimed session.Principal) (session.Principal, error) {
	if tx == nil {
		return session.Principal{}, ErrInvalid
	}
	record, err := s.store.WithTx(tx).AuthByID(ctx, claimed.SessionID)
	if errors.Is(err, ErrNotFound) {
		return session.Principal{}, ErrUnauthorized
	}
	if err != nil {
		return session.Principal{}, err
	}
	return RevalidateRecord(record, claimed, time.Now().UTC())
}

// Me 返回当前会话自己的账号资料，受限会话同样可用；失效或伪造的身份声明被拒绝。
func (s *Service) Me(ctx context.Context, claimed session.Principal) (User, error) {
	record, err := s.store.AuthByID(ctx, claimed.SessionID)
	if _, err := resolve(record, err); err != nil {
		return User{}, err
	}
	if _, err := RevalidateRecord(record, claimed, time.Now().UTC()); err != nil {
		return User{}, err
	}
	return record.Account.User, nil
}
