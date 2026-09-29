// 本文件把存储故障记为固定操作名、阶段、关联 ID 和安全错误分类，不输出 SQL 或驱动原文；并识别 SQLite 锁冲突。
package persistence

import (
	"context"
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"gorm.io/gorm"
)

// logDatabaseFailure 在 err 是存储故障时写一行诊断日志，然后原样返回 err；业务错误与未找到不记录。
func logDatabaseFailure(log database.Logger, ctx context.Context, stage string, err error) error {
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var business identity.Error
	if errors.As(err, &business) && business != identity.ErrUnavailable {
		return err
	}
	return database.LogFailure(log, ctx, "identity", stage, err)
}

// sqliteBusy 与新模块共用同一套驱动判定和重试规则。
func sqliteBusy(err error) bool { return database.IsSQLiteBusy(err) }
func retryBusy(ctx context.Context, attempt func() error) error {
	return database.RetrySQLiteBusy(ctx, attempt)
}
