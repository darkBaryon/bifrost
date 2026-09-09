// 本文件执行品牌表的版本化迁移和多节点迁移锁。
package branding

import (
	"context"

	upstream "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
)

// 迁移 ID 已用于版本表，移动文件不能改变它。
const brandingMigrationID = "ee_branding_v1"

// brandingAdvisoryLockKey 协调 PostgreSQL 多节点的本功能迁移。
// 它必须保持稳定，并与上游 framework/configstore/migrations.go 中的迁移锁键不同。
const brandingAdvisoryLockKey = 8342761901

// MigrateBranding 使用 ConfigStore.RunMigration 提供的连接执行品牌迁移。
// 表、初始单例和版本记录由同一个外层事务提交或回滚。
func MigrateBranding(ctx context.Context, db *gorm.DB) error {
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
				if err := tx.Migrator().CreateTable(&Branding{}); err != nil {
					return err
				}
				return tx.Create(&Branding{ID: 1}).Error
			},
			Rollback: func(tx *gorm.DB) error { return tx.Migrator().DropTable(&Branding{}) },
		})
	})
}
