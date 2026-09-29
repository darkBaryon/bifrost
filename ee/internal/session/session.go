// Package session 管理会话与一次性票据的生命周期；账号认证和业务授权由调用方负责。
// 本文件定义会话数据、存储契约及使用调用方事务的签发、撤销和消费操作。
package session

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// Error 是不包含凭据、SQL 或驱动信息的业务错误。
type Error string

func (e Error) Error() string { return string(e) }

// 会话错误由调用方映射到自身的协议错误；不存在、过期或已消费的票据均视为未认证。
const (
	ErrUnauthorized Error = "unauthorized"
	ErrInvalid      Error = "invalid_input"
	ErrUnavailable  Error = "unavailable"
)

// 产品规定会话采用绝对有效期，部署可在一小时到七天内配置；票据仅用于短暂的 WebSocket 握手。
const (
	DefaultTTL = 24 * time.Hour
	MinTTL     = time.Hour
	MaxTTL     = 7 * 24 * time.Hour
	TicketTTL  = 30 * time.Second
)

// Principal 是认证模块验证后返回的身份句柄；本模块不验证其中的账号状态或权限。
type Principal struct {
	AccountID          string
	SessionID          string
	AuthVersion        int64
	MustChangePassword bool
}

// Record 保存会话摘要及签发时的账号版本；撤销只做标记，到期后由下一次签发清理。
type Record struct {
	ID                string
	TokenHash         string
	AccountID         string
	IssuedAuthVersion int64
	ExpiresAt         time.Time
	CreatedAt         time.Time
	RevokedAt         *time.Time
}

// Issued 将原始令牌交给调用方一次；只有调用方事务提交后，令牌才可用于登录响应。
type Issued struct {
	Record Record
	Token  string
}

// Ticket 保存一次性票据的摘要与所属会话；消费标记由存储原子维护。
type Ticket struct {
	Hash      string
	SessionID string
	ExpiresAt time.Time
}

// Store 只处理会话和票据数据；WithTx 返回绑定调用方事务的副本，不开始或提交事务。
// 未绑定事务或传入 nil 时，写入必须拒绝，不能回退到原始数据库连接。
type Store interface {
	WithTx(*gorm.DB) Store
	Insert(context.Context, Record) error
	Revoke(context.Context, string, time.Time) error
	RevokeAccountSessions(context.Context, string, time.Time) error
	DeleteAccountSessions(context.Context, string) error
	InsertTicket(context.Context, Ticket) error
	ConsumeTicket(context.Context, string, time.Time) (string, error)
}

// Service 在调用方事务内维护会话与票据，不读取账号、不判断权限，也不持有共享事务。
// 存储错误原样交回事务拥有方，以保留锁冲突重试和诊断能力；事务结束后由入口转换为安全错误。
type Service struct {
	store Store
	ttl   time.Duration
}

// New 校验依赖和会话有效期；默认值须显式传入 DefaultTTL，零值同样视为无效配置。
func New(store Store, ttl time.Duration) (*Service, error) {
	if store == nil || ttl < MinTTL || ttl > MaxTTL {
		return nil, ErrInvalid
	}
	return &Service{store: store, ttl: ttl}, nil
}

// Create 签发会话；调用方须已在同一事务核实账号及版本，并负责最终提交。
func (s *Service) Create(ctx context.Context, tx *gorm.DB, accountID string, version int64) (Issued, error) {
	store, err := s.transactionStore(tx)
	if err != nil {
		return Issued{}, err
	}
	if accountID == "" {
		return Issued{}, ErrInvalid
	}
	at := time.Now().UTC()
	token := randomToken()
	record := Record{ID: randomID(), TokenHash: Digest(token), AccountID: accountID,
		IssuedAuthVersion: version, CreatedAt: at, ExpiresAt: at.Add(s.ttl)}
	if err := store.Insert(ctx, record); err != nil {
		return Issued{}, err
	}
	return Issued{Record: record, Token: token}, nil
}

// Revoke 撤销指定会话；不存在或已撤销视为成功，原撤销时间不变。
func (s *Service) Revoke(ctx context.Context, tx *gorm.DB, id string) error {
	store, err := s.transactionStore(tx)
	if err != nil {
		return err
	}
	if id == "" {
		return ErrInvalid
	}
	return store.Revoke(ctx, id, time.Now().UTC())
}

// RevokeAccountSessions 撤销账号的全部未撤销会话；票据保留，由认证模块在消费事务中复验会话。
func (s *Service) RevokeAccountSessions(ctx context.Context, tx *gorm.DB, accountID string) error {
	store, err := s.transactionStore(tx)
	if err != nil {
		return err
	}
	if accountID == "" {
		return ErrInvalid
	}
	return store.RevokeAccountSessions(ctx, accountID, time.Now().UTC())
}

// DeleteAccountSessions 先删除账号会话关联的票据，再删除会话；账号记录由调用方在同一事务处理。
func (s *Service) DeleteAccountSessions(ctx context.Context, tx *gorm.DB, accountID string) error {
	store, err := s.transactionStore(tx)
	if err != nil {
		return err
	}
	if accountID == "" {
		return ErrInvalid
	}
	return store.DeleteAccountSessions(ctx, accountID)
}

// IssueTicket 为指定会话签发一次性票据；会话有效性与通知权限须由调用方在当前事务核实。
func (s *Service) IssueTicket(ctx context.Context, tx *gorm.DB, sessionID string) (string, error) {
	store, err := s.transactionStore(tx)
	if err != nil {
		return "", err
	}
	if sessionID == "" {
		return "", ErrInvalid
	}
	token := randomToken()
	if err := store.InsertTicket(ctx, Ticket{Hash: Digest(token), SessionID: sessionID, ExpiresAt: time.Now().UTC().Add(TicketTTL)}); err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeTicket 原子消费票据并返回其会话编号；不存在、过期或重复消费均返回 ErrUnauthorized。
// 调用方必须在当前事务继续验证账号、会话与权限；验证失败应回滚，使消费标记一起回滚。
func (s *Service) ConsumeTicket(ctx context.Context, tx *gorm.DB, raw string) (string, error) {
	store, err := s.transactionStore(tx)
	if err != nil {
		return "", err
	}
	if !ValidToken(raw) {
		return "", ErrUnauthorized
	}
	id, err := store.ConsumeTicket(ctx, Digest(raw), time.Now().UTC())
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) transactionStore(tx *gorm.DB) (Store, error) {
	if tx == nil {
		return nil, ErrInvalid
	}
	if s == nil || s.store == nil {
		return nil, ErrUnavailable
	}
	store := s.store.WithTx(tx)
	if store == nil {
		return nil, ErrUnavailable
	}
	return store, nil
}
