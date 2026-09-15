// Package persistence 存储角色与权限；身份状态只经安全视图读取。
package persistence

import (
	"context"
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Scope 由身份持久化提供当前连接及安全读取范围。
type Scope func(context.Context, func(*gorm.DB, identity.Queries) error) error

// Store 将普通读写委托给统一事务协调器。
type Store struct{ read, write Scope }

// NewStore 只绑定范围，不自行开启事务。
func NewStore(read, write Scope) *Store { return &Store{read: read, write: write} }

// BoundStore 只在当前事务内有效，所有方法均不创建或提交事务。
type BoundStore struct {
	db       *gorm.DB
	identity identity.Queries
}

// Bind 组合当前事务连接与安全身份视图，SQL日志保持静默。
func Bind(db *gorm.DB, reader identity.Queries) *BoundStore {
	return &BoundStore{db: db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}), identity: reader}
}

func (s *Store) Read(ctx context.Context, fn func(rbac.Queries) error) error {
	if s.read == nil {
		return rbac.ErrUnavailable
	}
	return translate(s.read(ctx, func(db *gorm.DB, v identity.Queries) error { return scopedError(fn(Bind(db, v))) }))
}

func (s *Store) Write(ctx context.Context, fn func(rbac.Tx) error) error {
	if s.write == nil {
		return rbac.ErrUnavailable
	}
	return translate(s.write(ctx, func(db *gorm.DB, v identity.Queries) error { return scopedError(fn(Bind(db, v))) }))
}

func (s *BoundStore) Read(_ context.Context, fn func(rbac.Queries) error) error {
	return translate(fn(s))
}

func (s *BoundStore) Write(_ context.Context, fn func(rbac.Tx) error) error { return translate(fn(s)) }

// scopedError 让协调器识别业务拒绝，避免把缺权限记为数据库故障；保留RBAC关联冲突详情。
func scopedError(e error) error {
	var business rbac.Error
	if errors.As(e, &business) {
		return errors.Join(policy.IdentityError(business), e)
	}
	return e
}

// PostgreSQL unique_violation；与 identity/persistence/store.go 同源，保持身份导出面不变。
const pgUniqueViolation = "23505"

func translate(e error) error {
	if e == nil {
		return nil
	}
	var business rbac.Error
	if errors.As(e, &business) {
		return e
	}
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return rbac.ErrNotFound
	}
	var auth identity.Error
	if errors.As(e, &auth) {
		return policy.PermissionError(auth)
	}
	var pg *pgconn.PgError
	var lite sqlite3.Error
	if (errors.As(e, &pg) && pg.Code == pgUniqueViolation) || (errors.As(e, &lite) && (lite.ExtendedCode == sqlite3.ErrConstraintUnique || lite.ExtendedCode == sqlite3.ErrConstraintPrimaryKey)) {
		return rbac.ErrConflict
	}
	return e
}

func (s *BoundStore) State() rbac.State {
	v := s.identity.State()
	return rbac.State{Initialized: v.Initialized, ChiefAccountID: v.ChiefAccountID}
}

func account(a identity.Account) rbac.AccountInfo {
	return rbac.AccountInfo{ID: a.ID, Username: a.Username, DisplayName: a.DisplayName, Active: a.Status == identity.StatusActive, MustChangePassword: a.MustChangePassword}
}

func (s *BoundStore) Account(id string) (rbac.AccountInfo, error) {
	a, e := s.identity.LookupAccount(id)
	return account(a), translate(e)
}

func (s *BoundStore) Revalidate(p rbac.Subject) (rbac.AccountInfo, error) {
	actual, e := s.identity.Revalidate(identity.Principal{AccountID: p.AccountID, SessionID: p.SessionID, AuthVersion: p.AuthVersion})
	if e != nil {
		return rbac.AccountInfo{}, translate(e)
	}
	return s.Account(actual.AccountID)
}

func (s *BoundStore) CountActiveAccounts(ids []string) (int, error) {
	n, e := s.identity.CountActiveAccounts(ids)
	return n, translate(e)
}

var (
	_ rbac.Repository = (*Store)(nil)
	_ rbac.Repository = (*BoundStore)(nil)
	_ rbac.Tx         = (*BoundStore)(nil)
)
