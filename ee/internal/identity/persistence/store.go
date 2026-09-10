// Package persistence 通过共享配置数据库实现身份存储及原子操作。
package persistence

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/maximhq/bifrost/framework/encrypt"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type accountRow struct {
	identity.Credential `gorm:"embedded"`
}

func (accountRow) TableName() string { return "ee_identity_accounts" }

type stateRow struct {
	ID             int `gorm:"primaryKey"`
	Initialized    bool
	ChiefAccountID string
	Revision       int64
}

func (stateRow) TableName() string { return "ee_identity_state" }

type sessionRow struct {
	identity.Session `gorm:"embedded"`
}

func (sessionRow) TableName() string { return "ee_identity_sessions" }

type eventRow struct {
	identity.PasswordEvent `gorm:"embedded"`
}

func (eventRow) TableName() string { return "ee_identity_password_events" }

type ticketRow struct {
	Hash       string `gorm:"primaryKey"`
	SessionID  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

func (ticketRow) TableName() string { return "ee_identity_ws_tickets" }

type limitRow struct {
	Key         string    `gorm:"primaryKey"`
	WindowStart time.Time `gorm:"index"`
	Count       int
}

func (limitRow) TableName() string { return "ee_identity_login_limits" }

// Passwords 复用宿主bcrypt，禁止截断超过算法上限的密码。
type Passwords struct{}

func (Passwords) Hash(p string) (string, error)     { return encrypt.Hash(p) }
func (Passwords) Compare(h, p string) (bool, error) { return encrypt.CompareHash(h, p) }

var bcryptHashPattern = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)

func (Passwords) ValidHash(h string) bool {
	_, err := bcrypt.Cost([]byte(h))
	return err == nil && bcryptHashPattern.MatchString(h)
}

// Store 使用静默SQL日志，避免错误查询输出凭据、哈希和身份信息。
type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store {
	return &Store{db: db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})}
}
func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return identity.ErrNotFound
	}
	return err
}
func conflict(err error) error {
	if err != nil && (errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "SQLSTATE 23505")) {
		return identity.ErrConflict
	}
	return err
}
func (s *Store) State(ctx context.Context) (identity.State, error) {
	var r stateRow
	e := s.db.WithContext(ctx).First(&r, 1).Error
	return identity.State{Initialized: r.Initialized, ChiefAccountID: r.ChiefAccountID}, notFound(databaseFailure(ctx, "state.read", e))
}
func (s *Store) CredentialByName(ctx context.Context, name string) (identity.Credential, error) {
	var r accountRow
	e := s.db.WithContext(ctx).Where("username = ?", name).First(&r).Error
	return r.Credential, notFound(databaseFailure(ctx, "account.read", e))
}

// authRecord 在一个SQL语句中读取会话及账号，避免跨查询读出不一致版本。
func authRecord(db *gorm.DB, column, value string) (identity.AuthRecord, error) {
	var row struct {
		identity.Credential `gorm:"embedded"`
		SessionID           string
		TokenHash           string
		SessionAccountID    string
		IssuedAuthVersion   int64
		SessionExpiresAt    time.Time
		SessionCreatedAt    time.Time
		SessionRevokedAt    *time.Time
	}
	result := db.Table("ee_identity_accounts a").Select("a.*, s.id AS session_id, s.token_hash, s.account_id AS session_account_id, s.issued_auth_version, s.expires_at AS session_expires_at, s.created_at AS session_created_at, s.revoked_at AS session_revoked_at").Joins("JOIN ee_identity_sessions s ON s.account_id = a.id").Where(column+" = ?", value).Limit(1).Scan(&row)
	if result.Error != nil {
		return identity.AuthRecord{}, result.Error
	}
	if result.RowsAffected == 0 {
		return identity.AuthRecord{}, identity.ErrNotFound
	}
	return identity.AuthRecord{Credential: row.Credential, Session: identity.Session{ID: row.SessionID, TokenHash: row.TokenHash, AccountID: row.SessionAccountID, IssuedAuthVersion: row.IssuedAuthVersion, ExpiresAt: row.SessionExpiresAt, CreatedAt: row.SessionCreatedAt, RevokedAt: row.SessionRevokedAt}}, nil
}
func (s *Store) AuthByHash(ctx context.Context, h string) (identity.AuthRecord, error) {
	v, e := authRecord(s.db.WithContext(ctx), "s.token_hash", h)
	return v, databaseFailure(ctx, "session.read", e)
}
func (s *Store) AuthByID(ctx context.Context, id string) (identity.AuthRecord, error) {
	v, e := authRecord(s.db.WithContext(ctx), "s.id", id)
	return v, databaseFailure(ctx, "session.read", e)
}

type transaction struct {
	db    *gorm.DB
	state identity.State
	stage string
}

