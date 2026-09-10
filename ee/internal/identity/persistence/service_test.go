// 本文件用真实SQLite/PG事务验证身份操作的最终状态。
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const adminPassword = "Admin-password-1"

func testStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	var db *gorm.DB
	var err error
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	if dsn := os.Getenv("IDENTITY_TEST_POSTGRES_DSN"); dsn != "" {
		schema := fmt.Sprintf("identity_%d", time.Now().UnixNano())
		control, e := gorm.Open(postgres.Open(dsn), cfg)
		if e != nil {
			t.Fatal(e)
		}
		if e = control.Exec("CREATE SCHEMA " + schema).Error; e != nil {
			t.Fatal(e)
		}
		db, err = gorm.Open(postgres.Open(dsn+" search_path="+schema), cfg)
		t.Cleanup(func() { control.Exec("DROP SCHEMA " + schema + " CASCADE"); sql, _ := control.DB(); sql.Close() })
	} else {
		db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity.db")+"?_busy_timeout=1000&_journal_mode=WAL"), cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	sql, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { sql.Close() })
	if err = MigrateIdentity(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return NewStore(db), db
}
func newService(t *testing.T, store *Store) *identity.Service {
	t.Helper()
	s, e := identity.NewService(store, Passwords{}, identity.Options{InitialPassword: "123456", SetupToken: "test-setup-key", SessionTTL: 24 * time.Hour}, nil)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func fixture(t *testing.T) (*identity.Service, *Store, *gorm.DB, identity.IssuedSession) {
	t.Helper()
	store, db := testStore(t)
	s := newService(t, store)
	ctx := context.Background()
	if _, e := s.Initialize(ctx, "test-setup-key", "admin", adminPassword); e != nil {
		t.Fatal(e)
	}
	v, e := s.Login(ctx, "admin", adminPassword, "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Authenticate(ctx, v.Token); e != nil {
		t.Fatal(e)
	}
	return s, store, db, v
}
func requireError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
func TestAccountLifecycle(t *testing.T) {
	s, _, db, admin := fixture(t)
	ctx := context.Background()
	a, e := s.CreateAccount(ctx, admin.Principal, "alice", "")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateAccount(ctx, admin.Principal, "alice", "")
	requireError(t, e, identity.ErrConflict)
	v, e := s.Login(ctx, "alice", "123456", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	if !v.Principal.MustChangePassword {
		t.Fatal("default login is not restricted")
	}
	requireError(t, s.RequireAccountManager(ctx, v.Principal), identity.ErrForbidden)
	requireError(t, s.ChangePassword(ctx, v.Principal, "wrong", "Alice-password-1"), identity.ErrInvalid)
	if e = s.ChangePassword(ctx, v.Principal, "123456", "Alice-password-1"); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, v.Token)
	requireError(t, e, identity.ErrUnauthorized)
	v, e = s.Login(ctx, "alice", "Alice-password-1", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	event, e := s.ResetPassword(ctx, admin.Principal, a.ID, "00000000-0000-4000-8000-000000000001")
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, v.Token)
	requireError(t, e, identity.ErrUnauthorized)
	v, e = s.Login(ctx, "alice", "123456", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.ResetPassword(ctx, admin.Principal, a.ID, event.OperationID)
	if e != nil || again.ID != event.ID {
		t.Fatal("reset retry changed result", e)
	}
	if _, e = s.Authenticate(ctx, v.Token); e != nil {
		t.Fatal("reset retry revoked later session", e)
	}
	if e = s.ChangePassword(ctx, v.Principal, "123456", "Alice-password-2"); e != nil {
		t.Fatal(e)
	}
	v, e = s.Login(ctx, "alice", "Alice-password-2", "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	events, e := s.ListPasswordEvents(ctx, v.Principal, "", "", 1)
	if e != nil || len(events.Items) != 1 || events.NextCursor == nil {
		t.Fatal("event pagination", events, e)
	}
	_, e = s.ListPasswordEvents(ctx, v.Principal, admin.Principal.AccountID, "", 20)
	requireError(t, e, identity.ErrForbidden)
	body, _ := json.Marshal(events)
	if strings.Contains(string(body), "password_hash") || strings.Contains(string(body), "123456") {
		t.Fatal("secret serialized")
	}
	if e = s.SetAccountStatus(ctx, admin.Principal, a.ID, "disabled"); e != nil {
		t.Fatal(e)
	}
	if e = s.SetAccountStatus(ctx, admin.Principal, a.ID, "active"); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, v.Token)
	requireError(t, e, identity.ErrUnauthorized)
	requireError(t, s.SetAccountStatus(ctx, admin.Principal, admin.Principal.AccountID, "disabled"), identity.ErrForbidden)
	var rows []sessionRow
	db.Find(&rows)
	for _, r := range rows {
		if r.TokenHash == admin.Token || len(r.TokenHash) != 64 {
			t.Fatal("raw token stored")
		}
	}
}
func TestPasswordEventRollback(t *testing.T) {
	s, store, db, p := fixture(t)
	ctx := context.Background()
	a, e := s.CreateAccount(ctx, p.Principal, "alice", "")
	if e != nil {
		t.Fatal(e)
	}
	before, _ := store.CredentialByName(ctx, "alice")
	db.Callback().Create().Before("gorm:create").Register("fail:events", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_identity_password_events" {
			tx.AddError(errors.New("injected"))
		}
	})
	_, e = s.ResetPassword(ctx, p.Principal, a.ID, "00000000-0000-4000-8000-000000000002")
	requireError(t, e, identity.ErrUnavailable)
	after, _ := store.CredentialByName(ctx, "alice")
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion {
		t.Fatal("password survived audit failure")
	}
	db.Callback().Create().Remove("fail:events")
	ev, e := s.ResetPassword(ctx, p.Principal, a.ID, "00000000-0000-4000-8000-000000000002")
	if e != nil || ev.Result != "success" {
		t.Fatal(e)
	}
}
func TestInitializationAndLegacy(t *testing.T) {
	store, _ := testStore(t)
	s := newService(t, store)
	ctx := context.Background()
	_, e := s.Initialize(ctx, "wrong", "admin", adminPassword)
	requireError(t, e, identity.ErrForbidden)
	hash, e := (Passwords{}).Hash("short")
	if e != nil {
		t.Fatal(e)
	}
	legacy := &identity.Credential{Account: identity.Account{Username: "old admin!"}, PasswordHash: hash}
	if e = s.BootstrapLegacy(ctx, legacy); e != nil {
		t.Fatal(e)
	}
	legacy.PasswordHash = "invalid"
	if e = s.BootstrapLegacy(ctx, legacy); e != nil {
		t.Fatal("reimported changed legacy", e)
	}
	v, e := s.Login(ctx, "old admin!", "short", "peer")
	if e != nil || v.Principal.MustChangePassword {
		t.Fatal("legacy password changed", e)
	}
	_, e = s.Initialize(ctx, "test-setup-key", "other", adminPassword)
	requireError(t, e, identity.ErrConflict)
}
func TestTicketRecoveryAndLogout(t *testing.T) {
	s, _, _, p := fixture(t)
	ctx := context.Background()
	raw, e := s.IssueTicket(ctx, p.Principal)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ConsumeTicket(ctx, raw); e != nil {
		t.Fatal(e)
	}
	_, e = s.ConsumeTicket(ctx, raw)
	requireError(t, e, identity.ErrUnauthorized)
	if e = s.RecoverAdmin(ctx, "Recovered-password-1"); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, p.Token)
	requireError(t, e, identity.ErrUnauthorized)
	p, e = s.Login(ctx, "admin", "Recovered-password-1", "peer")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(ctx, p.Token); e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(ctx, p.Token); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, p.Token)
	requireError(t, e, identity.ErrUnauthorized)
}
func TestRateLimitAndFailedAudit(t *testing.T) {
	s, _, _, p := fixture(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, e := s.Login(ctx, "unknown", "invalid", "peer")
		requireError(t, e, identity.ErrUnauthorized)
	}
	_, e := s.Login(ctx, "unknown", "invalid", "another-peer")
	requireError(t, e, identity.ErrLimited)
	event, e := s.ResetPassword(ctx, p.Principal, p.Principal.AccountID, "00000000-0000-4000-8000-000000000003")
	requireError(t, e, identity.ErrForbidden)
	if event.Result != "failure" || event.ReasonCode != "forbidden" {
		t.Fatal("missing failure event")
	}
}
func TestConcurrentInitialize(t *testing.T) {
	store, _ := testStore(t)
	s := newService(t, store)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Initialize(ctx, "test-setup-key", "admin", adminPassword)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		} else {
			requireError(t, e, identity.ErrConflict)
		}
	}
	if successes != 1 {
		t.Fatalf("successes=%d", successes)
	}
}
