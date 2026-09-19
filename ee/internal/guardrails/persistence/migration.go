// Package persistence 在 Bifrost 配置数据库中保存内容安全配置（单例行，JSON 正文）。
// 本文件执行版本化迁移和多节点迁移锁，做法与品牌模块一致。
package persistence

import (
	"context"
	"errors"
	"time"

	"github.com/mattn/go-sqlite3"
	upstream "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
)

// 迁移 ID 已用于版本表，移动文件不能改变它。
const guardrailsMigrationID = "ee_guardrails_v1"

// guardrailsAdvisoryLockKey 协调 PostgreSQL 多节点的本功能迁移；必须稳定，且不与上游及其他 EE 模块的锁键相同。
const guardrailsAdvisoryLockKey = 8342761903

// SQLite 锁冲突最多整笔重试 busyRetries 次，间隔按次数递增；与品牌、身份模块是同一条驱动兼容规则，改动时须同步。
const (
	busyRetries   = 3
	busyRetryStep = 20 * time.Millisecond
)

// Migrate 使用 ConfigStore.RunMigration 提供的连接建表；只建表不插行，无行即"未配置"。
func Migrate(ctx context.Context, db *gorm.DB) error {
	var err error
	for attempt := 0; attempt < busyRetries; attempt++ {
		if err = migrateOnce(ctx, db); !sqliteBusy(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * busyRetryStep):
		}
	}
	return err
}

func sqliteBusy(err error) bool {
	var busy sqlite3.Error
	return errors.As(err, &busy) && (busy.Code == sqlite3.ErrBusy || busy.Code == sqlite3.ErrLocked)
}

func migrateOnce(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SET LOCAL lock_timeout = '10s'").Error; err != nil {
				return err
			}
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", guardrailsAdvisoryLockKey).Error; err != nil {
				return err
			}
		}
		opts := *migrator.DefaultOptions
		opts.UseTransaction = false
		return upstream.RunSingleMigration(ctx, &opts, tx, nil, &migrator.Migration{
			ID:       guardrailsMigrationID,
			Migrate:  func(tx *gorm.DB) error { return tx.Migrator().CreateTable(&Row{}) },
			Rollback: func(tx *gorm.DB) error { return tx.Migrator().DropTable(&Row{}) },
		})
	})
}
