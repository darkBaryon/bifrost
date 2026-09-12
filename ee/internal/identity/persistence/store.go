// 本文件实现读取、锁定 state 的事务、限流预占与分页；六张表的行结构见 rows.go。
//
// Package persistence 通过共享配置数据库实现身份存储及原子操作。
package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Store 实现 identity.Repository；SQL 日志静默，避免错误查询输出凭据、哈希和身份信息。连接由宿主持有并关闭。
type Store struct {
	db  *gorm.DB
	log Logger
}

// NewStore 复用宿主的数据库连接；存储故障经 log 记录安全诊断。
func NewStore(db *gorm.DB, log Logger) *Store {
	return &Store{db: db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}), log: log}
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
	var pg *pgconn.PgError
	var lite sqlite3.Error
	switch {
	case errors.As(err, &pg) && pg.Code == pgUniqueViolation,
		errors.As(err, &lite) && (lite.ExtendedCode == sqlite3.ErrConstraintUnique || lite.ExtendedCode == sqlite3.ErrConstraintPrimaryKey):
		return identity.ErrConflict
	}
	return err
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

// authRow 是账号与会话联合查询的结果；账号列由 GORM 按导出字段展开，会话列用 session_ 前缀避免同名。
type authRow struct {
	Account           accountRow `gorm:"embedded"`
	SessionID         string
	TokenHash         string
	SessionAccountID  string
	IssuedAuthVersion int64
	SessionExpiresAt  time.Time
	SessionCreatedAt  time.Time
	SessionRevokedAt  *time.Time
}

// authRecord 在一条 SQL 中读取会话及其账号，避免跨查询读出不一致的版本。
func authRecord(db *gorm.DB, column, value string) (identity.AuthRecord, error) {
	var row authRow
	result := db.Table("ee_identity_accounts a").
		Select("a.*, s.id AS session_id, s.token_hash, s.account_id AS session_account_id, s.issued_auth_version, "+
			"s.expires_at AS session_expires_at, s.created_at AS session_created_at, s.revoked_at AS session_revoked_at").
		Joins("JOIN ee_identity_sessions s ON s.account_id = a.id").Where(column+" = ?", value).Limit(1).Scan(&row)
	if result.Error != nil {
		return identity.AuthRecord{}, result.Error
	}
	if result.RowsAffected == 0 {
		return identity.AuthRecord{}, identity.ErrNotFound
	}
	return identity.AuthRecord{
		Account: row.Account.record(),
		Session: identity.Session{ID: row.SessionID, TokenHash: row.TokenHash, AccountID: row.SessionAccountID,
			IssuedAuthVersion: row.IssuedAuthVersion, ExpiresAt: row.SessionExpiresAt, CreatedAt: row.SessionCreatedAt, RevokedAt: row.SessionRevokedAt},
	}, nil
}

func (s *Store) AuthByHash(ctx context.Context, hash string) (identity.AuthRecord, error) {
	v, err := authRecord(s.db.WithContext(ctx), "s.token_hash", hash)
	return v, logDatabaseFailure(s.log, ctx, "session.read", err)
}

func (s *Store) AuthByID(ctx context.Context, id string) (identity.AuthRecord, error) {
	v, err := authRecord(s.db.WithContext(ctx), "s.id", id)
	return v, logDatabaseFailure(s.log, ctx, "session.read", err)
}

// transaction 实现 identity.Tx；stage 记录最近执行的操作，供失败诊断定位阶段。
type transaction struct {
	db    *gorm.DB
	state identity.State
	stage string
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

func (t *transaction) AuthByID(id string) (identity.AuthRecord, error) {
	t.stage = "session.read"
	return authRecord(t.db, "s.id", id)
}

// InsertSession 先删除已到期的会话行再写入：过期即删（含已撤销），会话表不承担审计；无索引全表扫描，行数由本清理封顶。
func (t *transaction) InsertSession(s identity.Session) error {
	t.stage = "session.sweep"
	if err := t.db.Where("expires_at < ?", time.Now().UTC()).Delete(&sessionRow{}).Error; err != nil {
		return err
	}
	t.stage = "session.insert"
	r := sessionRowOf(s)
	return t.db.Create(&r).Error
}

func (t *transaction) RevokeSession(id string, at time.Time) error {
	t.stage = "session.revoke"
	return t.db.Model(&sessionRow{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", at).Error
}

func (t *transaction) RevokeSessions(accountID string, at time.Time) error {
	t.stage = "sessions.revoke"
	return t.db.Model(&sessionRow{}).Where("account_id = ? AND revoked_at IS NULL", accountID).Update("revoked_at", at).Error
}

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

// InsertTicket 先删除已到期的票据行再写入；已消费但未到期的行留到到期。
func (t *transaction) InsertTicket(v identity.Ticket) error {
	t.stage = "ticket.sweep"
	if err := t.db.Where("expires_at < ?", time.Now().UTC()).Delete(&ticketRow{}).Error; err != nil {
		return err
	}
	t.stage = "ticket.insert"
	return t.db.Create(&ticketRow{Hash: v.Hash, SessionID: v.SessionID, ExpiresAt: v.ExpiresAt}).Error
}

func (t *transaction) ConsumeTicket(hash string, at time.Time) (string, error) {
	t.stage = "ticket.consume"
	result := t.db.Model(&ticketRow{}).Where("hash = ? AND consumed_at IS NULL AND expires_at > ?", hash, at).Update("consumed_at", at)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", identity.ErrUnauthorized
	}
	var r ticketRow
	err := t.db.Where("hash = ?", hash).First(&r).Error
	return r.SessionID, err
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
			result := db.Model(&stateRow{}).Where("id = 1").UpdateColumn("revision", gorm.Expr("revision + 1"))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return identity.ErrUnavailable
			}
			var row stateRow
			if e := db.First(&row, 1).Error; e != nil {
				return e
			}
			t := &transaction{db: db, state: identity.State{Initialized: row.Initialized, ChiefAccountID: row.ChiefAccountID}, stage: "transaction.operation"}
			e := fn(t)
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

// ReserveLogin 在一个事务里为每个桶预占一次：窗口过期则重开，达到上限返回 ErrLimited 并回滚全部预占。
func (s *Store) ReserveLogin(ctx context.Context, limits []identity.LoginLimit, at time.Time) error {
	return s.transaction(ctx, func(t *transaction) error {
		t.stage = "login_limit.reserve"
		if e := t.db.Where("window_start < ?", at.Add(-limitRetention)).Delete(&limitRow{}).Error; e != nil {
			return e
		}
		for _, l := range limits {
			r := limitRow{Key: l.Key, WindowStart: at}
			if e := t.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error; e != nil {
				return e
			}
			if e := t.db.Where("key = ?", l.Key).First(&r).Error; e != nil {
				return e
			}
			if !r.WindowStart.Add(l.Window).After(at) {
				r.WindowStart, r.Count = at, 0
			}
			if r.Count >= l.Max {
				return identity.ErrLimited
			}
			r.Count++
			if e := t.db.Save(&r).Error; e != nil {
				return e
			}
		}
		return nil
	})
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
