// 本文件验证品牌迁移失败、锁冲突、重试和取消。
package branding

import (
	"context"
	"errors"
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
			db.Callback().Create().Before("gorm:create").Register("test:migration-failure", func(tx *gorm.DB) {
				if tx.Statement.Table == stage {
					tx.AddError(errors.New("injected migration failure"))
				}
			})
			if err := MigrateBranding(context.Background(), db); err == nil {
				t.Fatal("expected migration failure")
			}
			if db.Migrator().HasTable(&Branding{}) {
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
	if second.Migrator().HasTable(&Branding{}) {
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
	if db.Migrator().HasTable(&Branding{}) {
		t.Fatal("canceled migration left a table")
	}
	if err := MigrateBranding(context.Background(), db); err != nil {
		t.Fatal(err)
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
	if db.Migrator().HasTable(&Branding{}) {
		t.Fatal("取消后不应残留品牌表")
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateBranding(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
