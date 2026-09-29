// Package persistence 在调用方事务内读写会话与票据，沿用已有表、字段及过期保留规则。
// 本文件实现事务绑定、撤销、删除和票据的条件消费；不读取或修改账号表。
package persistence

import (
	"context"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/session"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store 复用宿主数据库；只有 WithTx 返回的副本允许写入，原连接由宿主管理生命周期。
type Store struct {
	db    *gorm.DB
	bound bool
}

// NewStore 接收宿主连接，但不会开始事务；所有写入仍须先绑定调用方提供的事务。
func NewStore(db *gorm.DB) *Store { return &Store{db: quiet(db)} }

// WithTx 返回绑定指定事务的副本，不修改原 Store；nil 事务不会回退到宿主连接。
func (s *Store) WithTx(tx *gorm.DB) session.Store {
	return &Store{db: quiet(tx), bound: tx != nil}
}

func quiet(db *gorm.DB) *gorm.DB {
	if db == nil {
		return nil
	}
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}

func (s *Store) transaction(ctx context.Context) (*gorm.DB, error) {
	if s == nil || !s.bound || s.db == nil {
		return nil, session.ErrInvalid
	}
	return s.db.WithContext(ctx), nil
}

// Insert 清理到期会话后写入新记录；清理与插入共用调用方事务。
func (s *Store) Insert(ctx context.Context, record session.Record) error {
	db, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	if err := sweepExpired(db, &sessionRow{}); err != nil {
		return err
	}
	row := sessionRowOf(record)
	return db.Create(&row).Error
}

// Revoke 仅为未撤销记录写入时间；未找到或已撤销视为成功。
func (s *Store) Revoke(ctx context.Context, id string, at time.Time) error {
	db, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	return db.Model(&sessionRow{}).Where("id = ? AND revoked_at IS NULL", id).Update("revoked_at", at).Error
}

// RevokeAccountSessions 标记账号所有未撤销会话；未到期的行与票据仍保留。
func (s *Store) RevokeAccountSessions(ctx context.Context, accountID string, at time.Time) error {
	db, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	return db.Model(&sessionRow{}).Where("account_id = ? AND revoked_at IS NULL", accountID).Update("revoked_at", at).Error
}

// DeleteAccountSessions 在同一事务先清理关联票据，再清理会话；不删除账号或审计事件。
func (s *Store) DeleteAccountSessions(ctx context.Context, accountID string) error {
	db, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	sessions := db.Model(&sessionRow{}).Select("id").Where("account_id = ?", accountID)
	if err := db.Where("session_id IN (?)", sessions).Delete(&ticketRow{}).Error; err != nil {
		return err
	}
	return db.Where("account_id = ?", accountID).Delete(&sessionRow{}).Error
}

// InsertTicket 清理到期票据后插入新票据；已消费但未到期的行继续保留。
func (s *Store) InsertTicket(ctx context.Context, ticket session.Ticket) error {
	db, err := s.transaction(ctx)
	if err != nil {
		return err
	}
	if err := sweepExpired(db, &ticketRow{}); err != nil {
		return err
	}
	return db.Create(&ticketRow{Hash: ticket.Hash, SessionID: ticket.SessionID, ExpiresAt: ticket.ExpiresAt}).Error
}

// ConsumeTicket 通过条件更新保证并发时只有一次消费成功；不存在、过期、已消费均返回 ErrUnauthorized。
// 消费记录与返回所属会话共用调用方事务；调用方后续校验失败时必须回滚该事务。
func (s *Store) ConsumeTicket(ctx context.Context, hash string, at time.Time) (string, error) {
	db, err := s.transaction(ctx)
	if err != nil {
		return "", err
	}
	result := db.Model(&ticketRow{}).Where("hash = ? AND consumed_at IS NULL AND expires_at > ?", hash, at).Update("consumed_at", at)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected != 1 {
		return "", session.ErrUnauthorized
	}
	var row ticketRow
	if err := db.Where("hash = ?", hash).First(&row).Error; err != nil {
		return "", err
	}
	return row.SessionID, nil
}

// sweepExpired 沿用原保留规则：expires_at <= 当前时刻即删除，未到期的撤销会话和已消费票据不提前删除。
func sweepExpired(db *gorm.DB, model any) error {
	return db.Where("expires_at <= ?", time.Now().UTC()).Delete(model).Error
}

var _ session.Store = (*Store)(nil)
