// Package handlers 放 ee 版 transports 的 HTTP 处理器, 形状照上游: 一个资源一个文件,
// 每个 handler 暴露 RegisterRoutes(router). JSON 响应复用上游 handlers.SendJSON.
package handlers

import "github.com/valyala/fasthttp"

// EEHeaderName / EEHeaderValue: 所有经过 ee 版服务的响应都带这个头, 证明 "替换 s.Server.Handler 包一层" 可用.
const (
	EEHeaderName  = "X-Bifrost-EE"
	EEHeaderValue = "1"
)

// EEHeaderMiddleware 包在上游拼好的整条处理链外面 (安全头/CORS/解压/Router 之外).
// 它在 next 之前设头, 所以对 404 与错误响应同样生效. 审计中间件将来挂在同一位置.
func EEHeaderMiddleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set(EEHeaderName, EEHeaderValue)
		next(ctx)
	}
}
