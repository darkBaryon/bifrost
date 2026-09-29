// 本文件分别创建账号、认证、会话和权限服务，并明确连接跨模块调用。
package app

import (
	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/auth/hasher"
	authstore "github.com/darkBaryon/bifrost/ee/internal/auth/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/host"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	rbacpolicy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	sessionstore "github.com/darkBaryon/bifrost/ee/internal/session/persistence"
	"github.com/maximhq/bifrost/core/schemas"
	"gorm.io/gorm"
)

// consoleServices 仅供装配层持有依赖，不承载请求处理或转发方法。
type consoleServices struct {
	Accounts    *identity.Services
	Auth        *auth.Service
	Sessions    *session.Service
	Permissions *rbac.Service
	Tickets     *host.Tickets
}

func newConsoleServices(db *gorm.DB, options identity.Options, log schemas.Logger) (*consoleServices, error) {
	sessions, err := session.New(sessionstore.NewStore(db), options.SessionTTL)
	if err != nil {
		return nil, err
	}
	passwords := auth.NewPasswords(hasher.Bcrypt{})
	authentication, err := auth.New(db, authstore.NewStore(db, log), sessions, passwords, log)
	if err != nil {
		return nil, err
	}
	// 旧账号和RBAC事务策略留待它们各自迁移；新auth/session不经过这个工厂。
	store := persistence.NewStore(db, log, persistence.WithPolicyFactory(func(tx *gorm.DB, view identity.Queries) (identity.AccountPolicy, error) {
		return rbacpolicy.NewPolicy(rbac.New(rbacstore.Bind(tx, view))), nil
	}))
	permissions := rbac.New(rbacstore.NewStore(store.ReadWithin, store.Within))
	accounts, err := identity.New(store, passwords, options, rbacpolicy.NewPolicy(permissions), sessions)
	if err != nil {
		return nil, err
	}
	return &consoleServices{Accounts: accounts, Auth: authentication, Sessions: sessions, Permissions: permissions,
		Tickets: host.NewTickets(db, authentication, sessions, permissions, log)}, nil
}
