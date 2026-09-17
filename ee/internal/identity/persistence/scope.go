// 本文件将身份写锁与只读快照提供给功能适配；视图不得逸出回调。
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"gorm.io/gorm"
)

// PolicyFactory 用当前事务连接构造策略；不得查询、开事务或保存到全局。
type PolicyFactory func(*gorm.DB, identity.Queries) (identity.AccountPolicy, error)

// Option 是仅在构造时应用的存储配置。
type Option func(*Store)

// WithPolicyFactory 注入事务策略工厂；配置工厂时不允许返回空策略。
func WithPolicyFactory(factory PolicyFactory) Option {
	return func(s *Store) { s.policyFactory = factory }
}

func (t *transaction) AccessPolicy() identity.AccountPolicy { return t.policy }

// readView 不向使用方暴露身份写入方法，State 随当前事务 SaveState 更新。
type readView struct{ t *transaction }

func (v readView) State() identity.State { return v.t.State() }
func (v readView) Revalidate(p identity.Principal) (identity.Principal, error) {
	r, e := v.t.AuthByID(p.SessionID)
	if errors.Is(e, identity.ErrNotFound) {
		return identity.Principal{}, identity.ErrUnauthorized
	}
	if e != nil {
		return identity.Principal{}, e
	}
	return identity.RevalidateRecord(r, p, time.Now().UTC())
}

func (v readView) LookupAccount(id string) (identity.Account, error) {
	a, e := v.t.Account(id)
	return a.Account, e
}

// identityIDBatch 保留 SQLite 常见 999 参数上限的余量，合同规定每批最多500个ID。
const identityIDBatch = 500

func (v readView) CountActiveAccounts(ids []string) (int, error) {
	ids = append([]string{}, ids...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	total := 0
	for len(ids) > 0 {
		n := min(len(ids), identityIDBatch)
		var count int64
		if e := v.t.db.Model(&accountRow{}).Where("id IN ? AND status = ?", ids[:n], identity.StatusActive).Count(&count).Error; e != nil {
			return 0, e
		}
		total += int(count)
		ids = ids[n:]
	}
	return total, nil
}

func loadTransaction(db *gorm.DB) (*transaction, error) {
	var row stateRow
	if e := db.First(&row, 1).Error; e != nil {
		return nil, e
	}
	return &transaction{db: db, state: identity.State{Initialized: row.Initialized, ChiefAccountID: row.ChiefAccountID}, stage: "transaction.operation"}, nil
}

func lockTransaction(db *gorm.DB) (*transaction, error) {
	result := db.Model(&stateRow{}).Where("id = 1").UpdateColumn("revision", gorm.Expr("revision + 1"))
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, identity.ErrUnavailable
	}
	return loadTransaction(db)
}

// LockView 在已有可写事务中取得身份锁；提交、重试由调用方负责。
func LockView(ctx context.Context, tx *gorm.DB) (identity.Queries, error) {
	t, e := lockTransaction(tx.WithContext(ctx))
	if e != nil {
		return nil, e
	}
	return readView{t}, nil
}

// Within 与身份写操作共享同一把写锁和重试入口。
func (s *Store) Within(ctx context.Context, fn func(*gorm.DB, identity.Queries) error) error {
	return s.transaction(ctx, func(t *transaction) error { return fn(t.db, readView{t}) })
}

// ReadWithin 在主库一致快照中先读身份状态，再调用读取方。
func (s *Store) ReadWithin(ctx context.Context, fn func(*gorm.DB, identity.Queries) error) error {
	options := &sql.TxOptions{ReadOnly: true}
	if s.db.Dialector.Name() == "postgres" {
		options.Isolation = sql.LevelRepeatableRead
	}
	err := s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		t, e := loadTransaction(db)
		if e != nil {
			return e
		}
		return fn(db, readView{t})
	}, options)
	return logDatabaseFailure(s.log, ctx, "snapshot.read", err)
}

// Read 实现业务层只读范围，不向业务暴露数据库类型。
func (s *Store) Read(ctx context.Context, fn func(identity.Queries) error) error {
	return s.ReadWithin(ctx, func(_ *gorm.DB, v identity.Queries) error { return fn(v) })
}

// RetrySQLiteBusy 供共用配置库的迁移复用相同重试规则；attempt 必须可整笔重入。
func RetrySQLiteBusy(ctx context.Context, attempt func() error) error { return retryBusy(ctx, attempt) }
