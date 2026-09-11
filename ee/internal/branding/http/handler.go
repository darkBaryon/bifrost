// 本文件注册品牌路由，处理设置查询、保存、重置和图片响应。
package brandinghttp

import (
	"errors"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/branding"
	eeconfig "github.com/darkBaryon/bifrost/ee/internal/branding/persistence"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/valyala/fasthttp"
)

// BrandingHandler 处理品牌设置与图片请求，写操作复用管理端鉴权。
type BrandingHandler struct{ store *eeconfig.BrandingStore }

// NewBrandingHandler 注入品牌存储创建处理器，不创建数据库连接。
func NewBrandingHandler(store *eeconfig.BrandingStore) *BrandingHandler {
	return &BrandingHandler{store: store}
}

// RegisterRoutes 注册品牌路由；读取公开以供登录页使用，写入必须经过 auth。
// auth 缺失时返回错误，不注册任何路由。
func (h *BrandingHandler) RegisterRoutes(r *router.Router, auth schemas.BifrostHTTPMiddleware) error {
	if auth == nil {
		return errors.New("branding requires admin authentication middleware")
	}
	r.POST("/api/branding/get", h.get)
	r.POST("/api/branding/update", auth(h.update))
	r.POST("/api/branding/reset", auth(h.reset))
	r.GET("/api/branding/assets/{slot}/{hash}", h.asset)
	return nil
}

func (h *BrandingHandler) get(ctx *fasthttp.RequestCtx) {
	row, err := h.store.Read(ctx)
	brandingJSON(ctx, row, err)
}

func (h *BrandingHandler) reset(ctx *fasthttp.RequestCtx) {
	row, err := h.store.Reset(ctx)
	brandingJSON(ctx, row, err)
}

func (h *BrandingHandler) update(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	patch, err := parseUpdateRequest(ctx.PostBody())
	if err != nil {
		brandingError(ctx, err)
		return
	}
	row, err := h.store.Update(ctx, patch)
	brandingJSON(ctx, row, err)
}

func (h *BrandingHandler) asset(ctx *fasthttp.RequestCtx) {
	// 先禁止缓存错误响应，确认图片存在后才设置成功响应的缓存策略。
	ctx.Response.Header.Set("Cache-Control", "no-store")
	slot, _ := ctx.UserValue("slot").(string)
	hash, _ := ctx.UserValue("hash").(string)
	if (slot != "logo" && slot != "icon") || len(hash) != 64 {
		upstream.SendError(ctx, fasthttp.StatusNotFound, "branding asset not found")
		return
	}
	settings, err := h.store.Read(ctx)
	if err != nil {
		brandingError(ctx, err)
		return
	}
	data, mime, currentHash := settings.Logo, settings.LogoMIME, settings.LogoHash
	if slot == "icon" {
		data, mime, currentHash = settings.Icon, settings.IconMIME, settings.IconHash
	}
	if len(data) == 0 || currentHash != hash {
		upstream.SendError(ctx, fasthttp.StatusNotFound, "branding asset not found")
		return
	}
	etag := `"` + hash + `"`
	ctx.Response.Header.SetContentType(mime)
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Cache-Control", "public, max-age=0, must-revalidate")
	ctx.Response.Header.Set("ETag", etag)
	for _, candidate := range strings.Split(string(ctx.Request.Header.Peek("If-None-Match")), ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
			ctx.SetStatusCode(fasthttp.StatusNotModified)
			return
		}
	}
	ctx.SetBody(data)
}

func brandingJSON(ctx *fasthttp.RequestCtx, row eeconfig.Settings, err error) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if err != nil {
		brandingError(ctx, err)
		return
	}
	upstream.SendJSON(ctx, toSettingsResponse(row))
}

// brandingError 在 HTTP 边界映射业务错误，存储故障不暴露内部细节。
func brandingError(ctx *fasthttp.RequestCtx, err error) {
	var validation *branding.ValidationError
	if errors.As(err, &validation) {
		status := fasthttp.StatusBadRequest
		if validation.TooLarge {
			status = fasthttp.StatusRequestEntityTooLarge
		}
		upstream.SendError(ctx, status, err.Error())
	} else {
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "branding storage unavailable")
	}
}
