// 本文件实现账号读写、锁定 state 的事务和分页；旧认证表结构仅供已有迁移使用。
//
// Package persistence 通过共享配置数据库实现身份存储及原子操作。
package persistence

import (
	"context"
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	authstore "github.com/darkBaryon/bifrost/ee/internal/auth/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store 实现 identity.Repository；SQL 日志静默，避免错误查询输出凭据、哈希和身份信息。连接由宿主持有并关闭。
type Store struct {
	db            *gorm.DB
	log           database.Logger
	policyFactory PolicyFactory
}

// NewStore 复用宿主的数据库连接；存储故障经 log 记录安全诊断。
func NewStore(db *gorm.DB, log database.Logger, options ...Option) *Store {
	s := &Store{db: db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}), log: log}
	for _, option := range options {
		option(s)
	}
	return s
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return identity.ErrNotFound
	}
	return err
}

// pgUniqueViolation 是 PostgreSQL 唯一约束冲突的 SQLSTATE。
const pgUniqueViolation = "23505"

// conflict 把两种驱动的唯一键冲突转换为 ErrConflict，其他错误原样返回。
func conflict(err error) error {
	if IsUniqueConflict(err) {
		return identity.ErrConflict
	}
	return err
}

// IsUniqueConflict 识别PostgreSQL和SQLite的唯一键或主键冲突；其他错误返回false。
func IsUniqueConflict(err error) bool {
	var pg *pgconn.PgError
	var lite sqlite3.Error
	return (errors.As(err, &pg) && pg.Code == pgUniqueViolation) ||
		(errors.As(err, &lite) && (lite.ExtendedCode == sqlite3.ErrConstraintUnique || lite.ExtendedCode == sqlite3.ErrConstraintPrimaryKey))
}

func (s *Store) State(ctx context.Context) (identity.State, error) {
	var r stateRow
	err := s.db.WithContext(ctx).First(&r, 1).Error
	return identity.State{Initialized: r.Initialized, ChiefAccountID: r.ChiefAccountID}, notFound(logDatabaseFailure(s.log, ctx, "state.read", err))
}

func (s *Store) RecordByName(ctx context.Context, name string) (identity.AccountRecord, error) {
	var r accountRow
	err := s.db.WithContext(ctx).Where("username = ?", name).First(&r).Error
	return r.record(), notFound(logDatabaseFailure(s.log, ctx, "account.read", err))
}

func (s *Store) AuthByID(ctx context.Context, id string) (auth.Record, error) {
	return authstore.NewStore(s.db, s.log).AuthByID(ctx, id)
}

// transaction 实现 identity.Tx；stage 记录最近执行的操作，供失败诊断定位阶段。
type transaction struct {
	db     *gorm.DB
	state  identity.State
	stage  string
	policy identity.AccountPolicy
}

func (t *transaction) State() identity.State { return t.state }

func (t *transaction) SaveState(s identity.State) error {
	t.stage = "state.save"
	err := t.db.Model(&stateRow{}).Where("id = 1").Updates(map[string]any{"initialized": s.Initialized, "chief_account_id": s.ChiefAccountID}).Error
	if err == nil {
		t.state = s
	}
	return err
}

func (t *transaction) Account(id string) (identity.AccountRecord, error) {
	t.stage = "account.read"
	var r accountRow
	err := t.db.Where("id = ?", id).First(&r).Error
	return r.record(), notFound(err)
}

func (t *transaction) AccountByName(name string) (identity.AccountRecord, error) {
	t.stage = "account.read"
	var r accountRow
	err := t.db.Where("username = ?", name).First(&r).Error
	return r.record(), notFound(err)
}

func (t *transaction) InsertAccount(c identity.AccountRecord) error {
	t.stage = "account.insert"
	r := accountRowOf(c)
	return conflict(t.db.Create(&r).Error)
}

func (t *transaction) SaveAccount(c identity.AccountRecord) error {
	t.stage = "account.update"
	r := accountRowOf(c)
	return t.db.Save(&r).Error
}

