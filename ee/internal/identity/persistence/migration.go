// 本文件原子建立身份表、索引与版本记录，复用宿主迁移机制。
package persistence

import (
	"context"

	upstream "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// identityMigrationID 已写入版本表，不能改变。
const identityMigrationID = "ee_identity_v1"

// lastLoginMigrationID 为旧账号增加可空的最近登录时间，不推断历史值。
const lastLoginMigrationID = "ee_identity_v2_last_login"

// identityMigrationLock 协调 PostgreSQL 多节点的身份迁移；须保持稳定，并与品牌迁移锁键 8342761901 及
// 上游 framework/configstore/migrations.go 的锁键不同。
const identityMigrationLock int64 = 8342761902

// MigrateIdentity 返回可交给 ConfigStore.RunMigration 的迁移函数：建立六张身份表、索引和 state 单例，
// 表、单例与版本记录在同一事务提交或回滚。SQLite 锁冲突整笔重试，耗尽后返回原始错误，由调用方决定是否拒绝启动。
func MigrateIdentity(log Logger) func(context.Context, *gorm.DB) error {
	return func(ctx context.Context, db *gorm.DB) error {
		return logDatabaseFailure(log, ctx, "migration", retryBusy(ctx, func() error { return migrateIdentityOnce(ctx, db) }))
	}
}

func migrateIdentityOnce(ctx context.Context, db *gorm.DB) error {
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if e := tx.Exec("SET LOCAL lock_timeout = '10s'").Error; e != nil {
				return e
			}
			if e := tx.Exec("SELECT pg_advisory_xact_lock(?)", identityMigrationLock).Error; e != nil {
				return e
			}
		}
		opts := *migrator.DefaultOptions
		opts.UseTransaction = false
		if err := upstream.RunSingleMigration(ctx, &opts, tx, nil, &migrator.Migration{ID: identityMigrationID, Migrate: func(tx *gorm.DB) error {
			for _, model := range []any{&accountRow{}, &stateRow{}, &sessionRow{}, &eventRow{}, &ticketRow{}, &limitRow{}} {
				if e := tx.Migrator().CreateTable(model); e != nil {
					return e
				}
			}
			for _, sql := range []string{
				"CREATE UNIQUE INDEX ee_identity_username ON ee_identity_accounts(username)",
				"CREATE INDEX ee_identity_accounts_page ON ee_identity_accounts(created_at, id)",
				"CREATE UNIQUE INDEX ee_identity_session_hash ON ee_identity_sessions(token_hash)",
				"CREATE INDEX ee_identity_session_account ON ee_identity_sessions(account_id)",
				"CREATE UNIQUE INDEX ee_identity_event_operation ON ee_identity_password_events(operation_id)",
				"CREATE INDEX ee_identity_event_target ON ee_identity_password_events(target_id, occurred_at, id)",
				"CREATE INDEX ee_identity_event_page ON ee_identity_password_events(occurred_at, id)",
			} {
				if e := tx.Exec(sql).Error; e != nil {
					return e
				}
			}
			return tx.Create(&stateRow{ID: 1}).Error
		}}); err != nil {
			return err
		}
		return upstream.RunSingleMigration(ctx, &opts, tx, nil, &migrator.Migration{ID: lastLoginMigrationID, Migrate: func(tx *gorm.DB) error {
			if tx.Migrator().HasColumn(&accountRow{}, "LastLoginAt") {
				return nil
			}
			return tx.Migrator().AddColumn(&accountRow{}, "LastLoginAt")
		}})
	})
}
