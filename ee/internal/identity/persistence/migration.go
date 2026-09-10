// 本文件原子建立身份表、索引与版本记录，复用宿主迁移机制。
package persistence

import (
	"context"
	"time"

	upstream "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const identityMigrationID = "ee_identity_v1"
const identityMigrationLock int64 = 8342761902

func MigrateIdentity(ctx context.Context, db *gorm.DB) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = migrateIdentityOnce(ctx, db)
		if !sqliteBusy(err) {
			return databaseFailure(ctx, "migration", err)
		}
		select {
		case <-ctx.Done():
			return databaseFailure(ctx, "migration", ctx.Err())
		case <-time.After(time.Duration(attempt+1) * 20 * time.Millisecond):
		}
	}
	return databaseFailure(ctx, "migration", err)
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
		return upstream.RunSingleMigration(ctx, &opts, tx, nil, &migrator.Migration{ID: identityMigrationID, Migrate: func(tx *gorm.DB) error {
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
		}})
	})
}
