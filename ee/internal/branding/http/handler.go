// 本文件定义品牌请求处理器并注册路由。
//
// Package brandinghttp 负责品牌 HTTP 字段解析、路由与响应，不负责数据库存取。
package brandinghttp

import (
	"errors"

	"github.com/darkBaryon/bifrost/ee/internal/branding"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
)

// BrandingHandler 处理品牌设置与图片请求，写操作复用管理端鉴权。
type BrandingHandler struct{ service *branding.Service }

// NewBrandingHandler 注入品牌服务创建处理器，不创建数据库连接。
func NewBrandingHandler(service *branding.Service) *BrandingHandler {
	return &BrandingHandler{service: service}
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
