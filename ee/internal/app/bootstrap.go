// Package app 在启动时创建EE业务服务，并接到同一个Bifrost HTTP应用中。
package app

import (
	"context"
	"errors"
	"fmt"

	brandinghandler "github.com/darkBaryon/bifrost/ee/internal/branding/http"
	eeconfig "github.com/darkBaryon/bifrost/ee/internal/branding/persistence"
	eehost "github.com/darkBaryon/bifrost/ee/internal/host"
	rbachost "github.com/darkBaryon/bifrost/ee/internal/rbac/host"
	"github.com/maximhq/bifrost/core/schemas"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
)

// Bootstrap 启动前先告诉Bifrost如何创建身份和角色服务，再完成Bifrost自身的准备，最后接入品牌并核对所有接口的权限规则。
// log 由程序入口传入，所有EE日志都使用这份配置。
func Bootstrap(ctx context.Context, s *bifrostServer.BifrostHTTPServer, log schemas.Logger) error {
	var auth *eehost.AuthAdapter
	var permissions *rbachost.Adapter
	// Bifrost准备好数据库后、注册管理路由前，会调用这个函数。
	// 失败时直接返回nil；如果把空指针放进接口，Bifrost就无法用接口是否为nil来识别缺失的实现。
	s.ConsoleAuthFactory = func(ctx context.Context, host *bifrostServer.BifrostHTTPServer) (bifrostServer.ConsoleAuthProvider, error) {
		adapter, permissionAdapter, err := assembleIdentity(ctx, host, log)
		if err != nil {
			return nil, err
		}
		auth = adapter
		permissions = permissionAdapter
		return adapter, nil
	}
	if err := s.Bootstrap(ctx); err != nil {
		return err
	}
	// 如果Bifrost没有调用上面的函数，说明登录检查尚未接好，必须停止启动。
	if auth == nil || permissions == nil {
		return errors.New("ee: console auth factory was not invoked by the host")
	}
	if err := attach(ctx, s, auth.APIMiddleware()); err != nil {
		return err
	}
	if err := permissions.VerifyRoutes(s.Router); err != nil {
		return err
	}
	s.Server.Handler = permissions.RootGuard(s.Server.Handler)
	return nil
}

// attach 给已经准备好的Bifrost应用添加品牌接口；此时还没开始接收请求，可以继续注册路由。
func attach(ctx context.Context, s *bifrostServer.BifrostHTTPServer, auth schemas.BifrostHTTPMiddleware) error {
	if s.Config == nil || s.Config.ConfigStore == nil {
		return errors.New("ee requires the config store (database mode); config_store.enabled=false is not supported")
	}
	db := s.Config.ConfigStore.DB()
	if db == nil {
		return errors.New("ee: config store returned a nil *gorm.DB")
	}
	// 品牌数据也放在Bifrost的配置数据库里，由品牌模块准备自己的表。
	if err := s.Config.ConfigStore.RunMigration(ctx, eeconfig.MigrateBranding); err != nil {
		return fmt.Errorf("ee: migrate branding: %w", err)
	}
	// 登录页可以读取品牌信息；修改品牌时使用同一套EE登录检查。
	if err := brandinghandler.NewBrandingHandler(eeconfig.NewBrandingStore(db)).RegisterRoutes(s.Router, auth); err != nil {
		return fmt.Errorf("ee: register branding routes: %w", err)
	}
	return nil
}
