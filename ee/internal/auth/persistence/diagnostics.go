// 本文件记录安全的数据库故障分类，不输出 SQL、驱动原文、登录名或 token。
package persistence

import (
	"context"
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/database"
)

func logFailure(log database.Logger, ctx context.Context, stage string, err error) error {
	var business auth.Error
	if errors.As(err, &business) && business != auth.ErrUnavailable {
		return err
	}
	return database.LogFailure(log, ctx, "auth", stage, err)
}
