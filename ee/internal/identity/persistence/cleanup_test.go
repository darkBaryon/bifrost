// 本文件验证 FIND-020：写入新会话/票据时删除已到期的行，未到期的行（含已撤销、已消费）原样保留。
package persistence

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestExpiredRowsSweptOnInsert(t *testing.T) {
	s, _, db, admin := fixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	// 直接写行，绕过 InsertSession/InsertTicket 自带的清理，让下面的登录与签票成为唯一触发者。
	for _, r := range []sessionRow{
		{ID: "00000000-0000-4000-8000-00000000e001", TokenHash: "expired", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-25 * time.Hour)},
		{ID: "00000000-0000-4000-8000-00000000e002", TokenHash: "expired-revoked", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(-time.Hour), CreatedAt: now.Add(-25 * time.Hour), RevokedAt: &past},
		{ID: "00000000-0000-4000-8000-00000000a001", TokenHash: "alive", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		{ID: "00000000-0000-4000-8000-00000000a002", TokenHash: "alive-revoked", AccountID: admin.Account.ID, IssuedAuthVersion: 1, ExpiresAt: now.Add(time.Hour), CreatedAt: now, RevokedAt: &past},
	} {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range []ticketRow{
		{Hash: "ticket-expired", SessionID: admin.Principal.SessionID, ExpiresAt: now.Add(-time.Minute)},
		{Hash: "ticket-expired-consumed", SessionID: admin.Principal.SessionID, ExpiresAt: now.Add(-time.Minute), ConsumedAt: &past},
		{Hash: "ticket-alive", SessionID: admin.Principal.SessionID, ExpiresAt: now.Add(time.Minute)},
		{Hash: "ticket-alive-consumed", SessionID: admin.Principal.SessionID, ExpiresAt: now.Add(time.Minute), ConsumedAt: &past},
	} {
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	sessions := func(hashes ...string) []sessionRow {
		var rows []sessionRow
		if err := db.Where("token_hash IN ?", hashes).Order("token_hash").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	tickets := func(hashes ...string) []ticketRow {
		var rows []ticketRow
		if err := db.Where("hash IN ?", hashes).Order("hash").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	expiredSessions := []string{"expired", "expired-revoked"}
	aliveSessions := []string{"alive", "alive-revoked"}
	expiredTickets := []string{"ticket-expired", "ticket-expired-consumed"}
	aliveTickets := []string{"ticket-alive", "ticket-alive-consumed"}
	// 触发前：过期行确实在库里，未过期行整行留底。
	if n := len(sessions(expiredSessions...)); n != 2 {
		t.Fatalf("seed: expired sessions present = %d, want 2", n)
	}
	if n := len(tickets(expiredTickets...)); n != 2 {
		t.Fatalf("seed: expired tickets present = %d, want 2", n)
	}
	aliveSessionsBefore, aliveTicketsBefore := sessions(aliveSessions...), tickets(aliveTickets...)
	if len(aliveSessionsBefore) != 2 || len(aliveTicketsBefore) != 2 {
		t.Fatalf("seed: unexpired rows = %d sessions, %d tickets, want 2 and 2", len(aliveSessionsBefore), len(aliveTicketsBefore))
	}
	// 一次登录写入新会话，触发会话清理；一次签票触发票据清理。
	if _, err := s.Login(ctx, "admin", adminPassword, "peer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueTicket(ctx, admin.Principal); err != nil {
		t.Fatal(err)
	}
	if rows := sessions(expiredSessions...); len(rows) != 0 {
		t.Fatalf("expired sessions remain: %+v", rows)
	}
	if rows := tickets(expiredTickets...); len(rows) != 0 {
		t.Fatalf("expired tickets remain: %+v", rows)
	}
	if after := sessions(aliveSessions...); !reflect.DeepEqual(after, aliveSessionsBefore) {
		t.Fatalf("unexpired sessions changed:\n before %+v\n after  %+v", aliveSessionsBefore, after)
	}
	if after := tickets(aliveTickets...); !reflect.DeepEqual(after, aliveTicketsBefore) {
		t.Fatalf("unexpired tickets changed:\n before %+v\n after  %+v", aliveTicketsBefore, after)
	}
}
