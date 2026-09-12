// 本文件装配账号认证：解析部署环境变量、迁移身份表、构造服务与宿主适配，供正常启动与离线恢复共用。
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
	"github.com/darkBaryon/bifrost/ee/internal/identity/hasher"
	identityhttp "github.com/darkBaryon/bifrost/ee/internal/identity/http"
	"github.com/darkBaryon/bifrost/ee/internal/identity/persistence"
	"github.com/maximhq/bifrost/core/schemas"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
	"gorm.io/gorm"
)

// 账号认证的部署环境变量；初始化密钥复用宿主已解析的 BIFROST_SETUP_TOKEN。
const (
	envInitialPassword = "EE_INITIAL_PASSWORD"  // 建号与重置使用的初始密码，默认 identity.DefaultInitialPassword
	envSessionTTLHours = "EE_SESSION_TTL_HOURS" // 会话绝对有效期（小时），范围见 identity.MinSessionTTL/MaxSessionTTL
	envPublicOrigin    = "EE_PUBLIC_ORIGIN"     // 完整外部 origin；未设置时只允许回环监听地址
)

// identityOptions 读取部署环境变量并校验范围；非法值返回错误，由调用方拒绝启动。
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

// newIdentity 在已迁移的共享数据库上构造三条线的身份服务，使用宿主 bcrypt 与暂行的主管理员策略；存储诊断经 log 输出。
func newIdentity(db *gorm.DB, options identity.Options, log schemas.Logger) (*identity.Services, error) {
	return identity.New(persistence.NewStore(db, log), hasher.Bcrypt{}, options, nil)
}

// assembleIdentity 在宿主注册路由前执行：先校验全部部署配置，再迁移身份表、导入旧管理员并构造宿主适配。
func assembleIdentity(ctx context.Context, host *bifrostServer.BifrostHTTPServer, log schemas.Logger) (*eehost.AuthAdapter, error) {
	ctx = identity.WithDiagnosticOperation(ctx, "identity.bootstrap")
	if host.Config == nil || host.Config.ConfigStore == nil || host.Config.ConfigStore.DB() == nil {
		return nil, errors.New("EE identity requires a database config store")
	}
	options, err := identityOptions(host.Config.SetupToken)
	if err != nil {
		return nil, err
	}
	origin := os.Getenv(envPublicOrigin)
	if origin == "" {
		origin = "http://" + net.JoinHostPort(host.Host, host.Port)
	}
	if _, _, err = identityhttp.ValidateOrigin(origin); err != nil {
		return nil, fmt.Errorf("%s must be an HTTPS origin and is required for a non-loopback listener", envPublicOrigin)
	}
	if err = host.Config.ConfigStore.RunMigration(ctx, persistence.MigrateIdentity(log)); err != nil {
		return nil, errors.New("EE identity migration failed")
	}
	svc, err := newIdentity(host.Config.ConfigStore.DB(), options, log)
	if err != nil {
		return nil, err
	}
	if err = eehost.ImportLegacyAdministrator(ctx, host, svc.Account, log); err != nil {
		return nil, err
	}
	handler, err := identityhttp.NewHandler(svc, origin)
	if err != nil {
		return nil, err
	}
	return eehost.NewAuthAdapter(host, svc.Session, handler), nil
}
