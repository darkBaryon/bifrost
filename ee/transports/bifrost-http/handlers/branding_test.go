package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"
)

func TestBrandingDefault(t *testing.T) {
	ctx := &fasthttp.RequestCtx{}
	NewBrandingHandler().get(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d", ctx.Response.StatusCode())
	}
	if ct := string(ctx.Response.Header.ContentType()); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"enabled", "has_logo", "has_icon"} {
		v, ok := got[k]
		if !ok || v != false {
			t.Fatalf("field %s = %v (present=%v), want false", k, v, ok)
		}
	}
}
