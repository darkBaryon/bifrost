// Package persistence 在 Bifrost 配置数据库中保存内容安全配置（单例行，JSON 正文）。
// 本文件执行版本化迁移和多节点迁移锁，做法与品牌、角色权限模块一致。
package persistence

import (
	"context"

	authstore "github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	upstream "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
)

// 迁移 ID 已用于版本表，移动文件不能改变它。
const guardrailsMigrationID = "ee_guardrails_v1"

// guardrailsAdvisoryLockKey 协调 PostgreSQL 多节点的本功能迁移；须保持稳定，且不与上游
// framework/configstore/migrations.go 及 EE 已占用的锁键相同：品牌 8342761901、身份 8342761902、角色权限 8342761903。
const guardrailsAdvisoryLockKey = 8342761904

// Migrate 使用 ConfigStore.RunMigration 提供的连接建表；只建表不插行，无行即"未配置"。
// SQLite busy/locked 整笔重试复用身份模块导出的规则，与角色权限迁移一致。
func Migrate(ctx context.Context, db *gorm.DB) error {
	return authstore.RetrySQLiteBusy(ctx, func() error { return migrateOnce(ctx, db) })
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
