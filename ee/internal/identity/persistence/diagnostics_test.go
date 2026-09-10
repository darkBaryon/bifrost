// 本文件验证故障诊断可关联且不泄露凭据，以及SQLite迁移重试的事务边界。
package persistence

import (
	"bytes"
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func captureDiagnostics(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	old := log.Writer()
	log.SetOutput(&b)
	t.Cleanup(func() { log.SetOutput(old) })
	return &b
}
func TestSafeDiagnostics(t *testing.T) {
	s, _, db, admin := fixture(t)
	output := captureDiagnostics(t)
	ctx := identity.WithDiagnosticOperation(context.Background(), "identity.change-password")
	_, id := identity.DiagnosticOperation(ctx)
	secret := "credential-that-must-never-be-logged"
	db.Callback().Create().Before("gorm:create").Register("fail:diagnostics", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_identity_password_events" {
			tx.AddError(errors.New(secret))
		}
	})
	requireError(t, s.ChangePassword(ctx, admin.Principal, adminPassword, "Changed-password-1"), identity.ErrUnavailable)
	db.Callback().Create().Remove("fail:diagnostics")
	line := output.String()
	for _, want := range []string{"operation=identity.change-password", "stage=password_event.insert", "incident_id=" + id, "kind=driver_error"} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing diagnostic field %s", want)
		}
	}
	for _, forbidden := range []string{secret, admin.Token, adminPassword, "Changed-password-1", "INSERT INTO", "password_hash"} {
		if strings.Contains(line, forbidden) {
			t.Fatal("diagnostic contains credential or SQL")
		}
	}
	output.Reset()
	sql, _ := db.DB()
	sql.Close()
	_, err := s.Authenticate(ctx, admin.Token)
	requireError(t, err, identity.ErrUnavailable)
	if !strings.Contains(output.String(), "stage=session.read") || !strings.Contains(output.String(), "code=closed") {
		t.Fatal("query failure not diagnosed")
	}
}
func TestMigrationRetriesSQLiteBusyAtomically(t *testing.T) {
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "busy.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	attempts := 0
	db.Callback().Create().Before("gorm:create").Register("fail:busy-once", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_identity_state" {
			attempts++
			if attempts == 1 {
				tx.AddError(sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrBusySnapshot})
			}
		}
	})
	if e = MigrateIdentity(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	if attempts != 2 {
		t.Fatalf("expected a full retry, attempts=%d", attempts)
	}
	state, e := NewStore(db).State(context.Background())
	if e != nil || state.Initialized {
		t.Fatal("partial or duplicate initialization", e)
	}
	if e = MigrateIdentity(context.Background(), db); e != nil {
		t.Fatal("migration not idempotent", e)
	}
	if attempts != 2 {
		t.Fatal("completed migration repeated")
	}
}

func TestMigrationBusyExhaustionPreservesFailure(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "exhausted.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	output := captureDiagnostics(t)
	attempts := 0
	db.Callback().Create().Before("gorm:create").Register("fail:always-busy", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_identity_state" {
			attempts++
			tx.AddError(sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrBusySnapshot})
		}
	})
	ctx := identity.WithDiagnosticOperation(context.Background(), "identity.bootstrap")
	if err = MigrateIdentity(ctx, db); !sqliteBusy(err) || attempts != 3 {
		t.Fatalf("failure lost or retries unbounded: attempts=%d", attempts)
	}
	if db.Migrator().HasTable(&accountRow{}) || db.Migrator().HasTable(&stateRow{}) {
		t.Fatal("failed migration left partial identity tables")
	}
	for _, field := range []string{"operation=identity.bootstrap", "stage=migration", "kind=sqlite", "code=5/517"} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("missing safe migration diagnostic %s", field)
		}
	}
}
