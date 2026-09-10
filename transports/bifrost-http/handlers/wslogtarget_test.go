package handlers

import (
	"github.com/valyala/fasthttp"
	"testing"
)

func TestRequestLogTargetHidesWebSocketCredentials(t *testing.T) {
	for _, method := range []string{"GET", "OPTIONS", "POST"} {
		var request fasthttp.Request
		request.Header.SetMethod(method)
		request.SetRequestURI("/ws?ticket=private-ticket&token=old-secret")
		var ctx fasthttp.RequestCtx
		ctx.Init(&request, nil, nil)
		if got := requestLogTarget(&ctx); got != "/ws" {
			t.Fatal("access log contains websocket query")
		}
		if string(ctx.QueryArgs().Peek("ticket")) != "private-ticket" {
			t.Fatal("logging consumed the credential needed by auth")
		}
	}
}
