// Package guardrailshttp 提供内容安全配置的读写接口；写入后立即构建并热替换检查器。
package guardrailshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/persistence"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/valyala/fasthttp"
)

// 请求体上限：配置只有几 KB 文本，超过即拒绝，不进入解析。
const maxBodyBytes = 256 << 10

// Builder 把已校验配置变成检查器；由 host.Builder 满足。
type Builder interface {
	Build(ctx context.Context, cfg config.Config) (*guardrails.Checker, error)
}

// Swapper 让新检查器对后续请求生效；由 plugin.Plugin 满足。
type Swapper interface{ Swap(*guardrails.Checker) }

// Locked 报告是否处于开发假检测器模式；该模式下拒绝写入生产配置。
type Locked func() bool

// Handler 处理配置的读取、更新与重置。
type Handler struct {
	store   *persistence.Store
	builder Builder
	swapper Swapper
	locked  Locked
}

// NewHandler 显式注入依赖；locked 可为 nil（视为未锁定）。
func NewHandler(store *persistence.Store, builder Builder, swapper Swapper, locked Locked) (*Handler, error) {
	if store == nil || builder == nil || swapper == nil {
		return nil, errors.New("guardrails http: store, builder and swapper are required")
	}
	if locked == nil {
		locked = func() bool { return false }
	}
	return &Handler{store: store, builder: builder, swapper: swapper, locked: locked}, nil
}

// RegisterRoutes 注册三条 POST 路由；读取与写入都经管理端鉴权，权限在 rbac/host/routes.txt 登记。
func (h *Handler) RegisterRoutes(r *router.Router, auth schemas.BifrostHTTPMiddleware) error {
	if auth == nil {
		return errors.New("guardrails requires admin authentication middleware")
	}
	r.POST("/api/guardrails/get", auth(h.get))
	r.POST("/api/guardrails/update", auth(h.update))
	r.POST("/api/guardrails/reset", auth(h.reset))
	return nil
}

func (h *Handler) get(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	row, err := h.store.Read(ctx)
	if errors.Is(err, persistence.ErrNotConfigured) {
		upstream.SendJSON(ctx, stateResponse{Config: nil, Version: persistence.UnsetVersion})
		return
	}
	if err != nil {
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "guardrails storage unavailable")
		return
	}
	upstream.SendJSON(ctx, stateResponse{Config: json.RawMessage(row.Config), Version: row.Version})
}

// update 顺序：解析校验 → 构建检查器（含 provider 校验）→ 乐观锁写库 → 热替换。构建或写库失败都不改变运行中的检查器。
func (h *Handler) update(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if h.locked() {
		upstream.SendError(ctx, fasthttp.StatusConflict, "development fake guardrails are active; unset EE_GUARDRAILS_FAKE before writing configuration")
		return
	}
	req, err := parseUpdateRequest(ctx.PostBody())
	if err != nil {
		upstream.SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	cfg, err := config.Parse(req.Config)
	if err != nil {
		upstream.SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	checker, err := h.builder.Build(ctx, cfg)
	if err != nil {
		upstream.SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	row, err := h.store.Update(ctx, string(req.Config), req.Version)
	if errors.Is(err, persistence.ErrVersionConflict) {
		upstream.SendError(ctx, fasthttp.StatusConflict, "guardrails configuration version conflict; reload and retry")
		return
	}
	if err != nil {
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "guardrails storage unavailable")
		return
	}
	h.swapper.Swap(checker)
	upstream.SendJSON(ctx, stateResponse{Config: json.RawMessage(row.Config), Version: row.Version})
}

func (h *Handler) reset(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if err := h.store.Reset(ctx); err != nil {
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "guardrails storage unavailable")
		return
	}
	h.swapper.Swap(nil)
	upstream.SendJSON(ctx, stateResponse{Config: nil, Version: persistence.UnsetVersion})
}

type updateRequest struct {
	Config  json.RawMessage `json:"config"`
	Version int             `json:"version"`
}

// stateResponse 是 get/update/reset 的统一响应；未配置时 config 为 null、version 为 0。
type stateResponse struct {
	Config  json.RawMessage `json:"config"`
	Version int             `json:"version"`
}

func parseUpdateRequest(body []byte) (updateRequest, error) {
	if len(body) > maxBodyBytes {
		return updateRequest{}, errors.New("guardrails payload too large")
	}
	var req updateRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return updateRequest{}, errors.New("invalid guardrails JSON: expected {config, version}")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return updateRequest{}, errors.New("expected one JSON object")
	}
	if len(bytes.TrimSpace(req.Config)) == 0 || req.Version < persistence.UnsetVersion {
		return updateRequest{}, errors.New("config is required and version must be non-negative")
	}
	return req, nil
}
