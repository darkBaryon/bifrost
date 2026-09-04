package handlers

import (
	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"gorm.io/gorm"

	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"

	"github.com/darkBaryon/bifrost/ee/transports/bifrost-http/lib"
)

// ProbeHandler 提供 GET /api/ee/ping: 证明 "上游 Bootstrap 之后仍可往 s.Router 加路由".
// 注意: 这是无鉴权路由 (骨架探针). ee 的正式 API 路由必须包 s.AuthMiddleware.APIMiddleware().
type ProbeHandler struct {
	countRows func() (int64, error)
	plugin    string
}

// NewProbeHandler 用上游的数据库连接数 ee_probe 的行数, 顺便证明表已建成.
func NewProbeHandler(db *gorm.DB, pluginName string) *ProbeHandler {
	return &ProbeHandler{
		countRows: func() (int64, error) {
			var n int64
			err := db.Model(&lib.Probe{}).Count(&n).Error
			return n, err
		},
		plugin: pluginName,
	}
}

// RegisterRoutes 形状照上游 handler.
func (h *ProbeHandler) RegisterRoutes(r *router.Router) {
	r.GET("/api/ee/ping", h.ping)
}

type pingResponse struct {
	OK        bool   `json:"ok"`
	ProbeRows int64  `json:"probe_rows"`
	Plugin    string `json:"plugin"`
}

func (h *ProbeHandler) ping(ctx *fasthttp.RequestCtx) {
	n, err := h.countRows()
	if err != nil {
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "ee_probe count failed: "+err.Error())
		return
	}
	upstream.SendJSON(ctx, pingResponse{OK: true, ProbeRows: n, Plugin: h.plugin})
}