func (t *transaction) State() identity.State { return t.state }
func (t *transaction) SaveState(s identity.State) error {
	t.stage = "state.save"
	e := t.db.Model(&stateRow{}).Where("id = 1").Updates(map[string]any{"initialized": s.Initialized, "chief_account_id": s.ChiefAccountID}).Error
	if e == nil {
		t.state = s
	}
	return e
}
func (t *transaction) Account(id string) (identity.Credential, error) {
	t.stage = "account.read"
	var r accountRow
	e := t.db.Where("id = ?", id).First(&r).Error
	return r.Credential, notFound(e)
}
func (t *transaction) AccountByName(name string) (identity.Credential, error) {
	t.stage = "account.read"
	var r accountRow
	e := t.db.Where("username = ?", name).First(&r).Error
	return r.Credential, notFound(e)
}
func (t *transaction) InsertAccount(c identity.Credential) error {
	t.stage = "account.insert"
	return conflict(t.db.Create(&accountRow{c}).Error)
}
func (t *transaction) SaveAccount(c identity.Credential) error {
	t.stage = "account.update"
	return t.db.Save(&accountRow{c}).Error
}
func (t *transaction) AuthByID(id string) (identity.AuthRecord, error) {
	t.stage = "session.read"
	return authRecord(t.db, "s.id", id)
}
func (t *transaction) InsertSession(s identity.Session) error {
	t.stage = "session.insert"
	return t.db.Create(&sessionRow{s}).Error
}
func (t *transaction) RevokeSession(id string, at time.Time) error {
	t.stage = "session.revoke"
	return t.db.Model(&sessionRow{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", at).Error
}
func (t *transaction) RevokeSessions(id string, at time.Time) error {
	t.stage = "sessions.revoke"
	return t.db.Model(&sessionRow{}).Where("account_id = ? AND revoked_at IS NULL", id).Update("revoked_at", at).Error
}
func (t *transaction) Event(id string) (identity.PasswordEvent, error) {
	t.stage = "password_event.read"
	var r eventRow
	e := t.db.Where("operation_id = ?", id).First(&r).Error
	return r.PasswordEvent, notFound(e)
}
func (t *transaction) InsertEvent(e identity.PasswordEvent) error {
	t.stage = "password_event.insert"
	return conflict(t.db.Create(&eventRow{e}).Error)
}
func (t *transaction) InsertTicket(v identity.Ticket) error {
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
	e := t.db.Where("hash = ?", hash).First(&r).Error
	return r.SessionID, e
}
func (s *Store) Transaction(ctx context.Context, fn func(identity.Tx) error) error {
	var last error
	phase := "transaction.begin"
	for attempt := 0; attempt < 3; attempt++ {
		phase = "transaction.begin"
		e := s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
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
			tx := &transaction{db: db, state: identity.State{Initialized: row.Initialized, ChiefAccountID: row.ChiefAccountID}, stage: "transaction.operation"}
			e := fn(tx)
			phase = tx.stage
			if e == nil {
				phase = "transaction.commit"
			}
			return e
		})
		if !sqliteBusy(e) {
			return databaseFailure(ctx, phase, e)
		}
		last = e
		select {
		case <-ctx.Done():
			return databaseFailure(ctx, phase, ctx.Err())
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		}
	}
	databaseFailure(ctx, phase, last)
	return identity.ErrUnavailable
}
func (s *Store) ReserveLogin(ctx context.Context, nameHash, ipHash string, at time.Time) error {
	return s.Transaction(ctx, func(tx identity.Tx) error {
		t := tx.(*transaction)
		t.stage = "login_limit.reserve"
		db := t.db
		// 删除旧限流窗口；凭据与审计记录不参与此清理。
		if e := db.Where("window_start < ?", at.Add(-time.Hour)).Delete(&limitRow{}).Error; e != nil {
			return e
		}
		for _, v := range []struct {
			key string
			max int
		}{{"ip:" + ipHash, identity.LoginAttemptsPerIP}, {"name:" + nameHash, identity.LoginAttemptsPerUsername}} {
			r := limitRow{Key: v.key, WindowStart: at}
			if e := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error; e != nil {
				return e
			}
			if e := db.Where("key = ?", v.key).First(&r).Error; e != nil {
				return e
			}
			if !r.WindowStart.Add(identity.LoginRateWindow).After(at) {
				r.WindowStart = at
				r.Count = 0
			}
			if r.Count >= v.max {
				return identity.ErrLimited
			}
			r.Count++
			if e := db.Save(&r).Error; e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Accounts(ctx context.Context, c identity.Cursor, n int) ([]identity.Credential, error) {
	rows := []accountRow{}
	q := s.db.WithContext(ctx)
	if c.ID != "" {
		q = q.Where("created_at < ? OR (created_at = ? AND id < ?)", c.At, c.At, c.ID)
	}
	e := q.Order("created_at DESC, id DESC").Limit(n).Find(&rows).Error
	out := make([]identity.Credential, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Credential)
	}
	return out, databaseFailure(ctx, "accounts.list", e)
}
func (s *Store) Events(ctx context.Context, target string, c identity.Cursor, n int) ([]identity.PasswordEvent, error) {
	rows := []eventRow{}
	q := s.db.WithContext(ctx)
	if target != "" {
		q = q.Where("target_id = ?", target)
	}
	if c.ID != "" {
		q = q.Where("occurred_at < ? OR (occurred_at = ? AND id < ?)", c.At, c.At, c.ID)
	}
	e := q.Order("occurred_at DESC, id DESC").Limit(n).Find(&rows).Error
	out := make([]identity.PasswordEvent, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.PasswordEvent)
	}
	return out, databaseFailure(ctx, "password_events.list", e)
}

var _ identity.Repository = (*Store)(nil)