// DeleteAccount 只删除账号；关联会话由调用方显式清理。
func (t *transaction) DeleteAccount(id string) error {
	t.stage = "account.delete"
	result := t.db.Where("id = ?", id).Delete(&accountRow{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return identity.ErrNotFound
	}
	return nil
}

func (t *transaction) AuthByID(id string) (auth.Record, error) {
	t.stage = "session.read"
	return authstore.NewStore(t.db, nil).AuthByID(t.db.Statement.Context, id)
}

// DB 返回当前调用方拥有的事务，供其他模块在同一事务内执行操作。
func (t *transaction) DB() *gorm.DB { return t.db }

func (t *transaction) Event(operationID string) (identity.PasswordEvent, error) {
	t.stage = "password_event.read"
	var r eventRow
	err := t.db.Where("operation_id = ?", operationID).First(&r).Error
	return r.event(), notFound(err)
}

func (t *transaction) InsertEvent(e identity.PasswordEvent) error {
	t.stage = "password_event.insert"
	r := eventRowOf(e)
	return conflict(t.db.Create(&r).Error)
}

// Transaction 先更新 state.revision 取得写锁（SQLite 为库级写锁，PostgreSQL 为行锁），再执行 fn。
// SQLite 锁冲突整笔重试，耗尽后返回 ErrUnavailable；其他错误记录诊断后原样返回。
func (s *Store) Transaction(ctx context.Context, fn func(identity.Tx) error) error {
	return s.transaction(ctx, func(t *transaction) error { return fn(t) })
}

func (s *Store) transaction(ctx context.Context, fn func(*transaction) error) error {
	phase := "transaction.begin"
	err := retryBusy(ctx, func() error {
		phase = "transaction.begin"
		return s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
			phase = "transaction.lock"
			t, e := lockTransaction(db)
			if e != nil {
				return e
			}
			if s.policyFactory != nil {
				t.policy, e = s.policyFactory(db, readView{t})
				if e != nil {
					return e
				}
				if t.policy == nil {
					return identity.ErrUnavailable
				}
			}
			e = fn(t)
			phase = t.stage
			if e == nil {
				phase = "transaction.commit"
			}
			return e
		})
	})
	if sqliteBusy(err) {
		logDatabaseFailure(s.log, ctx, phase, err)
		return identity.ErrUnavailable
	}
	return logDatabaseFailure(s.log, ctx, phase, err)
}

// beforeCursor 是分页的唯一比较规则：按 (column, id) 严格早于游标；游标为空表示从头开始。
// column 必须与调用方随后的 Order 排序键一致，否则翻页会漏行或重复。括号显式写出，不依赖 GORM 包裹 OR 表达式。
func beforeCursor(q *gorm.DB, column string, c identity.Cursor) *gorm.DB {
	if c.ID == "" {
		return q
	}
	return q.Where("("+column+" < ? OR ("+column+" = ? AND id < ?))", c.At, c.At, c.ID)
}

func (s *Store) Accounts(ctx context.Context, c identity.Cursor, n int) ([]identity.Account, error) {
	rows := []accountRow{}
	q := beforeCursor(s.db.WithContext(ctx), "created_at", c)
	err := q.Order("created_at DESC, id DESC").Limit(n).Find(&rows).Error
	out := make([]identity.Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.account())
	}
	return out, logDatabaseFailure(s.log, ctx, "accounts.list", err)
}

func (s *Store) Events(ctx context.Context, target string, c identity.Cursor, n int) ([]identity.PasswordEvent, error) {
	rows := []eventRow{}
	q := s.db.WithContext(ctx)
	if target != "" {
		q = q.Where("target_id = ?", target)
	}
	q = beforeCursor(q, "occurred_at", c)
	err := q.Order("occurred_at DESC, id DESC").Limit(n).Find(&rows).Error
	out := make([]identity.PasswordEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.event())
	}
	return out, logDatabaseFailure(s.log, ctx, "password_events.list", err)
}

var _ identity.Repository = (*Store)(nil)
