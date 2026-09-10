// 本文件投递当前版本的品牌图片，并处理缓存与条件请求。
package brandinghttp

import (
	"strings"

	"github.com/valyala/fasthttp"
)

func (h *BrandingHandler) asset(ctx *fasthttp.RequestCtx) {
	// 先禁止缓存错误响应，确认图片存在后才设置成功响应的缓存策略。
	ctx.Response.Header.Set("Cache-Control", "no-store")
	slot, _ := ctx.UserValue("slot").(string)
	hash, _ := ctx.UserValue("hash").(string)
	asset, err := h.service.Asset(ctx, slot, hash)
	if err != nil {
		brandingError(ctx, err)
		return
	}
	etag := `"` + hash + `"`
	ctx.Response.Header.SetContentType(asset.MIME())
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
	ctx.SetBody(asset.Data())
}
