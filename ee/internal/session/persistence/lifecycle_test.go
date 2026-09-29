// 本文件用真实 SQLite 验证会话与票据加入调用方事务，且拒绝无事务写入。
package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func sessionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "session.db")+"?_busy_timeout=1000"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Migrator().CreateTable(&sessionRow{}, &ticketRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX session_test_token_hash ON ee_identity_sessions(token_hash)").Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func persistedRows(t *testing.T, db *gorm.DB) ([]sessionRow, []ticketRow) {
	t.Helper()
	var sessions []sessionRow
	var tickets []ticketRow
	if err := db.Order("id").Find(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Order("hash").Find(&tickets).Error; err != nil {
		t.Fatal(err)
	}
	return sessions, tickets
}

func sessionService(t *testing.T, store session.Store) *session.Service {
	t.Helper()
	svc, err := session.New(store, session.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func issueSessionAndTicket(t *testing.T, db *gorm.DB, accountID string) (session.Issued, string) {
	t.Helper()
	svc := sessionService(t, NewStore(db))
	var issued session.Issued
	var ticket string
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		issued, err = svc.Create(context.Background(), tx, accountID, 7)
		if err != nil {
			return err
		}
		ticket, err = svc.IssueTicket(context.Background(), tx, issued.Record.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued, ticket
}

func tokenDigest(raw string) string {
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func TestCreateAndIssueTicketJoinCallerTransaction(t *testing.T) {
	db := sessionDB(t)
	svc := sessionService(t, NewStore(db))
	ctx := context.Background()
	rollback := errors.New("调用方业务失败")
	before := time.Now().UTC()
	err := db.Transaction(func(tx *gorm.DB) error {
		issued, err := svc.Create(ctx, tx, "account-a", 7)
		if err != nil {
			return err
		}
		ticket, err := svc.IssueTicket(ctx, tx, issued.Record.ID)
		if err != nil {
			return err
		}
		after := time.Now().UTC()
		sessions, tickets := persistedRows(t, tx)
		if len(sessions) != 1 || len(tickets) != 1 {
			t.Fatalf("事务内行数 = %d/%d，期望 1/1", len(sessions), len(tickets))
		}
		s, w := sessions[0], tickets[0]
		if len(issued.Token) != 43 || len(ticket) != 43 || issued.Token == ticket {
			t.Fatal("会话与票据必须返回独立的 43 字符随机原文")
		}
		if s.TokenHash != tokenDigest(issued.Token) || w.Hash != tokenDigest(ticket) {
			t.Fatal("数据库未保存原文对应的 SHA-256 摘要")
		}
		if s.ID != issued.Record.ID || s.AccountID != "account-a" || s.IssuedAuthVersion != 7 || w.SessionID != s.ID {
			t.Fatalf("会话或票据关联错误：%+v / %+v", s, w)
		}
		if s.ExpiresAt.Before(before.Add(24*time.Hour)) || s.ExpiresAt.After(after.Add(24*time.Hour)) {
			t.Fatalf("默认会话到期时间不在 24 小时范围内：%v", s.ExpiresAt)
		}
		if w.ExpiresAt.Before(before.Add(30*time.Second)) || w.ExpiresAt.After(after.Add(30*time.Second)) {
			t.Fatalf("票据到期时间不在 30 秒范围内：%v", w.ExpiresAt)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("事务错误 = %v，期望调用方错误", err)
	}
	if sessions, tickets := persistedRows(t, db); len(sessions) != 0 || len(tickets) != 0 {
		t.Fatalf("调用方回滚后仍有行：%+v / %+v", sessions, tickets)
	}
}

func TestConsumeTicketJoinsCallerTransaction(t *testing.T) {
	db := sessionDB(t)
	issued, raw := issueSessionAndTicket(t, db, "account-a")
	svc := sessionService(t, NewStore(db))
	rollback := errors.New("连接授权失败")
	consume := func(tx *gorm.DB) error {
		id, err := svc.ConsumeTicket(context.Background(), tx, raw)
		if err == nil && id != issued.Record.ID {
			t.Fatalf("票据指向 %q，期望 %q", id, issued.Record.ID)
		}
		if err != nil && id != "" {
			t.Fatal("消费失败不应返回会话编号")
		}
		return err
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := consume(tx); err != nil {
			return err
		}
		_, tickets := persistedRows(t, tx)
		if tickets[0].ConsumedAt == nil {
			t.Fatal("事务内未标记票据消费")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	_, tickets := persistedRows(t, db)
	if tickets[0].ConsumedAt != nil {
		t.Fatal("调用方回滚后票据仍被标记消费")
	}
	if err := db.Transaction(consume); err != nil {
		t.Fatalf("回滚后重试失败：%v", err)
	}
	if err := db.Transaction(consume); !errors.Is(err, session.ErrUnauthorized) {
		t.Fatalf("重复消费错误 = %v，期望 ErrUnauthorized", err)
	}
	_, tickets = persistedRows(t, db)
	if len(tickets) != 1 || tickets[0].ConsumedAt == nil {
		t.Fatal("已消费但未到期的票据必须保留消费记录")
	}
}

func TestWritesRejectMissingTransaction(t *testing.T) {
	db := sessionDB(t)
	issued, raw := issueSessionAndTicket(t, db, "account-a")
	store := NewStore(db)
	svc := sessionService(t, store)
	ctx := context.Background()
	beforeSessions, beforeTickets := persistedRows(t, db)
	checks := map[string]func() error{
		"创建会话":   func() error { _, err := svc.Create(ctx, nil, "account-b", 1); return err },
		"撤销会话":   func() error { return svc.Revoke(ctx, nil, issued.Record.ID) },
		"撤销账号会话": func() error { return svc.RevokeAccountSessions(ctx, nil, "account-a") },
		"删除账号会话": func() error { return svc.DeleteAccountSessions(ctx, nil, "account-a") },
		"签发票据":   func() error { _, err := svc.IssueTicket(ctx, nil, issued.Record.ID); return err },
		"消费票据":   func() error { _, err := svc.ConsumeTicket(ctx, nil, raw); return err },
	}
	for name, check := range checks {
		if err := check(); !errors.Is(err, session.ErrInvalid) {
			t.Errorf("%s 缺少事务时错误 = %v，期望 ErrInvalid", name, err)
		}
	}
	// 绑定副本不能使根 Store 留下事务，WithTx(nil) 也不能偷偷回到根数据库。
	if err := db.Transaction(func(tx *gorm.DB) error { _ = store.WithTx(tx); return nil }); err != nil {
		t.Fatal(err)
	}
	for _, unbound := range []session.Store{store, store.WithTx(nil)} {
		at := time.Now().UTC()
		errorsToCheck := []error{
			unbound.Insert(ctx, session.Record{ID: "unbound", TokenHash: "unbound", ExpiresAt: at.Add(time.Hour), CreatedAt: at}),
			unbound.Revoke(ctx, issued.Record.ID, at),
			unbound.RevokeAccountSessions(ctx, "account-a", at),
			unbound.DeleteAccountSessions(ctx, "account-a"),
			unbound.InsertTicket(ctx, session.Ticket{Hash: "unbound", SessionID: issued.Record.ID, ExpiresAt: at.Add(time.Hour)}),
		}
		_, consumeErr := unbound.ConsumeTicket(ctx, tokenDigest(raw), at)
		for _, err := range append(errorsToCheck, consumeErr) {
			if !errors.Is(err, session.ErrInvalid) {
				t.Errorf("未绑定 Store 写入错误 = %v，期望 ErrInvalid", err)
			}
		}
	}
	afterSessions, afterTickets := persistedRows(t, db)
	if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeTickets, afterTickets) {
		t.Fatal("无事务调用修改了根数据库")
	}
}

func TestRevokeJoinsCallerTransaction(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "单会话"
		if all {
			name = "账号全部会话"
		}
		t.Run(name, func(t *testing.T) {
			db := sessionDB(t)
			first, _ := issueSessionAndTicket(t, db, "account-a")
			issueSessionAndTicket(t, db, "account-a")
			issueSessionAndTicket(t, db, "account-b")
			svc := sessionService(t, NewStore(db))
			revoke := func(tx *gorm.DB) error {
				if all {
					return svc.RevokeAccountSessions(context.Background(), tx, "account-a")
				}
				return svc.Revoke(context.Background(), tx, first.Record.ID)
			}
			before, beforeTickets := persistedRows(t, db)
			rollback := errors.New("撤销后的业务失败")
			if err := db.Transaction(func(tx *gorm.DB) error {
				if err := revoke(tx); err != nil {
					return err
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
			if after, _ := persistedRows(t, db); !reflect.DeepEqual(before, after) {
				t.Fatal("撤销未随调用方事务回滚")
			}
			if err := db.Transaction(revoke); err != nil {
				t.Fatal(err)
			}
			after, afterTickets := persistedRows(t, db)
			if len(after) != len(before) {
				t.Fatalf("撤销改变了会话行数：%d，期望 %d", len(after), len(before))
			}
			for _, row := range after {
				wantRevoked := row.ID == first.Record.ID || all && row.AccountID == "account-a"
				if (row.RevokedAt != nil) != wantRevoked {
					t.Errorf("会话 %s 撤销状态错误：%v", row.ID, row.RevokedAt)
				}
			}
			if !reflect.DeepEqual(beforeTickets, afterTickets) {
				t.Fatal("撤销会话不应删除票据留存行")
			}
		})
	}
}

func TestDeleteAccountSessionsJoinsCallerTransaction(t *testing.T) {
	db := sessionDB(t)
	issueSessionAndTicket(t, db, "account-a")
	issueSessionAndTicket(t, db, "account-a")
	other, otherTicket := issueSessionAndTicket(t, db, "account-b")
	svc := sessionService(t, NewStore(db))
	beforeSessions, beforeTickets := persistedRows(t, db)
	deleteAccount := func(tx *gorm.DB) error {
		if err := svc.DeleteAccountSessions(context.Background(), tx, "account-a"); err != nil {
			return err
		}
		sessions, tickets := persistedRows(t, tx)
		if len(sessions) != 1 || sessions[0].ID != other.Record.ID || len(tickets) != 1 || tickets[0].Hash != tokenDigest(otherTicket) {
			t.Fatalf("删除账号会话后的剩余行错误：%+v / %+v", sessions, tickets)
		}
		return nil
	}
	rollback := errors.New("删除后的业务失败")
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := deleteAccount(tx); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	afterSessions, afterTickets := persistedRows(t, db)
	if !reflect.DeepEqual(beforeSessions, afterSessions) || !reflect.DeepEqual(beforeTickets, afterTickets) {
		t.Fatal("账号会话与关联票据未一起回滚")
	}
	if err := db.Transaction(deleteAccount); err != nil {
		t.Fatal(err)
	}
	if sessions, tickets := persistedRows(t, db); len(sessions) != 1 || len(tickets) != 1 {
		t.Fatalf("删除提交后行数 = %d/%d，期望 1/1", len(sessions), len(tickets))
	}
}

func TestStorageFailuresDoNotExposeIssuedCredentials(t *testing.T) {
	db := sessionDB(t)
	issued, raw := issueSessionAndTicket(t, db, "account-a")
	svc := sessionService(t, NewStore(db))
	ctx := context.Background()
	// 移除真实表制造 SQL 故障，事务拥有方必须仍能识别驱动错误，但不能收到半成品凭据。
	if err := db.Migrator().DropTable(&sessionRow{}, &ticketRow{}); err != nil {
		t.Fatal(err)
	}
	operations := map[string]func(*gorm.DB) error{
		"创建会话": func(tx *gorm.DB) error {
			out, err := svc.Create(ctx, tx, "account-a", 7)
			if !reflect.DeepEqual(out, session.Issued{}) {
				t.Errorf("创建失败仍返回会话或令牌：%+v", out)
			}
			return err
		},
		"签发票据": func(tx *gorm.DB) error {
			out, err := svc.IssueTicket(ctx, tx, issued.Record.ID)
			if out != "" {
				t.Error("签发失败仍返回票据原文")
			}
			return err
		},
		"消费票据": func(tx *gorm.DB) error {
			out, err := svc.ConsumeTicket(ctx, tx, raw)
			if out != "" {
				t.Error("消费失败仍返回会话编号")
			}
			return err
		},
	}
	for name, operation := range operations {
		var driverError sqlite3.Error
		if err := db.Transaction(operation); !errors.As(err, &driverError) {
			t.Errorf("%s 的 SQL 故障返回 = %v，期望保留 SQLite 驱动错误", name, err)
		}
	}
}
