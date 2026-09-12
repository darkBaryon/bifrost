// 本文件把存储故障记为固定操作名、阶段、关联 ID 和安全错误分类，不输出 SQL 或驱动原文；并识别 SQLite 锁冲突。
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// Logger 是本包需要的日志能力，由 app 注入宿主按部署配置好的 logger；消息为 printf 风格。
type Logger interface {
	Error(msg string, args ...any)
}

// logDatabaseFailure 在 err 是存储故障时写一行诊断日志，然后原样返回 err；业务错误与未找到不记录。
func logDatabaseFailure(log Logger, ctx context.Context, stage string, err error) error {
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var business identity.Error
	if errors.As(err, &business) && business != identity.ErrUnavailable {
		return err
	}
	kind, code := "driver_error", fmt.Sprintf("%T", err)
	var pg *pgconn.PgError
	var lite sqlite3.Error
	switch {
	case errors.As(err, &pg):
		kind, code = "postgres", pg.Code
	case errors.As(err, &lite):
		kind, code = "sqlite", fmt.Sprintf("%d/%d", lite.Code, lite.ExtendedCode)
	case errors.Is(err, context.DeadlineExceeded):
		kind, code = "timeout", "context_deadline"
	case errors.Is(err, context.Canceled):
		kind, code = "cancelled", "context_cancelled"
	case errors.Is(err, sql.ErrConnDone) || err.Error() == "sql: database is closed":
		kind, code = "connection", "closed"
	}
	operation, id := identity.DiagnosticOperation(ctx)
	log.Error("EE identity database failure operation=%s stage=%s incident_id=%s kind=%s code=%s", operation, stage, id, kind, code)
	return err
}

// sqliteBusy 识别宿主后台写入导致的 SQLite busy/locked，这类错误整笔重试后通常能成功。
func sqliteBusy(err error) bool {
	var e sqlite3.Error
	return errors.As(err, &e) && (e.Code == sqlite3.ErrBusy || e.Code == sqlite3.ErrLocked)
}

// SQLite 锁冲突最多整笔重试 busyRetries 次，间隔按次数递增。
const (
	busyRetries   = 3
	busyRetryStep = 20 * time.Millisecond
)

// retryBusy 执行 attempt；只对 SQLite busy/locked 重试，其他错误立即返回，耗尽后返回最后一次错误。
func retryBusy(ctx context.Context, attempt func() error) error {
	var err error
	for i := 0; i < busyRetries; i++ {
		if err = attempt(); !sqliteBusy(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * busyRetryStep):
		}
	}
	return err
}
