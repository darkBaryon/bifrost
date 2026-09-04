// Package server 是 ee 对上游 transports/bifrost-http/server 的扩建:
// 不改上游任何文件, 只在上游 Bootstrap 前后各加一道装配工序.
//
//	prepare  —— 上游装配前必须设好的字段与开关 (上游 Bootstrap 内会读取它们)
//	attach   —— 上游装配完成后往对象上加我们的零件 (表 / 插件 / 路由 / Handler 包裹)
//
// 两道工序之间原样调用上游 Bootstrap, 之后由 main 调用上游 Start.
package server

import (
	"context"
	"errors"
	"fmt"

	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"

	"github.com/darkBaryon/bifrost/ee/transports/bifrost-http/handlers"
	"github.com/darkBaryon/bifrost/ee/transports/bifrost-http/lib"
)

// Bootstrap 取代上游 main 里的 s.Bootstrap(ctx) 调用.
func Bootstrap(ctx context.Context, s *bifrostServer.BifrostHTTPServer) error {
	prepare(s)
	if err := s.Bootstrap(ctx); err != nil {
		return err
	}
	return attach(ctx, s)
}

// prepare 在上游 Bootstrap 之前运行: 只能放上游装配过程中会读取的东西.
// ShellRewriter 在 RegisterAPIRoutes → NewUIHandler 时被捕获, 之后再赋值无效.
// B2 起在此处理 IsEnterprise 相关开关.
func prepare(s *bifrostServer.BifrostHTTPServer) {
	s.ShellRewriter = lib.EEShellRewriter
}

// attach 在上游 Bootstrap 之后、Start 之前运行. 此时上游对象已装配完成但尚未监听,
// Router / Server.Handler / Config / 插件链都是可改的活数据.
func attach(ctx context.Context, s *bifrostServer.BifrostHTTPServer) error {
	// ① 自建表: 复用上游的数据库连接, ee 表一律 ee_ 前缀, 各自迁移互不打听.
	if s.Config == nil || s.Config.ConfigStore == nil {
		return errors.New("ee requires the config store (database mode); config_store.enabled=false is not supported")
	}
	db := s.Config.ConfigStore.DB()
	if db == nil {
		return errors.New("ee: config store returned a nil *gorm.DB")
	}
	if err := db.AutoMigrate(&lib.Probe{}); err != nil {
		return fmt.Errorf("ee: migrate %s: %w", lib.Probe{}.TableName(), err)
	}

	// ② 进程内插件: 不经 .so, 上游把它同步进 Config 与 core 的插件链并排序 (纯内存, 不落库).
	if err := s.SyncLoadedPlugin(ctx, ProbePluginName, probePlugin{}, nil, nil); err != nil {
		return fmt.Errorf("ee: register plugin %s: %w", ProbePluginName, err)
	}

	// ③ 路由: 往上游的路由表加 ee 的路径. 无鉴权的只有骨架探针与上游设计即公开的 branding.
	handlers.NewProbeHandler(db, ProbePluginName).RegisterRoutes(s.Router)
	handlers.NewBrandingHandler().RegisterRoutes(s.Router)

	// ④ 全局中间件: 包在上游拼好的整条链外面 (ServerCallbacks 被上游硬编码, 不可替换).
	s.Server.Handler = handlers.EEHeaderMiddleware(s.Server.Handler)
	return nil
}
