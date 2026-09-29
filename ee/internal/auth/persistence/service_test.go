// 本文件以真实 SQLite 和会话存储验证登录的事务结果与并发复核，不导入旧身份模块。
package persistence

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	sessionstore "github.com/darkBaryon/bifrost/ee/internal/session/persistence"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type testHasher struct{}

func (testHasher) Hash(p string) (string, error)     { return "test:" + p, nil }
func (testHasher) Compare(h, p string) (bool, error) { return h == "test:"+p, nil }
func (testHasher) ValidHash(h string) bool           { return strings.HasPrefix(h, "test:") }

type testLog struct{ lines []string }

func (l *testLog) Error(format string, args ...any) {
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func fixture(t *testing.T, hasher auth.PasswordHasher) (*auth.Service, *Store, *gorm.DB, *testLog) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "auth.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	if err := db.AutoMigrate(&accountRow{}, &limitRow{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ee_identity_sessions").AutoMigrate(&session.Record{}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE ee_identity_state (id integer PRIMARY KEY, revision integer NOT NULL)",
		"INSERT INTO ee_identity_state (id, revision) VALUES (1, 0)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Add(-time.Hour)
	if err := db.Create(&accountRow{ID: "account", Username: "member", Status: "active", MustChangePassword: true,
		PasswordHash: "test:password", AuthVersion: 1, CreatedAt: at, UpdatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	log := &testLog{}
	store := NewStore(db, log)
	sessions, err := session.New(sessionstore.NewStore(db), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service, err := auth.New(db, store, sessions, hasher, nil)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, db, log
}

func TestLoginLastLoginRollbackAndValidation(t *testing.T) {
	service, store, db, log := fixture(t, testHasher{})
	ctx := context.Background()
	if err := db.Exec("CREATE TRIGGER reject_last_login BEFORE UPDATE OF last_login_at ON ee_identity_accounts BEGIN SELECT RAISE(ABORT, 'private-login-fault'); END").Error; err != nil {
		t.Fatal(err)
	}
	result, err := service.Login(ctx, "member", "password", "peer")
	if err != auth.ErrUnavailable || result.Token != "" {
		t.Fatalf("failed login exposed issued token: %v", err)
	}
	var count int64
	db.Table("ee_identity_sessions").Count(&count)
	account, err := store.AccountByID(ctx, "account")
	if err != nil || count != 0 || account.LastLoginAt != nil {
		t.Fatal("last-login failure did not roll back session")
	}
	if strings.Contains(strings.Join(log.lines, "\n"), "private-login-fault") || len(log.lines) == 0 {
		t.Fatal("unsafe or missing database diagnostic")
	}
	if err := db.Exec("DROP TRIGGER reject_last_login").Error; err != nil {
		t.Fatal(err)
	}
	result, err = service.Login(ctx, "member", "password", "peer")
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.AuthByHash(ctx, session.Digest(result.Token))
	if err != nil || record.Account.ID != "account" || record.Session.ID != result.Principal.SessionID ||
		record.Account.LastLoginAt == nil || !record.Account.LastLoginAt.Equal(*result.Account.LastLoginAt) ||
		result.Account.LastLoginAt.Nanosecond()%int(time.Microsecond) != 0 {
		t.Fatalf("joined login state or time precision changed: %v", err)
	}
	principal, err := service.Authenticate(ctx, result.Token)
	if err != nil || !principal.MustChangePassword {
		t.Fatalf("limited session rejected: %v", err)
	}
	if _, err = service.Me(ctx, principal); err != nil {
		t.Fatal(err)
	}
	if err := database.Write(ctx, db, func(tx *gorm.DB) error {
		if _, err := service.ValidateSessionInTransaction(ctx, tx, principal.SessionID); err != nil {
			return err
		}
		_, err := service.RevalidateSession(ctx, tx, principal)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateSessionInTransaction(ctx, nil, principal.SessionID); err != auth.ErrInvalid {
		t.Fatal("nil transaction accepted")
	}
	if err := service.Logout(ctx, result.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, result.Token); err != auth.ErrUnauthorized {
		t.Fatal("revoked session remained authenticated")
	}
	for _, token := range []string{result.Token, "invalid", strings.Repeat("a", 43)} {
		if err := service.Logout(ctx, token); err != nil {
			t.Fatalf("logout is not idempotent: %v", err)
		}
	}
}

type pausedHasher struct {
	testHasher
	compared chan struct{}
	resume   chan struct{}
}

func (p pausedHasher) Compare(hash, password string) (bool, error) {
	ok, err := p.testHasher.Compare(hash, password)
	close(p.compared)
	<-p.resume
	return ok, err
}

func TestLoginRejectsAccountChangedDuringCompare(t *testing.T) {
	for _, test := range []struct {
		name    string
		changes map[string]any
	}{
		{"version", map[string]any{"auth_version": 2}},
		{"hash", map[string]any{"password_hash": "test:changed"}},
		{"status", map[string]any{"status": "disabled"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			paused := pausedHasher{compared: make(chan struct{}), resume: make(chan struct{})}
			service, _, db, _ := fixture(t, paused)
			ctx := context.Background()
			done := make(chan error, 1)
			go func() { _, err := service.Login(ctx, "member", "password", "peer"); done <- err }()
			select {
			case <-paused.compared:
			case <-time.After(5 * time.Second):
				close(paused.resume)
				t.Fatal("login did not reach password comparison")
			}
			err := database.Write(ctx, db, func(tx *gorm.DB) error {
				return tx.Model(&accountRow{}).Where("id = ?", "account").Updates(test.changes).Error
			})
			close(paused.resume)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != auth.ErrUnauthorized {
				t.Fatalf("changed account received session: %v", err)
			}
			var count int64
			db.Table("ee_identity_sessions").Count(&count)
			if count != 0 {
				t.Fatal("rejected login inserted a session")
			}
		})
	}
}

func TestLoginBucketsRollbackTogether(t *testing.T) {
	_, store, db, _ := fixture(t, testHasher{})
	ctx := context.Background()
	at := time.Now().UTC()
	limits := []auth.LoginLimit{{Key: "ip:first", Max: 3, Window: time.Minute}, {Key: "name:second", Max: 1, Window: time.Minute}}
	reserve := func(at time.Time) error {
		return database.Write(ctx, db, func(tx *gorm.DB) error { return store.WithTx(tx).ReserveLogin(ctx, limits, at) })
	}
	if err := reserve(at); err != nil {
		t.Fatal(err)
	}
	if err := reserve(at.Add(time.Second)); !errors.Is(err, auth.ErrLimited) {
		t.Fatalf("limit not enforced: %v", err)
	}
	var first limitRow
	if err := db.Where("key = ?", limits[0].Key).First(&first).Error; err != nil || first.Count != 1 {
		t.Fatal("later bucket failure consumed earlier bucket")
	}
	if err := reserve(at.Add(time.Minute)); err != nil {
		t.Fatalf("new rate window rejected: %v", err)
	}
	if err := store.ReserveLogin(ctx, limits, at); err != auth.ErrInvalid {
		t.Fatal("unbound login reservation accepted")
	}
	if err := store.WithTx(nil).UpdateLastLogin(ctx, "account", at, at); err != auth.ErrInvalid {
		t.Fatal("nil transaction used main connection")
	}
}
