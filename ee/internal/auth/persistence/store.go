// Package persistence 用现有账号和会话表提供一次联合认证读取，仅写登录时间与限流桶。
package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Store 借用宿主连接；写方法必须绑定调用方已打开并锁定账号状态的事务。
type Store struct {
	db    *gorm.DB
	log   database.Logger
	bound bool
}

// NewStore 不创建或关闭连接，关闭 SQL 日志以避免查询参数泄露凭据。
func NewStore(db *gorm.DB, log database.Logger) *Store {
	return &Store{db: quiet(db), log: log}
}

func quiet(db *gorm.DB) *gorm.DB {
	if db == nil {
		return nil
	}
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

// WithTx 返回持有当前事务的副本；nil 事务保持不可用，绝不回退原连接。
func (s *Store) WithTx(tx *gorm.DB) auth.Store {
	return &Store{db: quiet(tx), log: s.log, bound: tx != nil}
}

func (s *Store) query(ctx context.Context) (*gorm.DB, error) {
	if s.db == nil {
		return nil, auth.ErrUnavailable
	}
	return s.db.WithContext(ctx), nil
}

func (s *Store) account(ctx context.Context, column, value string) (auth.Credentials, error) {
	db, err := s.query(ctx)
	if err != nil {
		return auth.Credentials{}, err
	}
	var row accountRow
	err = db.Where(column+" = ?", value).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return auth.Credentials{}, auth.ErrNotFound
	}
	if err != nil {
		return auth.Credentials{}, logFailure(s.log, ctx, "account.read", err)
	}
	return row.credentials(), nil
}

// AccountByName 按原始登录名读取凭据，不 trim 或改写旧账号名称。
func (s *Store) AccountByName(ctx context.Context, name string) (auth.Credentials, error) {
	return s.account(ctx, "username", name)
}

// AccountByID 读取签发前复核所需的账号记录；不存在返回 ErrNotFound。
func (s *Store) AccountByID(ctx context.Context, id string) (auth.Credentials, error) {
	return s.account(ctx, "id", id)
}

func (s *Store) authRecord(ctx context.Context, column, value string) (auth.Record, error) {
	db, err := s.query(ctx)
	if err != nil {
		return auth.Record{}, err
	}
	var row authRow
	result := db.Table("ee_identity_accounts a").
		Select("a.*, s.id AS session_id, s.token_hash, s.account_id AS session_account_id, s.issued_auth_version, "+
			"s.expires_at AS session_expires_at, s.created_at AS session_created_at, s.revoked_at AS session_revoked_at").
		Joins("JOIN ee_identity_sessions s ON s.account_id = a.id").Where(column+" = ?", value).Limit(1).Scan(&row)
	if result.Error != nil {
		return auth.Record{}, logFailure(s.log, ctx, "session.read", result.Error)
	}
	if result.RowsAffected == 0 {
		return auth.Record{}, auth.ErrNotFound
	}
	return row.record(), nil
}

// AuthByHash 在一条 SQL 中按 token 摘要读取账号和会话。
func (s *Store) AuthByHash(ctx context.Context, hash string) (auth.Record, error) {
	return s.authRecord(ctx, "s.token_hash", hash)
}

// AuthByID 在一条 SQL 中按会话编号读取账号和会话。
func (s *Store) AuthByID(ctx context.Context, id string) (auth.Record, error) {
	return s.authRecord(ctx, "s.id", id)
}

// UpdateLastLogin 仅更新登录时间和记录更新时间，和会话插入共用调用方事务。
func (s *Store) UpdateLastLogin(ctx context.Context, id string, loggedAt, updatedAt time.Time) error {
	if !s.bound {
		return auth.ErrInvalid
	}
	db, err := s.query(ctx)
	if err != nil {
		return err
	}
	result := db.Model(&accountRow{}).Where("id = ?", id).
		Updates(map[string]any{"last_login_at": loggedAt, "updated_at": updatedAt})
	if result.Error != nil {
		return logFailure(s.log, ctx, "account.last_login", result.Error)
	}
	if result.RowsAffected != 1 {
		return auth.ErrNotFound
	}
	return nil
}

// limitRetention 沿用旧登录桶清理周期，远大于一分钟的登录限流窗口。
const limitRetention = time.Hour

// ReserveLogin 在绑定事务内为所有桶预占一次；任一桶超限时调用方必须回滚整个事务。
func (s *Store) ReserveLogin(ctx context.Context, limits []auth.LoginLimit, at time.Time) error {
	if !s.bound {
		return auth.ErrInvalid
	}
	for _, limit := range limits {
		if limit.Key == "" || limit.Max <= 0 || limit.Window <= 0 {
			return auth.ErrInvalid
		}
	}
	db, err := s.query(ctx)
	if err != nil {
		return err
	}
	if err = db.Where("window_start < ?", at.Add(-limitRetention)).Delete(&limitRow{}).Error; err != nil {
		return logFailure(s.log, ctx, "login_limit.cleanup", err)
	}
	for _, limit := range limits {
		row := limitRow{Key: limit.Key, WindowStart: at}
		if err = db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return logFailure(s.log, ctx, "login_limit.reserve", err)
		}
		if err = db.Where("key = ?", limit.Key).First(&row).Error; err != nil {
			return logFailure(s.log, ctx, "login_limit.reserve", err)
		}
		if !row.WindowStart.Add(limit.Window).After(at) {
			row.WindowStart, row.Count = at, 0
		}
		if row.Count >= limit.Max {
			return auth.ErrLimited
		}
		row.Count++
		if err = db.Save(&row).Error; err != nil {
			return logFailure(s.log, ctx, "login_limit.reserve", err)
		}
	}
	return nil
}

var _ auth.Store = (*Store)(nil)
