// 本文件用迁移前代码生成的 SQLite 快照验证目录与模型拆分不会改变已有数据。
package persistence

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLegacyDatabaseCompatibility(t *testing.T) {
	if os.Getenv("BRANDING_TEST_POSTGRES_DSN") != "" {
		t.Skip("历史快照是 SQLite 格式，PostgreSQL 使用既有存储与迁移测试")
	}
	db := testDB(t, filepath.Join(t.TempDir(), "legacy.db"))
	fixture, err := os.ReadFile("testdata/legacy.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(string(fixture)).Error; err != nil {
		t.Fatal(err)
	}
	var before, after brandingRow
	if err := db.First(&before, 1).Error; err != nil {
		t.Fatal(err)
	}
	var schemaBefore, schemaAfter string
	db.Raw("SELECT sql FROM sqlite_master WHERE name = 'ee_branding'").Scan(&schemaBefore)
	if err := MigrateBranding(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&after, 1).Error; err != nil {
		t.Fatal(err)
	}
	db.Raw("SELECT sql FROM sqlite_master WHERE name = 'ee_branding'").Scan(&schemaAfter)
	if !reflect.DeepEqual(before, after) || schemaBefore != schemaAfter {
		t.Fatal("migration changed old schema, image bytes, hashes or timestamp")
	}
	settings, err := NewBrandingStore(db).Read(context.Background())
	if err != nil || !reflect.DeepEqual(settings, before.settings()) {
		t.Fatal("row-to-domain conversion changed old settings")
	}
	var versions []string
	if err := db.Table("migrations").Pluck("id", &versions).Error; err != nil || len(versions) != 1 || versions[0] != "ee_branding_v1" {
		t.Fatalf("migration version changed: %v %v", versions, err)
	}
}
