// 本文件原子创建模板表与迁移记录，复用身份模块的SQLite重试。
package persistence

import (
	"context"

	authstore "github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	upstream "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const migrationID = "ee_usage_templates_v1"
const migrationLock int64 = 8342761905

// MigrateTemplates 返回宿主迁移回调；失败只记录错误类型，不输出SQL。
func MigrateTemplates(log authstore.Logger) func(context.Context, *gorm.DB) error {
	return func(ctx context.Context, db *gorm.DB) error {
		err := authstore.RetrySQLiteBusy(ctx, func() error {
			return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if tx.Dialector.Name() == "postgres" {
					if e := tx.Exec("SET LOCAL lock_timeout = '10s'").Error; e != nil {
						return e
					}
					if e := tx.Exec("SELECT pg_advisory_xact_lock(?)", migrationLock).Error; e != nil {
						return e
					}
				}
				opts := *migrator.DefaultOptions
				opts.UseTransaction = false
				return upstream.RunSingleMigration(ctx, &opts, tx, nil, &migrator.Migration{
					ID:      migrationID,
					Migrate: func(tx *gorm.DB) error { return tx.Migrator().CreateTable(&templateRow{}) },
				})
			})
		})
		if err != nil && log != nil {
			log.Error("EE usage migration failed kind=%T", err)
		}
		return err
	}
}
