// 本文件验证 FIND-020：写入新会话/票据时删除已到期的行，未到期的行（含已撤销、已消费）原样保留。
package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
)

func TestExpiredRowsSweptOnInsert(t *testing.T) {
	s, store, db, admin := fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	revoked := now.Add(-time.Minute)
	seed := func(tx identity.Tx) error {
		for _, v := range []identity.Session{
			{ID: "00000000-0000-4000-8000-00000000e001", TokenHash: "expired", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-25 * time.Hour)},
			{ID: "00000000-0000-4000-8000-00000000e002", TokenHash: "expired-revoked", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-25 * time.Hour), RevokedAt: &revoked},
			{ID: "00000000-0000-4000-8000-00000000a001", TokenHash: "alive", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
			{ID: "00000000-0000-4000-8000-00000000a002", TokenHash: "alive-revoked", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now, RevokedAt: &revoked},
		} {
			if err := tx.InsertSession(v); err != nil {
				return err
			}
		}
		for _, v := range []identity.Ticket{
			{Hash: "ticket-expired", SessionID: admin.Principal.SessionID, ExpiresAt: now.Add(-time.Minute)},
			{Hash: "ticket-alive", SessionID: admin.Principal.SessionID, ExpiresAt: now.Add(time.Minute)},
		} {
			if err := tx.InsertTicket(v); err != nil {
				return err
			}
		}
		return nil
	}
	if err := store.Transaction(ctx, seed); err != nil {
		t.Fatal(err)
	}
	// 一次登录写入新会话，触发会话清理；一次签票触发票据清理。
	if _, err := s.Login(ctx, "admin", adminPassword, "peer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueTicket(ctx, admin.Principal); err != nil {
		t.Fatal(err)
	}
	count := func(model any, where string, args ...any) int64 {
		var n int64
		if err := db.Model(model).Where(where, args...).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(&sessionRow{}, "token_hash IN ?", []string{"expired", "expired-revoked"}); n != 0 {
		t.Fatalf("expired sessions remain: %d", n)
	}
	if n := count(&sessionRow{}, "token_hash IN ?", []string{"alive", "alive-revoked"}); n != 2 {
		t.Fatalf("unexpired sessions swept: %d remain, want 2", n)
	}
	if n := count(&ticketRow{}, "hash = ?", "ticket-expired"); n != 0 {
		t.Fatalf("expired ticket remains: %d", n)
	}
	if n := count(&ticketRow{}, "hash = ?", "ticket-alive"); n != 1 {
		t.Fatalf("unexpired ticket swept: %d remain, want 1", n)
	}
}
