// 本文件验证迁移幂等、未配置语义、乐观锁版本与重置。
package persistence

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "config.db")+"?_busy_timeout=5000"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { sql.Close() })
	for range 2 { // 迁移幂等
		if err := Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(db)
}

func TestVersionedUpdates(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	if _, err := s.Read(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty table: %v", err)
	}
	if _, err := s.Update(ctx, `{"a":1}`, 3); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("first write must use version %d: %v", UnsetVersion, err)
	}
	row, err := s.Update(ctx, `{"a":1}`, UnsetVersion)
	if err != nil || row.Version != 1 || row.Config != `{"a":1}` {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	if _, err := s.Update(ctx, `{"a":2}`, 0); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale version accepted: %v", err)
	}
	row, err = s.Update(ctx, `{"a":2}`, 1)
	if err != nil || row.Version != 2 {
		t.Fatalf("row=%+v err=%v", row, err)
	}
	got, err := s.Read(ctx)
	if err != nil || got.Version != 2 || got.Config != `{"a":2}` {
		t.Fatalf("row=%+v err=%v", got, err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(ctx); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("after reset: %v", err)
	}
	if err := s.Reset(ctx); err != nil {
		t.Fatalf("reset must be idempotent: %v", err)
	}
	if row, err := s.Update(ctx, `{"a":3}`, UnsetVersion); err != nil || row.Version != 1 {
		t.Fatalf("version restarts after reset: row=%+v err=%v", row, err)
	}
}
