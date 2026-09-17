// 本文件创建身份服务和角色服务，让它们共用数据库，并在账号操作中接入角色权限检查。
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

// newConsoleServices 在已经建好表的数据库上创建两个服务。
// 账号操作已经开始事务时，角色检查和角色绑定也使用这次事务，失败时一起回滚。
func newConsoleServices(db *gorm.DB, options identity.Options, log schemas.Logger) (*identity.Services, *rbac.Service, error) {
	// 身份模块开始事务后，通过这个回调取得使用同一事务的角色服务。
	store := persistence.NewStore(db, log, persistence.WithPolicyFactory(func(tx *gorm.DB, view identity.Queries) (identity.AccountPolicy, error) {
		return rbacpolicy.NewPolicy(rbac.New(rbacstore.Bind(tx, view))), nil
	}))
	// 直接调用角色接口时，由存储实现按需打开读取或写入事务。
	permissions := rbac.New(rbacstore.NewStore(store.ReadWithin, store.Within))
	services, err := identity.New(store, hasher.Bcrypt{}, options, rbacpolicy.NewPolicy(permissions))
	return services, permissions, err
}
