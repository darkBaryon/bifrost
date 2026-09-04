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

	bifrostServer "github.com/maximhq/bifrost/transports/bifrost-http/server"
)

// Bootstrap 取代上游 main 里的 s.Bootstrap(ctx) 调用.
func Bootstrap(ctx context.Context, s *bifrostServer.BifrostHTTPServer) error {
	prepare(s)
	if err := s.Bootstrap(ctx); err != nil {
		return err
	}
	return attach(ctx, s)
}

// prepare 在上游 Bootstrap 之前运行. 骨架期为空, 后续在此设置 ShellRewriter 等
// "nil on OSS" 字段; B2 起在此处理 IsEnterprise 相关开关.
func prepare(s *bifrostServer.BifrostHTTPServer) {}

// attach 在上游 Bootstrap 之后、Start 之前运行. 骨架期为空, 第 2 步补五个插座.
func attach(ctx context.Context, s *bifrostServer.BifrostHTTPServer) error {
	return nil
}
