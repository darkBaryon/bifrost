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
		db, err = gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "identity.db")+"?_busy_timeout=1000&_journal_mode=WAL&_foreign_keys=on"), cfg)
	}
	if err != nil {
		t.Fatal(err)
	}
	sql, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { sql.Close() })
	logs := &recordedLogs{}
	if err = MigrateIdentity(logs)(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return NewStore(db, logs), db
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
	if e != nil || len(events.Items) != 1 || events.NextCursor == "" {
		t.Fatal("event pagination", events, e)
	}
	_, e = s.ListPasswordEvents(ctx, v.Principal, admin.Principal.AccountID, "", 20)
	requireError(t, e, identity.ErrForbidden)
	body, _ := json.Marshal(events)
	if strings.Contains(string(body), "password_hash") || strings.Contains(string(body), "123456") {
		t.Fatal("secret serialized")
	}
	if _, e = s.SetAccountStatus(ctx, admin.Principal, a.ID, identity.StatusDisabled); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetAccountStatus(ctx, admin.Principal, a.ID, identity.StatusActive); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, v.Token)
	requireError(t, e, identity.ErrUnauthorized)
	_, e = s.SetAccountStatus(ctx, admin.Principal, admin.Principal.AccountID, identity.StatusDisabled)
	requireError(t, e, identity.ErrForbidden)
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
	before, _ := store.RecordByName(ctx, "alice")
	db.Callback().Create().Before("gorm:create").Register("fail:events", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_identity_password_events" {
			tx.AddError(errors.New("injected"))
		}
	})
	_, e = s.ResetPassword(ctx, p.Principal, a.ID, "00000000-0000-4000-8000-000000000002")
	requireError(t, e, identity.ErrUnavailable)
	after, _ := store.RecordByName(ctx, "alice")
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion {
		t.Fatal("password survived audit failure")
	}
	db.Callback().Create().Remove("fail:events")
	ev, e := s.ResetPassword(ctx, p.Principal, a.ID, "00000000-0000-4000-8000-000000000002")
	if e != nil || ev.Result != identity.ResultSuccess {
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
	if e = s.BootstrapLegacy(ctx, "old admin!", hash); e != nil {
		t.Fatal(e)
	}
	if e = s.BootstrapLegacy(ctx, "old admin!", "invalid"); e != nil {
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
	if event.Result != identity.ResultFailure || event.ReasonCode != identity.ErrForbidden.Error() {
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

// 暂停已完成密码比较的登录，确保重置提交后不会签发旧版本会话。
type pausedPasswords struct {
	Passwords
	compared chan struct{}
	resume   chan struct{}
}

func (p pausedPasswords) Compare(h, raw string) (bool, error) {
	ok, e := p.Passwords.Compare(h, raw)
	close(p.compared)
	<-p.resume
	return ok, e
}
func TestLoginRacingReset(t *testing.T) {
	s, store, _, admin := fixture(t)
	ctx := context.Background()
	a, e := s.CreateAccount(ctx, admin.Principal, "alice", "")
	if e != nil {
		t.Fatal(e)
	}
	p := pausedPasswords{Passwords: Passwords{}, compared: make(chan struct{}), resume: make(chan struct{})}
	login, e := identity.NewService(store, p, identity.Options{InitialPassword: "123456", SessionTTL: time.Hour}, nil)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { _, e := login.Login(ctx, "alice", "123456", "peer"); done <- e }()
	<-p.compared
	_, e = s.ResetPassword(ctx, admin.Principal, a.ID, "00000000-0000-4000-8000-000000000010")
	close(p.resume)
	if e != nil {
		t.Fatal(e)
	}
	requireError(t, <-done, identity.ErrUnauthorized)
}
func TestExpiredCredentialsAndDatabaseFailure(t *testing.T) {
	s, _, db, admin := fixture(t)
	ctx := context.Background()
	ticket, e := s.IssueTicket(ctx, admin.Principal)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Model(&ticketRow{}).Where("1=1").Update("expires_at", time.Now().UTC().Add(-time.Hour)).Error; e != nil {
		t.Fatal(e)
	}
	_, e = s.ConsumeTicket(ctx, ticket)
	requireError(t, e, identity.ErrUnauthorized)
	if e = db.Model(&sessionRow{}).Where("id=?", admin.Principal.SessionID).Update("expires_at", time.Now().UTC().Add(-time.Hour)).Error; e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(ctx, admin.Token)
	requireError(t, e, identity.ErrUnauthorized)
	sql, _ := db.DB()
	sql.Close()
	_, e = s.Authenticate(ctx, admin.Token)
	requireError(t, e, identity.ErrUnavailable)
	_, e = s.Login(ctx, "admin", adminPassword, "peer")
	requireError(t, e, identity.ErrUnavailable)
}
func TestPasswordValidationAndIndependentSalt(t *testing.T) {
	s, store, _, admin := fixture(t)
	ctx := context.Background()
	for _, name := range []string{"alice", "bob"} {
		if _, e := s.CreateAccount(ctx, admin.Principal, name, ""); e != nil {
			t.Fatal(e)
		}
	}
	a, _ := store.RecordByName(ctx, "alice")
	b, _ := store.RecordByName(ctx, "bob")
	if a.PasswordHash == b.PasswordHash {
		t.Fatal("default passwords share hash")
	}
	for _, password := range []string{"123456", adminPassword, strings.Repeat("界", 25), strings.Repeat("a", 73), "short", string([]byte{255})} {
		requireError(t, s.ChangePassword(ctx, admin.Principal, adminPassword, password), identity.ErrInvalid)
	}
	if _, e := s.Authenticate(ctx, admin.Token); e != nil {
		t.Fatal("invalid password changed session", e)
	}
}
func TestTransactionAndMigrationRollback(t *testing.T) {
	_, store, db, admin := fixture(t)
	ctx := context.Background()
	injected := errors.New("forced rollback before commit")
	e := store.Transaction(ctx, func(tx identity.Tx) error {
		c, e := tx.Account(admin.Principal.AccountID)
		if e != nil {
			return e
		}
		c.AuthVersion++
		if e = tx.SaveAccount(c); e != nil {
			return e
		}
		return injected
	})
	if !errors.Is(e, injected) {
		t.Fatal(e)
	}
	c, e := store.RecordByName(ctx, "admin")
	if e != nil || c.AuthVersion != admin.Principal.AuthVersion {
		t.Fatal("transaction was not rolled back", e)
	}
	// 回滚专用迁移夹具：删除自己的身份表，预置冲突表使迁移在中途失败。
	for _, table := range []string{"ee_identity_login_limits", "ee_identity_ws_tickets", "ee_identity_password_events", "ee_identity_sessions", "ee_identity_state", "ee_identity_accounts"} {
		if e = db.Exec("DROP TABLE " + table).Error; e != nil {
			t.Fatal(e)
		}
	}
	if e = db.Exec("DELETE FROM migrations WHERE id=?", identityMigrationID).Error; e != nil {
		t.Fatal(e)
	}
	if e = db.Exec("CREATE TABLE ee_identity_sessions (sentinel integer)").Error; e != nil {
		t.Fatal(e)
	}
	if e = MigrateIdentity(store.log)(ctx, db); e == nil {
		t.Fatal("broken migration succeeded")
	}
	if db.Migrator().HasTable("ee_identity_accounts") || db.Migrator().HasTable("ee_identity_state") {
		t.Fatal("partial DDL survived migration failure")
	}
	var count int64
	if e = db.Table("migrations").Where("id=?", identityMigrationID).Count(&count).Error; e != nil || count != 0 {
		t.Fatal("failed migration version persisted", e)
	}
	if e = db.Exec("DROP TABLE ee_identity_sessions").Error; e != nil {
		t.Fatal(e)
	}
	if e = MigrateIdentity(store.log)(ctx, db); e != nil {
		t.Fatal("migration cannot recover", e)
	}
}

func TestDeferredCommitFailure(t *testing.T) {
	s, store, db, admin := fixture(t)
	output := captureDiagnostics(store)
	ctx := context.Background()
	for _, sql := range []string{"CREATE TABLE failure_parent (id integer PRIMARY KEY)", "CREATE TABLE failure_child (parent_id integer REFERENCES failure_parent(id) DEFERRABLE INITIALLY DEFERRED)"} {
		if e := db.Exec(sql).Error; e != nil {
			t.Fatal(e)
		}
	}
	before, e := store.RecordByName(ctx, "admin")
	if e != nil {
		t.Fatal(e)
	}
	injected := false
	db.Callback().Create().After("gorm:create").Register("fail:commit", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_identity_password_events" {
			// 新Statement仍复用当前事务，避免继承Create的绑定参数；PG须真正延迟到COMMIT失败。
			if e := tx.Session(&gorm.Session{NewDB: true}).Exec("INSERT INTO failure_child(parent_id) VALUES (1)").Error; e != nil {
				tx.AddError(e)
			} else {
				injected = true
			}
		}
	})
	e = s.ChangePassword(ctx, admin.Principal, adminPassword, "Updated-password-2")
	requireError(t, e, identity.ErrUnavailable)
	if !injected {
		t.Fatal("fixture failed before deferred constraint was inserted")
	}
	if !strings.Contains(output.String(), "stage=transaction.commit") {
		t.Fatal("commit failure diagnosis missing: " + output.String())
	}
	db.Callback().Create().Remove("fail:commit")
	after, e := store.RecordByName(ctx, "admin")
	if e != nil {
		t.Fatal(e)
	}
	if before.PasswordHash != after.PasswordHash || before.AuthVersion != after.AuthVersion {
		t.Fatal("COMMIT failure persisted password")
	}
	if _, e = s.Authenticate(ctx, admin.Token); e != nil {
		t.Fatal("COMMIT failure revoked original session", e)
	}
	events, e := s.ListPasswordEvents(ctx, admin.Principal, "", "", 20)
	if e != nil || len(events.Items) != 0 {
		t.Fatal("COMMIT failure persisted success event", e)
	}
}

func TestLegacyRejectsMalformedBcrypt(t *testing.T) {
	store, _ := testStore(t)
	s := newService(t, store)
	ctx := context.Background()
	for _, hash := range []string{"plaintext", "$2a$10$" + strings.Repeat("!", 53), "$2z$10$" + strings.Repeat("a", 53), "$2a$10$" + strings.Repeat("a", 54)} {
		requireError(t, s.BootstrapLegacy(ctx, "admin", hash), identity.ErrInvalid)
	}
	state, e := s.State(ctx)
	if e != nil || state.Initialized {
		t.Fatal("bad legacy input initialized identity", e)
	}
}
