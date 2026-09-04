package handlers

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/valyala/fasthttp"
)

func TestProbePing(t *testing.T) {
	h := &ProbeHandler{countRows: func() (int64, error) { return 3, nil }, plugin: "ee-probe"}
	ctx := &fasthttp.RequestCtx{}
	h.ping(ctx)
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d", ctx.Response.StatusCode())
	}
	var got pingResponse
	if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil {
		t.Fatalf("bad json: %v (%s)", err, ctx.Response.Body())
	}
	if !got.OK || got.ProbeRows != 3 || got.Plugin != "ee-probe" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestProbePingCountError(t *testing.T) {
	h := &ProbeHandler{countRows: func() (int64, error) { return 0, errors.New("boom") }}
	ctx := &fasthttp.RequestCtx{}
	h.ping(ctx)
	if ctx.Response.StatusCode() != 500 {
		t.Fatalf("want 500, got %d", ctx.Response.StatusCode())
	}
}
