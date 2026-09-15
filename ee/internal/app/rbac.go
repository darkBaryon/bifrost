// 本文件只装配身份与权限存储范围，业务调用仍由各自服务负责。
package app

import (
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbacpolicy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	"github.com/maximhq/bifrost/core/schemas"
	"gorm.io/gorm"
)

// newConsoleServices 在已迁移的数据库上装配身份与权限，事务策略与普通策略只差读取范围。
func newConsoleServices(db *gorm.DB, options identity.Options, log schemas.Logger) (*identity.Services, *rbac.Service, error) {
	store := persistence.NewStore(db, log, persistence.WithPolicyFactory(func(tx *gorm.DB, view identity.Queries) (identity.AccountPolicy, error) {
		return rbacpolicy.NewPolicy(rbac.New(rbacstore.Bind(tx, view))), nil
	}))
	permissions := rbac.New(rbacstore.NewStore(store.ReadWithin, store.Within))
	services, err := identity.New(store, hasher.Bcrypt{}, options, rbacpolicy.NewPolicy(permissions))
	return services, permissions, err
}
