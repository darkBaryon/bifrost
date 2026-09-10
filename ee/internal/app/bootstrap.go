// Package app 负责 EE 业务依赖与内嵌 Bifrost HTTP 应用的装配。
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/darkBaryon/bifrost/ee/internal/branding"
	brandinghandler "github.com/darkBaryon/bifrost/ee/internal/branding/http"
	eeconfig "github.com/darkBaryon/bifrost/ee/internal/branding/persistence"
	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
)

// Bootstrap 取代上游 main 里的 s.Bootstrap(ctx) 调用.
func Bootstrap(ctx context.Context, s *bifrostServer.BifrostHTTPServer) error {
	if err := s.Bootstrap(ctx); err != nil {
		return err
	}
	return attach(ctx, s)
}

// attach 在上游 Bootstrap 之后、Start 之前运行. 此时上游对象已装配完成但尚未监听,
// Router / Server.Handler / Config / 插件链都是可改的活数据.
func attach(ctx context.Context, s *bifrostServer.BifrostHTTPServer) error {
	// 复用上游配置数据库，由品牌存储适配负责自己的表迁移。
	if s.Config == nil || s.Config.ConfigStore == nil {
		return errors.New("ee requires the config store (database mode); config_store.enabled=false is not supported")
	}
	db := s.Config.ConfigStore.DB()
	if db == nil {
		return errors.New("ee: config store returned a nil *gorm.DB")
	}

	if s.AuthMiddleware == nil {
		return errors.New("ee: branding requires admin authentication middleware")
	}
	if err := s.Config.ConfigStore.RunMigration(ctx, eeconfig.MigrateBranding); err != nil {
		return fmt.Errorf("ee: migrate branding: %w", err)
	}

	// 品牌读取供登录页使用，写入路由使用上游管理员认证。
	if err := brandinghandler.NewBrandingHandler(branding.NewService(eeconfig.NewBrandingStore(db))).RegisterRoutes(s.Router, s.AuthMiddleware.APIMiddleware()); err != nil {
		return fmt.Errorf("ee: register branding routes: %w", err)
	}

	return nil
}
