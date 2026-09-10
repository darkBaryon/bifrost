// 本文件只记录固定操作名、关联ID和安全错误分类，禁止SQL及driver错误原文。
package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

func databaseFailure(ctx context.Context, stage string, err error) error {
	if err == nil || errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var business identity.Error
	if errors.As(err, &business) && business != identity.ErrUnavailable {
		return err
	}
	kind, code := "driver_error", fmt.Sprintf("%T", err)
	var pg *pgconn.PgError
	var sqlite sqlite3.Error
	switch {
	case errors.As(err, &pg):
		kind, code = "postgres", pg.Code
	case errors.As(err, &sqlite):
		kind, code = "sqlite", fmt.Sprintf("%d/%d", sqlite.Code, sqlite.ExtendedCode)
	case errors.Is(err, context.DeadlineExceeded):
		kind, code = "timeout", "context_deadline"
	case errors.Is(err, context.Canceled):
		kind, code = "cancelled", "context_cancelled"
	case errors.Is(err, sql.ErrConnDone) || err.Error() == "sql: database is closed":
		kind, code = "connection", "closed"
	}
	operation, id := identity.DiagnosticOperation(ctx)
	log.Printf("EE identity database failure operation=%s stage=%s incident_id=%s kind=%s code=%s", operation, stage, id, kind, code)
	return err
}
func sqliteBusy(err error) bool {
	var e sqlite3.Error
	return errors.As(err, &e) && (e.Code == sqlite3.ErrBusy || e.Code == sqlite3.ErrLocked)
}
