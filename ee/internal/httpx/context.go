// 本文件给控制台操作创建诊断上下文，并向访问日志与调用方传递同一关联 ID。
package httpx

import (
	"context"

	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/valyala/fasthttp"
)

// OperationContext 为一次入口操作分配关联 ID，并写入请求与响应头，与宿主访问日志的 request_id 对应。
func OperationContext(c *fasthttp.RequestCtx, operation string) context.Context {
	ctx := database.WithOperation(c, operation)
	_, id := database.Operation(ctx)
	c.Request.Header.Set("x-request-id", id)
	c.Response.Header.Set("X-Request-ID", id)
	return ctx
}
