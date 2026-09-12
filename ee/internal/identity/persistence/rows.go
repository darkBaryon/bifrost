// 本文件定义六张身份表的行结构及其与业务类型的转换。字段名决定列名；ee_identity_v1 迁移按当前定义建表，改字段就是改表结构，须走新的迁移版本。
package persistence

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
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
}

func (accountRow) TableName() string { return "ee_identity_accounts" }

func (r accountRow) credential() identity.Credential {
	return identity.Credential{
		Account: identity.Account{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName,
			Status: identity.AccountStatus(r.Status), MustChangePassword: r.MustChangePassword},
		PasswordHash: r.PasswordHash, AuthVersion: r.AuthVersion, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func accountRowOf(c identity.Credential) accountRow {
	return accountRow{ID: c.ID, Username: c.Username, DisplayName: c.DisplayName, Status: string(c.Status),
		MustChangePassword: c.MustChangePassword, PasswordHash: c.PasswordHash, AuthVersion: c.AuthVersion,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// stateRow 是 ID 固定为 1 的单例；Revision 只用于在事务开始时取得写锁。
type stateRow struct {
	ID             int `gorm:"primaryKey"`
	Initialized    bool
	ChiefAccountID string
	Revision       int64
}

func (stateRow) TableName() string { return "ee_identity_state" }

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

func sessionRowOf(s identity.Session) sessionRow {
	return sessionRow{ID: s.ID, TokenHash: s.TokenHash, AccountID: s.AccountID, IssuedAuthVersion: s.IssuedAuthVersion,
		ExpiresAt: s.ExpiresAt, CreatedAt: s.CreatedAt, RevokedAt: s.RevokedAt}
}

type eventRow struct {
	ID          string `gorm:"primaryKey"`
	OperationID string
	ActorID     string
	TargetID    string
	ActorName   string
	TargetName  string
	Action      string
	Result      string
	ReasonCode  string
	OccurredAt  time.Time
}

func (eventRow) TableName() string { return "ee_identity_password_events" }

func (r eventRow) event() identity.PasswordEvent {
	return identity.PasswordEvent{ID: r.ID, OperationID: r.OperationID, ActorID: r.ActorID, TargetID: r.TargetID,
		ActorName: r.ActorName, TargetName: r.TargetName, Action: identity.EventAction(r.Action),
		Result: identity.EventResult(r.Result), ReasonCode: r.ReasonCode, OccurredAt: r.OccurredAt}
}

func eventRowOf(e identity.PasswordEvent) eventRow {
	return eventRow{ID: e.ID, OperationID: e.OperationID, ActorID: e.ActorID, TargetID: e.TargetID, ActorName: e.ActorName,
		TargetName: e.TargetName, Action: string(e.Action), Result: string(e.Result), ReasonCode: e.ReasonCode, OccurredAt: e.OccurredAt}
}

// ticketRow 以票据摘要为主键；ConsumedAt 由 UPDATE 条件保证多节点只消费一次。
type ticketRow struct {
	Hash       string `gorm:"primaryKey"`
	SessionID  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

func (ticketRow) TableName() string { return "ee_identity_ws_tickets" }

// limitRow 是一个登录限流桶，Key 形如 "ip:<摘要>" 或 "name:<摘要>"。
type limitRow struct {
	Key         string    `gorm:"primaryKey"`
	WindowStart time.Time `gorm:"index"`
	Count       int
}

func (limitRow) TableName() string { return "ee_identity_login_limits" }

// limitRetention 是限流桶的保留期，远大于任何登录窗口，只用于顺带清理旧桶。
const limitRetention = time.Hour
