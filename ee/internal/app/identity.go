// 本文件读取登录相关配置，并在启动时创建身份和角色数据表、服务及HTTP接口。
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	eehost "github.com/darkBaryon/bifrost/ee/internal/host"
	"github.com/darkBaryon/bifrost/ee/internal/identity"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	rbachost "github.com/darkBaryon/bifrost/ee/internal/rbac/host"
	rbachttp "github.com/darkBaryon/bifrost/ee/internal/rbac/http"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	"github.com/maximhq/bifrost/core/schemas"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
)

// 登录相关配置从这些环境变量读取；首次建管理员的密钥使用BIFROST_SETUP_TOKEN。
const (
	envInitialPassword = "EE_INITIAL_PASSWORD"  // 创建账号或重置密码时使用；未设置则使用identity.DefaultInitialPassword。
	envSessionTTLHours = "EE_SESSION_TTL_HOURS" // 登录最多保持多少小时；允许范围由identity.MinSessionTTL和MaxSessionTTL限定。
	envPublicOrigin    = "EE_PUBLIC_ORIGIN"     // 浏览器访问控制台的协议、域名和端口；未设置时只允许本机监听地址。
)

// identityOptions 读取初始密码和登录时长，并检查配置是否合法；有问题就返回错误。
func identityOptions(setupToken string) (identity.Options, error) {
	options := identity.Options{InitialPassword: identity.DefaultInitialPassword, SetupToken: setupToken, SessionTTL: identity.DefaultSessionTTL}
	if v, ok := os.LookupEnv(envInitialPassword); ok {
		options.InitialPassword = v
	}
	if raw, ok := os.LookupEnv(envSessionTTLHours); ok {
		hours, err := strconv.Atoi(raw)
		if err != nil {
			return identity.Options{}, fmt.Errorf("%s must be an integer number of hours", envSessionTTLHours)
		}
		options.SessionTTL = time.Duration(hours) * time.Hour
	}
	if err := options.Validate(); err != nil {
		return identity.Options{}, errors.New("invalid EE password/session settings")
	}
	return options, nil
}

// assembleIdentity 准备身份和角色接口，并把它们接到Bifrost的登录检查中。
// 先检查配置、准备数据表并导入旧管理员，全部成功后才返回；任何一步失败都会阻止启动。
func assembleIdentity(ctx context.Context, host *bifrostServer.BifrostHTTPServer, log schemas.Logger) (*eehost.AuthAdapter, *rbachost.Adapter, error) {
	ctx = identity.WithDiagnosticOperation(ctx, "identity.bootstrap")
	if host.Config == nil || host.Config.ConfigStore == nil || host.Config.ConfigStore.DB() == nil {
		return nil, nil, errors.New("EE identity requires a database config store")
	}
	options, err := identityOptions(host.Config.SetupToken)
	if err != nil {
		return nil, nil, err
	}
	origin := os.Getenv(envPublicOrigin)
	if origin == "" {
		origin = "http://" + net.JoinHostPort(host.Host, host.Port)
	}
	if _, _, err = identityhttp.ValidateOrigin(origin); err != nil {
		return nil, nil, fmt.Errorf("%s must be an HTTPS origin and is required for a non-loopback listener", envPublicOrigin)
	}
	if err = host.Config.ConfigStore.RunMigration(ctx, persistence.MigrateIdentity(log)); err != nil {
		return nil, nil, errors.New("EE identity migration failed")
	}
	if err = host.Config.ConfigStore.RunMigration(ctx, rbacstore.MigrateRBAC(log)); err != nil {
		return nil, nil, errors.New("EE RBAC migration failed")
	}
	svc, permissions, err := newConsoleServices(host.Config.ConfigStore.DB(), options, log)
	if err != nil {
		return nil, nil, err
	}
	if err = eehost.ImportLegacyAdministrator(ctx, host, svc.Account, log); err != nil {
		return nil, nil, err
	}
	handler, err := identityhttp.NewHandler(svc, origin)
	if err != nil {
		return nil, nil, err
	}
	routes := rbachttp.NewHandler(permissions, svc.Session, handler)
	permissionsAdapter := rbachost.NewAdapter(permissions, host.Config, log)
	adapter := eehost.NewAuthAdapter(host, svc.Session, handler, eehost.WithAdditionalRoutes(routes), eehost.WithRecoveryAnchor(svc.Account.RecoveryAnchor), eehost.WithConsoleAccess(permissionsAdapter), eehost.WithConsoleLogger(log))
	return adapter, permissionsAdapter, nil
}
