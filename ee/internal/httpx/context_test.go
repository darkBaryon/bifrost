// 本文件验证 HTTP 请求、响应与业务诊断上下文使用同一关联 ID。
package httpx

import (
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/valyala/fasthttp"
)

func TestOperationContextCorrelatesHeaders(t *testing.T) {
	var c fasthttp.RequestCtx
	c.Request.Header.Set("X-Request-ID", "untrusted-client-id")
	ctx := OperationContext(&c, "auth.login")
	operation, id := database.Operation(ctx)
	if operation != "auth.login" || id == "" || id == "untrusted-client-id" {
		t.Fatalf("unexpected operation metadata: operation=%q id=%q", operation, id)
	}
	if requestID := string(c.Request.Header.Peek("X-Request-ID")); requestID != id {
		t.Fatalf("request ID %q differs from diagnostic ID %q", requestID, id)
	}
	if responseID := string(c.Response.Header.Peek("X-Request-ID")); responseID != id {
		t.Fatalf("response ID %q differs from diagnostic ID %q", responseID, id)
	}
}
