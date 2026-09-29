// 本文件验证过期清理的事务边界：只清理当前表，未到期的撤销和消费记录继续保留。
package persistence

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/session"
	"gorm.io/gorm"
)

func cleanupRows(t *testing.T, db *gorm.DB) ([]sessionRow, []ticketRow) {
	t.Helper()
	var sessions []sessionRow
	var tickets []ticketRow
	if err := db.Where("id LIKE ?", "seed-%").Order("id").Find(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Where("hash LIKE ?", "seed-%").Order("hash").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	return sessions, tickets
}

func TestCleanupPreservesUnexpiredRowsAndUsesCallerTransaction(t *testing.T) {
	db := sessionDB(t)
	now := time.Now().UTC()
	past := now.Add(-time.Minute)
	sessions := []sessionRow{
		{ID: "seed-alive", TokenHash: "alive", AccountID: "account-a", ExpiresAt: now.Add(time.Hour), CreatedAt: now},
		{ID: "seed-alive-revoked", TokenHash: "alive-revoked", AccountID: "account-a", ExpiresAt: now.Add(time.Hour), CreatedAt: now, RevokedAt: &past},
		{ID: "seed-expired", TokenHash: "expired", AccountID: "account-a", ExpiresAt: past, CreatedAt: past.Add(-time.Hour)},
		{ID: "seed-expired-revoked", TokenHash: "expired-revoked", AccountID: "account-a", ExpiresAt: past, CreatedAt: past.Add(-time.Hour), RevokedAt: &past},
	}
	tickets := []ticketRow{
		{Hash: "seed-alive", SessionID: "seed-alive", ExpiresAt: now.Add(time.Hour)},
		{Hash: "seed-alive-consumed", SessionID: "seed-alive", ExpiresAt: now.Add(time.Hour), ConsumedAt: &past},
		{Hash: "seed-expired", SessionID: "seed-expired", ExpiresAt: past},
		{Hash: "seed-expired-consumed", SessionID: "seed-expired", ExpiresAt: past, ConsumedAt: &past},
	}
	// 直接建行，避免准备数据时提前触发清理。
	if err := db.Create(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	beforeSessions, beforeTickets := cleanupRows(t, db)
	svc := sessionService(t, NewStore(db))
	ctx := context.Background()
	create := func(tx *gorm.DB) error { _, err := svc.Create(ctx, tx, "account-b", 1); return err }
	issue := func(tx *gorm.DB) error { _, err := svc.IssueTicket(ctx, tx, "seed-alive"); return err }
	rollback := errors.New("写入后的业务失败")
	assertRollback := func(write func(*gorm.DB) error) {
		t.Helper()
		wantSessions, wantTickets := persistedRows(t, db)
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := write(tx); err != nil {
				return err
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatalf("事务错误 = %v，期望调用方错误", err)
		}
		afterSessions, afterTickets := persistedRows(t, db)
		if !reflect.DeepEqual(wantSessions, afterSessions) || !reflect.DeepEqual(wantTickets, afterTickets) {
			t.Fatal("清理删除或新写入未随调用方事务回滚")
		}
	}
	assertRollback(create)
	if err := db.Transaction(create); err != nil {
		t.Fatal(err)
	}
	afterSessions, afterTickets := cleanupRows(t, db)
	if !reflect.DeepEqual(afterSessions, beforeSessions[:2]) {
		t.Fatalf("会话清理没有仅删除已过期行：%+v", afterSessions)
	}
	if !reflect.DeepEqual(afterTickets, beforeTickets) {
		t.Fatal("写入会话时不应清理票据表")
	}
	// 再留下一个到期会话，确认签票不会顺带清理另一张表。
	expiredSession := sessionRow{ID: "seed-new-expired", TokenHash: "new-expired", AccountID: "account-a", ExpiresAt: past, CreatedAt: past.Add(-time.Hour)}
	if err := db.Create(&expiredSession).Error; err != nil {
		t.Fatal(err)
	}
	beforeTicketSweepSessions, _ := cleanupRows(t, db)
	assertRollback(issue)
	if err := db.Transaction(issue); err != nil {
		t.Fatal(err)
	}
	afterSessions, afterTickets = cleanupRows(t, db)
	if !reflect.DeepEqual(afterSessions, beforeTicketSweepSessions) || !reflect.DeepEqual(afterTickets, beforeTickets[:2]) {
		t.Fatalf("未到期行（含已撤销/已消费）必须原样保留：%+v / %+v", afterSessions, afterTickets)
	}
}

func TestTicketExpiryBoundaryRejectsConsumption(t *testing.T) {
	db := sessionDB(t)
	at := time.Date(2026, time.September, 29, 12, 0, 0, 0, time.UTC)
	for _, expires := range []time.Time{at.Add(-time.Nanosecond), at} {
		t.Run(expires.Format(time.RFC3339Nano), func(t *testing.T) {
			row := ticketRow{Hash: expires.Format(time.RFC3339Nano), SessionID: "session-a", ExpiresAt: expires}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				id, err := NewStore(db).WithTx(tx).ConsumeTicket(context.Background(), row.Hash, at)
				if !errors.Is(err, session.ErrUnauthorized) || id != "" {
					t.Fatalf("到期票据消费结果 = %q/%v，期望拒绝", id, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			var after ticketRow
			if err := db.First(&after, "hash = ?", row.Hash).Error; err != nil {
				t.Fatal(err)
			}
			if after.ConsumedAt != nil {
				t.Fatal("拒绝消费时不应修改到期票据")
			}
		})
	}
}
