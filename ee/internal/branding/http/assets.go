// 本文件读取当前品牌图片，处理不存在的版本及 HTTP 缓存。
package brandinghttp

import (
	"strings"

	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/valyala/fasthttp"
)

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
