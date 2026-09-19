// Package guardrailshttp 提供内容安全配置的读写接口；写入后立即构建并热替换运行中的配置。
package guardrailshttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/darkBaryon/bifrost/ee/internal/guardrails"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/config"
	"github.com/darkBaryon/bifrost/ee/internal/guardrails/persistence"
	safetyplugin "github.com/darkBaryon/bifrost/ee/internal/guardrails/plugin"
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

// Swapper 让新配置对后续请求生效；由 plugin.Plugin 满足。
type Swapper interface {
	Swap(checker *guardrails.Checker, options safetyplugin.Options) error
}

// Logger 记录存储与构建故障的服务端原因；对外响应保持脱敏。
type Logger interface{ Error(string, ...any) }

// Locked 报告是否处于开发假检测器模式；该模式下拒绝写入生产配置。
type Locked func() bool

// Handler 处理配置的读取、更新与重置。
// 从写库到热替换由 mu 串行：并发的 update/reset 不会让库中状态与运行中配置错位。
type Handler struct {
	store   *persistence.Store
	builder Builder
	swapper Swapper
	log     Logger
	locked  Locked
	mu      sync.Mutex
}

// NewHandler 显式注入依赖；locked 可为 nil（视为未锁定）。
func NewHandler(store *persistence.Store, builder Builder, swapper Swapper, log Logger, locked Locked) (*Handler, error) {
	if store == nil || builder == nil || swapper == nil || log == nil {
		return nil, errors.New("guardrails http: store, builder, swapper and logger are required")
	}
	if locked == nil {
		locked = func() bool { return false }
	}
	return &Handler{store: store, builder: builder, swapper: swapper, log: log, locked: locked}, nil
}

// RegisterRoutes 注册三条 POST 路由；读取与写入都经管理端鉴权，权限在 rbac/host/routes.txt 登记。
func (h *Handler) RegisterRoutes(r *router.Router, auth schemas.BifrostHTTPMiddleware) error {
	if auth == nil {
		return errors.New("guardrails requires admin authentication middleware")
	}
	r.POST("/api/guardrails/get", auth(noStore(h.get)))
	r.POST("/api/guardrails/update", auth(noStore(h.update)))
	r.POST("/api/guardrails/reset", auth(noStore(h.reset)))
	return nil
}

// noStore 让配置响应不被缓存。
func noStore(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.Response.Header.Set("Cache-Control", "no-store")
		next(ctx)
	}
}

func (h *Handler) get(ctx *fasthttp.RequestCtx) {
	row, err := h.store.Read(ctx)
	if errors.Is(err, persistence.ErrNotConfigured) {
		upstream.SendJSON(ctx, stateResponse{Config: nil, Version: persistence.UnsetVersion})
		return
	}
	if err != nil {
		h.storageFailure(ctx, "read", err)
		return
	}
	upstream.SendJSON(ctx, stateResponse{Config: json.RawMessage(row.Config), Version: row.Version})
}

// update 顺序：解析校验 → 构建（含 provider 校验）→ 乐观锁写库 → 热替换。构建或写库失败都不改变运行中的配置。
func (h *Handler) update(ctx *fasthttp.RequestCtx) {
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
		h.log.Error("content safety config: build failed: %v", err)
		upstream.SendError(ctx, fasthttp.StatusBadRequest, err.Error())
		return
	}
	options := safetyplugin.Options{StatusCode: cfg.Deny.Status, DenyMessage: cfg.Deny.Message}
	h.mu.Lock()
	defer h.mu.Unlock()
	row, err := h.store.Update(ctx, string(req.Config), req.Version)
	if errors.Is(err, persistence.ErrVersionConflict) {
		upstream.SendError(ctx, fasthttp.StatusConflict, "guardrails configuration version conflict; reload and retry")
		return
	}
	if err != nil {
		h.storageFailure(ctx, "update", err)
		return
	}
	if err := h.swapper.Swap(checker, options); err != nil {
		// 配置已通过校验，这里失败属于程序缺陷；如实报错并记录，不让库与运行状态静默错位。
		h.log.Error("content safety config: swap failed after persisting version %d: %v", row.Version, err)
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "guardrails configuration saved but could not be activated; restart the gateway")
		return
	}
	upstream.SendJSON(ctx, stateResponse{Config: json.RawMessage(row.Config), Version: row.Version})
}

func (h *Handler) reset(ctx *fasthttp.RequestCtx) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.store.Reset(ctx); err != nil {
		h.storageFailure(ctx, "reset", err)
		return
	}
	_ = h.swapper.Swap(nil, disabledOptions)
	upstream.SendJSON(ctx, stateResponse{Config: nil, Version: persistence.UnsetVersion})
}

// disabledOptions 是未配置时插件持有的拒绝策略；检查器为 nil 时不会用到，只需满足插件的合法性检查。
var disabledOptions = safetyplugin.Options{StatusCode: fasthttp.StatusBadRequest, DenyMessage: "内容未通过安全检查"}

// storageFailure 把存储故障的原因留在服务端日志，对外只给通用错误。
func (h *Handler) storageFailure(ctx *fasthttp.RequestCtx, op string, err error) {
	h.log.Error("content safety config: storage %s failed: %v", op, err)
	upstream.SendError(ctx, fasthttp.StatusInternalServerError, "guardrails storage unavailable")
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
