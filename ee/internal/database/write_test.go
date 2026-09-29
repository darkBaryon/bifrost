package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 事务中途发生 busy 时，前一次业务写入和写锁计数都必须回滚，不能只重试失败的 SQL。
func TestWriteRetriesWholeTransaction(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "write.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, statement := range []string{
		"CREATE TABLE ee_identity_state (id INTEGER PRIMARY KEY, revision INTEGER)",
		"INSERT INTO ee_identity_state VALUES (1, 0)",
		"CREATE TABLE writes (attempt INTEGER)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	attempts := 0
	err = Write(context.Background(), db, func(tx *gorm.DB) error {
		attempts++
		if err := tx.Exec("INSERT INTO writes VALUES (?)", attempts).Error; err != nil {
			return err
		}
		if attempts == 1 {
			return sqlite3.Error{Code: sqlite3.ErrBusy}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var saved []int
	if err := db.Table("writes").Pluck("attempt", &saved).Error; err != nil {
		t.Fatal(err)
	}
	var revision int
	if err := db.Table("ee_identity_state").Select("revision").Scan(&revision).Error; err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || len(saved) != 1 || saved[0] != 2 || revision != 1 {
		t.Fatalf("retry did not roll back whole transaction: attempts=%d saved=%v revision=%d", attempts, saved, revision)
	}
}
