// 本文件定义会话与票据的运行期行结构，字段与原身份表兼容；建表仍由既有迁移负责。
package persistence

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/session"
)

type sessionRow struct {
	ID                string `gorm:"primaryKey"`
	TokenHash         string
	AccountID         string
	IssuedAuthVersion int64
	ExpiresAt         time.Time
	CreatedAt         time.Time
	RevokedAt         *time.Time
}

func (sessionRow) TableName() string { return "ee_identity_sessions" }

func sessionRowOf(record session.Record) sessionRow {
	return sessionRow{ID: record.ID, TokenHash: record.TokenHash, AccountID: record.AccountID,
		IssuedAuthVersion: record.IssuedAuthVersion, ExpiresAt: record.ExpiresAt,
		CreatedAt: record.CreatedAt, RevokedAt: record.RevokedAt}
}

// ticketRow 用摘要作为主键；条件更新 ConsumedAt 保证多节点只消费一次。
type ticketRow struct {
	Hash       string `gorm:"primaryKey"`
	SessionID  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

func (ticketRow) TableName() string { return "ee_identity_ws_tickets" }
