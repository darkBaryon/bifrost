// 本文件原子建立角色表、预置数据、首次管理员关系及迁移标记。
package persistence

import (
	"context"
	"strconv"
	"time"

	authstore "github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	upstream "github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/migrator"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 迁移号与锁键已约定持久化，不能在发布后改变；锁键与identity和branding区分。
const (
	migrationID         = "ee_rbac_v1"
	migrationLock int64 = 8342761903
)

// MigrateRBAC 在身份迁移成功后执行，重启不会覆盖现有角色或重新授予锚点权限。
func MigrateRBAC(log authstore.Logger) func(context.Context, *gorm.DB) error {
	return func(ctx context.Context, db *gorm.DB) error {
		if !supportedRoleIDSize() {
			return rbac.ErrUnavailable
		}
		e := authstore.RetrySQLiteBusy(ctx, func() error { return migrate(ctx, db) })
		if e != nil {
			log.Error("EE RBAC migration failed kind=%T", e)
		}
		return e
	}
}

func migrate(ctx context.Context, db *gorm.DB) error {
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
		return upstream.RunSingleMigration(ctx, &opts, tx, nil, &migrator.Migration{ID: migrationID, Migrate: func(tx *gorm.DB) error {
			view, e := authstore.LockView(ctx, tx)
			if e != nil {
				return e
			}
			for _, model := range migrationModels() {
				if e := tx.Migrator().CreateTable(model); e != nil {
					return e
				}
			}
			bound := Bind(tx, view)
			for _, preset := range rbac.PresetRoles() {
				code := string(preset.SystemCode)
				at := time.Now().UTC()
				row := roleRow{ID: uint64(preset.ID), Name: preset.Name, Description: preset.Description, SystemCode: &code, CreatedAt: at, UpdatedAt: at}
				if e := tx.Create(&row).Error; e != nil {
					return e
				}
				if e := bound.permissions(preset.ID, preset.PermissionCodes); e != nil {
					return e
				}
			}
			if tx.Dialector.Name() == "postgres" {
				if e := tx.Exec("SELECT setval(pg_get_serial_sequence('ee_rbac_roles', 'id'), ?, true)", uint64(rbac.ReadonlyRoleID)).Error; e != nil {
					return e
				}
			}
			if view.State().Initialized {
				return rbac.New(bound).BindInitialChief(ctx, view.State().ChiefAccountID)
			}
			return nil
		}})
	})
}

// RequireComplete 仅核对完整迁移，不隐式建表，供离线恢复使用。
func RequireComplete(ctx context.Context, db *gorm.DB) error {
	if !supportedRoleIDSize() {
		return rbac.ErrUnavailable
	}
	db = db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).WithContext(ctx)
	pending, e := migrator.PendingIDs(ctx, db, migrator.DefaultOptions, []string{migrationID})
	if e != nil || len(pending) > 0 {
		return rbac.ErrUnavailable
	}
	for _, model := range migrationModels() {
		if !db.Migrator().HasTable(model) {
			return rbac.ErrUnavailable
		}
	}
	var row roleRow
	if e := db.First(&row, uint64(rbac.ChiefRoleID)).Error; e != nil || row.SystemCode == nil || *row.SystemCode != string(rbac.SystemChief) {
		return rbac.ErrUnavailable
	}
	return nil
}

func supportedRoleIDSize() bool { return strconv.IntSize == 64 }

func migrationModels() []any { return []any{&roleRow{}, &permissionRow{}, &accountRoleRow{}} }
