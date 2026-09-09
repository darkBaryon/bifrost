// 本文件验证品牌配置的持久化、合并和事务回滚。
package branding

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	var dialector gorm.Dialector = sqlite.Open(path + "?_busy_timeout=5000&_journal_mode=WAL")
	// 只连接显式指定的测试库；每个测试路径独占一个 schema，重连仍访问同一份数据。
	dsn := os.Getenv("BRANDING_TEST_POSTGRES_DSN")
	if strings.Contains(dsn, "://") {
		t.Fatal("BRANDING_TEST_POSTGRES_DSN 请使用 host=... port=... user=... dbname=... 键值格式，不接受 URL")
	}
	schema := fmt.Sprintf("branding_test_%x", sha256.Sum256([]byte(path)))[:40]
	if dsn != "" {
		dialector = postgres.Open(dsn)
	}
	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if dsn != "" {
		admin := db
		adminSQL, err := admin.DB()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := admin.Exec("DROP SCHEMA IF EXISTS " + schema + " CASCADE").Error; err != nil {
				t.Error(err)
			}
			adminSQL.Close()
		})
		if err := admin.Exec("CREATE SCHEMA IF NOT EXISTS " + schema).Error; err != nil {
			t.Fatal(err)
		}
		db, err = gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{})
		if err != nil {
			t.Fatal(err)
		}
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sql.Close() })
	return db
}

func TestBrandingPersistenceAndMerge(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "config.db")
	db := testDB(t, path)
	if err := MigrateBranding(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := NewBrandingStore(db)
	row, err := s.Read(ctx)
	if err != nil || len(row.Logo) != 0 || len(row.Icon) != 0 {
		t.Fatalf("empty: %v %v", row, err)
	}
	logo := &BrandingAsset{Data: []byte("logo"), MIME: "image/png"}
	icon := &BrandingAsset{Data: []byte("icon"), MIME: "image/jpeg"}
	var wg sync.WaitGroup
	for _, patch := range []BrandingPatch{{Logo: logo}, {Icon: icon}} {
		wg.Add(1)
		go func(p BrandingPatch) {
			defer wg.Done()
			if _, e := s.Update(ctx, p); e != nil {
				t.Error(e)
			}
		}(patch)
	}
	wg.Wait()
	row, err = s.Read(ctx)
	if err != nil || !bytes.Equal(row.Logo, logo.Data) || !bytes.Equal(row.Icon, icon.Data) {
		t.Fatalf("merge lost slot: %+v %v", row, err)
	}
	before := row
	if err := MigrateBranding(ctx, db); err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	sql.Close()
	s = NewBrandingStore(testDB(t, path))
	row, err = s.Read(ctx)
	if err != nil || !bytes.Equal(row.Logo, before.Logo) || row.LogoHash != before.LogoHash || row.IconHash != before.IconHash {
		t.Fatalf("reopen changed data: %+v %v", row, err)
	}
	row, err = s.Update(ctx, BrandingPatch{Icon: &BrandingAsset{}})
	if err != nil || len(row.Icon) != 0 || row.IconHash != "" || !bytes.Equal(row.Logo, logo.Data) {
		t.Fatalf("clear: %+v %v", row, err)
	}
	for i := 0; i < 2; i++ {
		row, err = s.Reset(ctx)
		if err != nil || len(row.Logo) != 0 || len(row.Icon) != 0 || row.LogoHash != "" {
			t.Fatalf("reset: %+v %v", row, err)
		}
	}
}

func TestBrandingReadFailureRollsBackWrite(t *testing.T) {
	ctx := context.Background()
	db := testDB(t, filepath.Join(t.TempDir(), "config.db"))
	if err := MigrateBranding(ctx, db); err != nil {
		t.Fatal(err)
	}
	s := NewBrandingStore(db)
	before, err := s.Update(ctx, BrandingPatch{Logo: &BrandingAsset{Data: []byte("old"), MIME: "image/png"}})
	if err != nil {
		t.Fatal(err)
	}
	db.Callback().Query().Before("gorm:query").Register("test:read-failure", func(tx *gorm.DB) { tx.AddError(errors.New("injected read failure")) })
	if _, err := s.Update(ctx, BrandingPatch{Logo: &BrandingAsset{Data: []byte("new"), MIME: "image/png"}}); err == nil {
		t.Fatal("expected failure")
	}
	db.Callback().Query().Remove("test:read-failure")
	var after Branding
	if err := db.First(&after, 1).Error; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.Logo, after.Logo) || before.LogoHash != after.LogoHash || !before.UpdatedAt.Equal(after.UpdatedAt) {
		t.Fatal("failed update committed")
	}
}
