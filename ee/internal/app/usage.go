// 本文件把模板管理接到现有身份路由；不装配任何个人扣额逻辑。
package app

import (
	eehost "github.com/darkBaryon/bifrost/ee/internal/host"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/core/schemas"
)

type consoleRoutes []eehost.AdditionalRoutes

func (routes consoleRoutes) RegisterRoutes(r *router.Router, m ...schemas.BifrostHTTPMiddleware) {
	for _, handler := range routes {
		handler.RegisterRoutes(r, m...)
	}
}
func (routes consoleRoutes) OwnsRoute(method, path string) bool {
	for _, handler := range routes {
		if handler.OwnsRoute(method, path) {
			return true
		}
	}
	return false
}
