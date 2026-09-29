package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
)

type failedReadStore struct {
	Store
	err error
}

func (s failedReadStore) WithTx(*gorm.DB) Store                            { return s }
func (s failedReadStore) AuthByID(context.Context, string) (Record, error) { return Record{}, s.err }

// 事务参与者必须保留重试原因；独立请求入口仍须隐藏驱动错误。
func TestTransactionalValidationPreservesRetryableError(t *testing.T) {
	service := &Service{store: failedReadStore{err: sqlite3.Error{Code: sqlite3.ErrBusy}}}
	ctx := context.Background()
	tx := &gorm.DB{}
	_, byID := service.ValidateSessionInTransaction(ctx, tx, "session")
	_, claimed := service.RevalidateSession(ctx, tx, session.Principal{SessionID: "session"})
	if !database.IsSQLiteBusy(byID) || !database.IsSQLiteBusy(claimed) {
		t.Fatalf("retry reason lost: byID=%T claimed=%T", byID, claimed)
	}
	_, public := service.ValidateSession(ctx, "session")
	if !errors.Is(public, ErrUnavailable) || database.IsSQLiteBusy(public) {
		t.Fatalf("public request leaked storage error: %v", public)
	}
}
