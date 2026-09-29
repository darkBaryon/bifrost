// 本文件组织登录预占、密码比较、事务内签发和登出；身份复验规则见 validation.go。
package auth

import (
	"context"
	"errors"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"gorm.io/gorm"
)

// lastLoginPrecision 使 SQLite 和 PostgreSQL 保存及返回相同精度的最近登录时间。
const lastLoginPrecision = time.Microsecond

// Service 使用账号查询、会话写入和共享密码算法完成认证，不持有角色或账号修改服务。
type Service struct {
	db        *gorm.DB
	store     Store
	sessions  *session.Service
	passwords PasswordHasher
	dummyHash string
	log       database.Logger
}

// New 绑定明确依赖并预生成未知账号比较用的摘要，避免通过比较耗时识别账号是否存在。
func New(db *gorm.DB, store Store, sessions *session.Service, hasher PasswordHasher, log database.Logger) (*Service, error) {
	if db == nil || store == nil || sessions == nil || hasher == nil {
		return nil, ErrInvalid
	}
	dummy, err := hasher.Hash("dummy-verification-only")
	if err != nil {
		return nil, SafeError(err)
	}
	return &Service{db: db, store: store, sessions: sessions, passwords: hasher, dummyHash: dummy, log: log}, nil
}

// Login 先原子预占登录桶，在事务外比较密码，再于同一写事务内复核账号、签发会话和更新登录时间。
// 错误用户名、密码及停用状态统一返回未认证；超长输入直接拒绝，不占用登录桶或密码计算槽。
func (s *Service) Login(ctx context.Context, username, password, peerIP string) (LoginResult, error) {
	if username == "" || len(username) > maxLoginUsernameBytes || len(password) > maxLoginPasswordBytes {
		return LoginResult{}, ErrUnauthorized
	}
	limits := []LoginLimit{
		{Key: "ip:" + session.Digest(peerIP), Max: LoginAttemptsPerIP, Window: LoginRateWindow},
		{Key: "name:" + session.Digest(username), Max: LoginAttemptsPerUsername, Window: LoginRateWindow},
	}
	if err := database.Write(ctx, s.db, func(tx *gorm.DB) error {
		return s.store.WithTx(tx).ReserveLogin(ctx, limits, time.Now().UTC())
	}); err != nil {
		return LoginResult{}, s.transactionError(ctx, "login_limit.transaction", err)
	}
	account, err := s.store.AccountByName(ctx, username)
	missing := errors.Is(err, ErrNotFound)
	if err != nil && !missing {
		return LoginResult{}, SafeError(err)
	}
	hash := account.PasswordHash
	if missing {
		hash = s.dummyHash
	}
	ok, err := s.passwords.Compare(hash, password)
	if err != nil {
		return LoginResult{}, SafeError(err)
	}
	if missing || !ok || account.Status != StatusActive {
		return LoginResult{}, ErrUnauthorized
	}
	var result LoginResult
	err = database.Write(ctx, s.db, func(tx *gorm.DB) error {
		store := s.store.WithTx(tx)
		latest, err := store.AccountByID(ctx, account.ID)
		if errors.Is(err, ErrNotFound) {
			return ErrUnauthorized
		}
		if err != nil {
			return err
		}
		// 密码比较期间账号可能已被改密、停用或删除，签发前必须复核版本和摘要。
		if latest.Status != StatusActive || latest.AuthVersion != account.AuthVersion || latest.PasswordHash != account.PasswordHash {
			return ErrUnauthorized
		}
		issued, err := s.sessions.Create(ctx, tx, latest.ID, latest.AuthVersion)
		if err != nil {
			return err
		}
		principal, err := ValidateRecord(Record{Account: latest, Session: issued.Record}, time.Now().UTC())
		if err != nil {
			return err
		}
		loggedAt := issued.Record.CreatedAt.Truncate(lastLoginPrecision)
		if err := store.UpdateLastLogin(ctx, latest.ID, loggedAt, issued.Record.CreatedAt); err != nil {
			return err
		}
		latest.LastLoginAt = &loggedAt
		result = LoginResult{Principal: principal, Account: latest.User, Token: issued.Token, ExpiresAt: issued.Record.ExpiresAt}
		return nil
	})
	if err != nil {
		return LoginResult{}, s.transactionError(ctx, "login.transaction", err)
	}
	return result, nil
}

// Logout 撤销 token 对应的会话；形状不对、不存在或已撤销的 token 同样成功，接口层随后清除 Cookie。
func (s *Service) Logout(ctx context.Context, raw string) error {
	if !session.ValidToken(raw) {
		return nil
	}
	record, err := s.store.AuthByHash(ctx, session.Digest(raw))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return SafeError(err)
	}
	return s.transactionError(ctx, "logout.transaction", database.Write(ctx, s.db, func(tx *gorm.DB) error {
		return s.sessions.Revoke(ctx, tx, record.Session.ID)
	}))
}

// transactionError 在整笔事务与重试结束后记录最终故障，再转换为安全错误；业务拒绝不记为数据库故障。
// Store 的具体 SQL 阶段仍可单独记录，本处补足开启、加锁、提交和跨模块写入失败。
func (s *Service) transactionError(ctx context.Context, stage string, err error) error {
	safe := SafeError(err)
	if errors.Is(safe, ErrUnavailable) || errors.Is(safe, session.ErrUnavailable) {
		database.LogFailure(s.log, ctx, "auth", stage, err)
	}
	return safe
}
