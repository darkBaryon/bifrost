// 本文件提供跨模块共用的操作关联与安全数据库故障日志，不输出凭据、SQL 或驱动错误原文。
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

// Logger 是数据库诊断需要的最小能力，由装配方注入宿主日志。
type Logger interface{ Error(string, ...any) }

type operationKey struct{}

type operation struct{ name, id string }

// WithOperation 为一次入口操作分配关联编号；同一上下文在各模块及其事务内传递。
func WithOperation(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, operationKey{}, operation{name: name, id: uuid.NewString()})
}

// Operation 返回操作名称与关联编号；未设置时保留已有诊断的 identity/none 占位值。
func Operation(ctx context.Context) (name, id string) {
	if value, ok := ctx.Value(operationKey{}).(operation); ok {
		return value.name, value.id
	}
	return "identity", "none"
}

// LogFailure 记录安全驱动分类并原样返回错误；nil 日志、nil 错误和未找到记录不输出。
// component 与 stage 必须是代码中的固定名称；业务错误是否需要记录由所属模块判断。
func LogFailure(log Logger, ctx context.Context, component, stage string, err error) error {
	if log == nil || err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	kind, code := "driver_error", fmt.Sprintf("%T", err)
	var postgres *pgconn.PgError
	var sqlite sqlite3.Error
	switch {
	case errors.As(err, &postgres):
		kind, code = "postgres", postgres.Code
	case errors.As(err, &sqlite):
		kind, code = "sqlite", fmt.Sprintf("%d/%d", sqlite.Code, sqlite.ExtendedCode)
	case errors.Is(err, context.DeadlineExceeded):
		kind, code = "timeout", "context_deadline"
	case errors.Is(err, context.Canceled):
		kind, code = "cancelled", "context_cancelled"
	case errors.Is(err, sql.ErrConnDone) || err.Error() == "sql: database is closed":
		kind, code = "connection", "closed"
	}
	name, id := Operation(ctx)
	log.Error("EE %s database failure operation=%s stage=%s incident_id=%s kind=%s code=%s", component, name, stage, id, kind, code)
	return err
}
