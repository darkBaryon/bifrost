// 本文件执行品牌表的版本化迁移和多节点迁移锁。
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
const brandingMigrationID = "ee_branding_v1"

// brandingAdvisoryLockKey 协调 PostgreSQL 多节点的本功能迁移。
// 它必须保持稳定，并与上游 framework/configstore/migrations.go 中的迁移锁键不同。
const brandingAdvisoryLockKey = 8342761901

// SQLite 锁冲突最多整笔重试 busyRetries 次，间隔按次数递增。
// 与 identity/persistence 的 retryBusy 是同一条驱动兼容规则；两个模块的迁移各自独立，不互相依赖，改动时须同步。
const (
	busyRetries   = 3
	busyRetryStep = 20 * time.Millisecond
)

// MigrateBranding 使用 ConfigStore.RunMigration 提供的连接执行品牌迁移。
// 表、初始单例和版本记录由同一个外层事务提交或回滚；只对 SQLite busy/locked 整笔重试，耗尽后返回最后一次错误。
func MigrateBranding(ctx context.Context, db *gorm.DB) error {
	var err error
	for attempt := 0; attempt < busyRetries; attempt++ {
		if err = migrateBrandingOnce(ctx, db); !sqliteBusy(err) {
			return err
		}
		// 宿主后台写入可能令SQLite读事务无法升级为写事务；先回滚再整笔重试。
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

func migrateBrandingOnce(ctx context.Context, db *gorm.DB) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SET LOCAL lock_timeout = '10s'").Error; err != nil {
				return err
			}
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", brandingAdvisoryLockKey).Error; err != nil {
				return err
			}
		}
		opts := *migrator.DefaultOptions
		opts.UseTransaction = false
		return upstream.RunSingleMigration(ctx, &opts, tx, nil, &migrator.Migration{
			ID: brandingMigrationID,
			Migrate: func(tx *gorm.DB) error {
				if err := tx.Migrator().CreateTable(&brandingRow{}); err != nil {
					return err
				}
				return tx.Create(&brandingRow{ID: 1}).Error
			},
			Rollback: func(tx *gorm.DB) error { return tx.Migrator().DropTable(&brandingRow{}) },
		})
	})
}
