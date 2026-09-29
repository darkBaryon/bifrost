// 本文件装配独立认证、会话与账号服务，供旧账号/密码业务的真实数据库回归使用。
package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/auth/hasher"
	authstore "github.com/darkBaryon/bifrost/ee/internal/auth/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	sessionstore "github.com/darkBaryon/bifrost/ee/internal/session/persistence"
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
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	logs := &recordedLogs{}
	if err = MigrateIdentity(logs)(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return NewStore(db, logs), db
}

type services struct {
	*auth.Service
	*identity.AccountService
	*identity.PasswordService
	sessions *session.Service
	store    *Store
}

func (s services) State(ctx context.Context) (identity.State, error) {
	return s.AccountService.State(ctx)
}

func (s services) RequireAccountManager(ctx context.Context, p session.Principal) error {
	return s.AccountService.RequireAccountManager(ctx, p)
}

func newService(t *testing.T, store *Store) services {
	t.Helper()
	passwords := auth.NewPasswords(hasher.Bcrypt{})
	sessions, err := session.New(sessionstore.NewStore(store.db), session.DefaultTTL)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := identity.New(store, passwords, identity.Options{InitialPassword: "123456", SetupToken: "test-setup-key", SessionTTL: session.DefaultTTL}, nil, sessions)
	if err != nil {
		t.Fatal(err)
	}
	authentication, err := auth.New(store.db, authstore.NewStore(store.db, store.log), sessions, passwords, store.log)
	if err != nil {
		t.Fatal(err)
	}
	return services{Service: authentication, AccountService: accounts.Account, PasswordService: accounts.Password, sessions: sessions, store: store}
}

func fixture(t *testing.T) (services, *Store, *gorm.DB, auth.LoginResult) {
	t.Helper()
	store, db := testStore(t)
	s := newService(t, store)
	ctx := context.Background()
	if _, err := s.Initialize(ctx, "test-setup-key", "admin", adminPassword); err != nil {
		t.Fatal(err)
	}
	issued, err := s.Login(ctx, "admin", adminPassword, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, issued.Token); err != nil {
		t.Fatal(err)
	}
	return s, store, db, issued
}

func requireError(t *testing.T, got, want error) {
	t.Helper()
	// 跨模块的同一公开错误经现有账号错误适配比较，不以字符串相等代替错误类型。
	if !errors.Is(identity.SafeError(got), want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// 这些测试使用账号模块默认的 ChiefPolicy；票据业务组合只留在测试夹具，不恢复旧生产会话壳。
func (s services) IssueTicket(ctx context.Context, claimed session.Principal) (string, error) {
	var raw string
	err := s.store.Transaction(ctx, func(tx identity.Tx) error {
		actual, err := s.Service.RevalidateSession(ctx, tx.DB(), claimed)
		if err != nil {
			return err
		}
		if err := authorizeTestTicket(tx.State(), actual); err != nil {
			return err
		}
		raw, err = s.sessions.IssueTicket(ctx, tx.DB(), actual.SessionID)
		return err
	})
	if err != nil {
		return "", identity.SafeError(err)
	}
	return raw, nil
}

func (s services) ConsumeTicket(ctx context.Context, raw string) (session.Principal, error) {
	var actual session.Principal
	err := s.store.Transaction(ctx, func(tx identity.Tx) error {
		id, err := s.sessions.ConsumeTicket(ctx, tx.DB(), raw)
		if err != nil {
			return err
		}
		actual, err = s.Service.ValidateSessionInTransaction(ctx, tx.DB(), id)
		if err != nil {
			return err
		}
		return authorizeTestTicket(tx.State(), actual)
	})
	return actual, identity.SafeError(err)
}

func authorizeTestTicket(state identity.State, principal session.Principal) error {
	if principal.MustChangePassword || !state.Initialized || principal.AccountID != state.ChiefAccountID {
		return identity.ErrForbidden
	}
	return nil
}
