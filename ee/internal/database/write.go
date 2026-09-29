// Package database 提供共享配置库的事务与 SQLite 锁冲突重试。
package database

import (
	"context"
	"errors"
	"time"

	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// Write 先取得控制台写锁，再执行整笔操作；跨模块调用显式传递同一个 tx。
// 锁沿用已有身份状态表，不改迁移或数据结构。回调必须允许因 SQLite 锁冲突而整笔重试。
func Write(ctx context.Context, db *gorm.DB, fn func(*gorm.DB) error) error {
	if db == nil || fn == nil {
		return errors.New("database: missing transaction dependency")
	}
	return RetrySQLiteBusy(ctx, func() error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := Lock(tx); err != nil {
				return err
			}
			return fn(tx)
		})
	})
}

// Lock 在调用方已有事务中取得账号、会话与权限写操作共用的锁。
func Lock(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("database: missing transaction")
	}
	result := tx.Table("ee_identity_state").Where("id = 1").UpdateColumn("revision", gorm.Expr("revision + 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("database: missing console state")
	}
	return nil
}

// IsSQLiteBusy 识别可通过整笔重试处理的 SQLite 写锁冲突。
func IsSQLiteBusy(err error) bool {
	var e sqlite3.Error
	return errors.As(err, &e) && (e.Code == sqlite3.ErrBusy || e.Code == sqlite3.ErrLocked)
}

// RetrySQLiteBusy 沿用三次尝试、20ms 递增间隔；其他错误立即返回。
func RetrySQLiteBusy(ctx context.Context, attempt func() error) error {
	var err error
	for i := 0; i < 3; i++ {
		if err = attempt(); !IsSQLiteBusy(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * 20 * time.Millisecond):
		}
	}
	return err
}
