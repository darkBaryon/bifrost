// 本文件验证品牌迁移失败、锁冲突、重试和取消。
package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

func TestBrandingMigrationAtomicFailure(t *testing.T) {
	for _, stage := range []string{"ee_branding", "migrations"} {
		t.Run(stage, func(t *testing.T) {
			db := testDB(t, filepath.Join(t.TempDir(), "config.db"))
			failures := 0
			db.Callback().Create().Before("gorm:create").Register("test:migration-failure", func(tx *gorm.DB) {
				if tx.Statement.Table == stage {
					failures++
					tx.AddError(errors.New("injected migration failure"))
				}
			})
			if err := MigrateBranding(context.Background(), db); err == nil {
				t.Fatal("expected migration failure")
			}
			if failures != 1 {
				t.Fatal("non-busy failure was retried", failures)
			}
			if db.Migrator().HasTable(&brandingRow{}) {
				t.Fatal("DDL survived failed transaction")
			}
			db.Callback().Create().Remove("test:migration-failure")
			if err := MigrateBranding(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			if err := MigrateBranding(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			var count int64
			db.Table("migrations").Where("id = ?", brandingMigrationID).Count(&count)
			if count != 1 {
				t.Fatalf("version count=%d", count)
			}
		})
	}
}

func TestBrandingSQLiteBusyAndRetry(t *testing.T) {
	if os.Getenv("BRANDING_TEST_POSTGRES_DSN") != "" {
		t.Skip("SQLite 专用锁测试")
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.db")
	first := testDB(t, path)
	second := testDB(t, path)
	secondSQL, _ := second.DB()
	secondSQL.SetMaxOpenConns(1)
	if err := second.Exec("PRAGMA busy_timeout=20").Error; err != nil {
		t.Fatal(err)
	}
	firstSQL, _ := first.DB()
	conn, err := firstSQL.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	// 锁被占用时必须明确失败，不能写入成功的迁移记录。
	err = MigrateBranding(ctx, second)
	var sqliteErr sqlite3.Error
	if !errors.As(err, &sqliteErr) || (sqliteErr.Code != sqlite3.ErrBusy && sqliteErr.Code != sqlite3.ErrLocked) {
		t.Fatalf("expected SQLite lock error, got %v", err)
	}
	if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if second.Migrator().HasTable(&brandingRow{}) {
		t.Fatal("busy migration left branding table")
	}
	if err := MigrateBranding(ctx, second); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := second.Table("migrations").Where("id = ?", brandingMigrationID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("retry count %d: %v", count, err)
	}
}

func TestBrandingCanceledMigration(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "config.db"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := MigrateBranding(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled: %v", err)
	}
	if db.Migrator().HasTable(&brandingRow{}) {
		t.Fatal("canceled migration left a table")
	}
	if err := MigrateBranding(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestBrandingMigrationInternalRetry(t *testing.T) {
	if os.Getenv("BRANDING_TEST_POSTGRES_DSN") != "" {
		t.Skip("SQLite 专用重试测试")
	}
	for _, failures := range []int{1, 3} {
		t.Run(fmt.Sprintf("busy-%d", failures), func(t *testing.T) {
			db := testDB(t, filepath.Join(t.TempDir(), "config.db"))
			attempts := 0
			db.Callback().Create().Before("gorm:create").Register("test:transient-busy", func(tx *gorm.DB) {
				if tx.Statement.Table == "ee_branding" {
					attempts++
					if attempts <= failures {
						tx.AddError(sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrBusySnapshot})
					}
				}
			})
			err := MigrateBranding(context.Background(), db)
			if failures == 3 {
				var busy sqlite3.Error
				if attempts != 3 || !errors.As(err, &busy) || db.Migrator().HasTable(&brandingRow{}) {
					t.Fatal("exhausted retry lost failure or left partial migration", attempts, err)
				}
				return
			}
			if err != nil || attempts != 2 {
				t.Fatal("transient lock did not retry the whole transaction", attempts, err)
			}
			if err = MigrateBranding(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"ee_branding", "migrations"} {
				var count int64
				if err = db.Table(table).Count(&count).Error; err != nil || count != 1 {
					t.Fatal("retry duplicated singleton or migration version", table, count, err)
				}
			}
		})
	}
}

func TestBrandingMigrationCancelRetry(t *testing.T) {
	db := testDB(t, filepath.Join(t.TempDir(), "config.db"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempts := 0
	db.Callback().Create().Before("gorm:create").Register("test:cancel-retry", func(tx *gorm.DB) {
		if tx.Statement.Table == "ee_branding" {
			attempts++
			cancel()
			tx.AddError(sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrBusySnapshot})
		}
	})
	if err := MigrateBranding(ctx, db); !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatal("cancel did not stop migration retry", attempts, err)
	}
	if db.Migrator().HasTable(&brandingRow{}) {
		t.Fatal("cancelled retry left partial migration")
	}
}

// PostgreSQL 真实事务锁应阻止并行迁移；等待取消后可重试成功。
func TestBrandingPostgresLockAndRetry(t *testing.T) {
	if os.Getenv("BRANDING_TEST_POSTGRES_DSN") == "" {
		t.Skip("未指定 PostgreSQL 测试库")
	}
	db := testDB(t, filepath.Join(t.TempDir(), "config.db"))
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	defer tx.Rollback()
	if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", brandingAdvisoryLockKey).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := MigrateBranding(ctx, db); err == nil {
		t.Fatal("持锁时迁移不应成功")
	}
	if db.Migrator().HasTable(&brandingRow{}) {
		t.Fatal("取消后不应残留品牌表")
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateBranding(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
