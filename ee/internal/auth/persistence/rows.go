// 本文件定义认证读取所需的旧表投影；不承担账号或会话表的建表与写入。
package persistence

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/session"
)

type accountRow struct {
	ID                 string `gorm:"primaryKey"`
	Username           string
	DisplayName        string
	Status             string
	MustChangePassword bool
	PasswordHash       string
	AuthVersion        int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastLoginAt        *time.Time
}

func (accountRow) TableName() string { return "ee_identity_accounts" }

func (row accountRow) credentials() auth.Credentials {
	return auth.Credentials{
		User: auth.User{ID: row.ID, Username: row.Username, DisplayName: row.DisplayName, Status: auth.AccountStatus(row.Status),
			MustChangePassword: row.MustChangePassword, CreatedAt: row.CreatedAt, LastLoginAt: row.LastLoginAt},
		PasswordHash: row.PasswordHash, AuthVersion: row.AuthVersion, UpdatedAt: row.UpdatedAt,
	}
}

// 联合查询的会话列使用别名，避免覆盖账号的 id、created_at 等同名列。
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

func (row authRow) record() auth.Record {
	return auth.Record{Account: row.Account.credentials(), Session: session.Record{
		ID: row.SessionID, TokenHash: row.TokenHash, AccountID: row.SessionAccountID,
		IssuedAuthVersion: row.IssuedAuthVersion, ExpiresAt: row.SessionExpiresAt,
		CreatedAt: row.SessionCreatedAt, RevokedAt: row.SessionRevokedAt,
	}}
}

type limitRow struct {
	Key         string `gorm:"primaryKey"`
	WindowStart time.Time
	Count       int
}

func (limitRow) TableName() string { return "ee_identity_login_limits" }
