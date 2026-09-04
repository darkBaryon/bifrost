package handlers

import (
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"

	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"
)

// BrandingHandler 提供 GET /api/branding, 只返回 "全部默认" 状态.
//
// 背景: 只要 ui/app/enterprise 存在, 上游 vite 就把控制台编成企业模式 (IS_ENTERPRISE=true),
// 此时 useBranding 每次整页加载都会请求 /api/branding; OSS 后端没有这个 handler, 请求会被
// /{filepath:*} 兜底成 200 的 index.html, 前端解析 JSON 失败. 骨架期先给一个合法的默认值;
// 上传/存储 (PUT/DELETE) 到 B8 换皮时再做. 契约见 ui/lib/store/apis/brandingApi.ts (BrandingState).
// 该路由按上游设计是公开的 (登录页也要读), 故不包鉴权中间件.
type BrandingHandler struct{}

func NewBrandingHandler() *BrandingHandler { return &BrandingHandler{} }

func (h *BrandingHandler) RegisterRoutes(r *router.Router) {
	r.GET("/api/branding", h.get)
}

type brandingState struct {
	Enabled bool `json:"enabled"`
	HasLogo bool `json:"has_logo"`
	HasIcon bool `json:"has_icon"`
}

func (h *BrandingHandler) get(ctx *fasthttp.RequestCtx) {
	upstream.SendJSON(ctx, brandingState{})
}
