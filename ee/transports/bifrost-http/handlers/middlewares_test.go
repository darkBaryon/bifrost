package handlers

import (
	"testing"

	"github.com/valyala/fasthttp"
)

func TestEEHeaderMiddleware(t *testing.T) {
	called := false
	h := EEHeaderMiddleware(func(ctx *fasthttp.RequestCtx) { called = true; ctx.SetStatusCode(404) })
	ctx := &fasthttp.RequestCtx{}
	h(ctx)
	if !called {
		t.Fatal("next handler not called")
	}
	if got := string(ctx.Response.Header.Peek(EEHeaderName)); got != EEHeaderValue {
		t.Fatalf("header %s = %q, want %q", EEHeaderName, got, EEHeaderValue)
	}
	if ctx.Response.StatusCode() != 404 {
		t.Fatalf("status must pass through, got %d", ctx.Response.StatusCode())
	}
}
