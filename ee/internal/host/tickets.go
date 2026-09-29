// 本文件组织控制台 WebSocket 票据的签发和消费，确保票据写入、身份复验及通知权限共用一个事务。
package host

import (
	"context"
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"gorm.io/gorm"
)

// Tickets 是控制台握手票据的业务入口；不持有 HTTP 请求，不将角色规则放进底层会话存储。
type Tickets struct {
	db             *gorm.DB
	authentication *auth.Service
	sessions       *session.Service
	permissions    *rbac.Service
	log            database.Logger
}

// NewTickets 显式绑定事务连接、认证、会话及权限服务；缺少依赖时请求返回不可用。
func NewTickets(db *gorm.DB, authentication *auth.Service, sessions *session.Service, permissions *rbac.Service, log database.Logger) *Tickets {
	return &Tickets{db: db, authentication: authentication, sessions: sessions, permissions: permissions, log: log}
}

// IssueTicket 在同一写事务复验身份与通知权限后签发票据；任一步失败都不提交票据。
func (t *Tickets) IssueTicket(ctx context.Context, claimed session.Principal) (string, error) {
	if !t.ready() {
		return "", auth.ErrUnavailable
	}
	var ticket string
	err := database.Write(ctx, t.db, func(tx *gorm.DB) error {
		principal, err := t.authentication.RevalidateSession(ctx, tx, claimed)
		if err != nil {
			return err
		}
		if err := t.authorize(ctx, tx, principal); err != nil {
			return err
		}
		ticket, err = t.sessions.IssueTicket(ctx, tx, principal.SessionID)
		return err
	})
	if err != nil {
		return "", t.transactionError(ctx, "ticket.issue", err)
	}
	return ticket, nil
}

// ConsumeTicket 在同一写事务消费票据、复验身份及通知权限；校验失败时回滚消费标记，票据仍可在有效期内重试。
func (t *Tickets) ConsumeTicket(ctx context.Context, raw string) (session.Principal, error) {
	if !session.ValidToken(raw) {
		return session.Principal{}, auth.ErrUnauthorized
	}
	if !t.ready() {
		return session.Principal{}, auth.ErrUnavailable
	}
	var principal session.Principal
	err := database.Write(ctx, t.db, func(tx *gorm.DB) error {
		id, err := t.sessions.ConsumeTicket(ctx, tx, raw)
		if err != nil {
			return err
		}
		principal, err = t.authentication.ValidateSessionInTransaction(ctx, tx, id)
		if err != nil {
			return err
		}
		return t.authorize(ctx, tx, principal)
	})
	if err != nil {
		return session.Principal{}, t.transactionError(ctx, "ticket.consume", err)
	}
	return principal, nil
}

func (t *Tickets) ready() bool {
	return t != nil && t.db != nil && t.authentication != nil && t.sessions != nil && t.permissions != nil
}

func (t *Tickets) authorize(ctx context.Context, tx *gorm.DB, principal session.Principal) error {
	subject := rbac.Subject{AccountID: principal.AccountID, SessionID: principal.SessionID, AuthVersion: principal.AuthVersion}
	return t.permissions.AuthorizeInTransaction(ctx, tx, subject, rbac.NotificationsView)
}

func (t *Tickets) transactionError(ctx context.Context, stage string, err error) error {
	safe := ticketError(err)
	if errors.Is(safe, auth.ErrUnavailable) || errors.Is(safe, session.ErrUnavailable) {
		database.LogFailure(t.log, ctx, "console", stage, err)
	}
	return safe
}

// ticketError 统一票据入口的安全错误；存储故障不输出 SQL，角色拒绝保留认证端点的既有状态。
func ticketError(err error) error {
	switch {
	case errors.Is(err, rbac.ErrUnauthorized):
		return auth.ErrUnauthorized
	case errors.Is(err, rbac.ErrForbidden):
		return auth.ErrForbidden
	case errors.Is(err, rbac.ErrInvalid):
		return auth.ErrInvalid
	default:
		return auth.SafeError(err)
	}
}
