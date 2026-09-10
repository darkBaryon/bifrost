// 本文件处理品牌设置路由、JSON参数、图片校验调用和响应；不直接操作数据库。
package brandinghttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

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

type brandingResponse struct {
	Enabled   bool   `json:"enabled"`
	HasLogo   bool   `json:"has_logo"`
	HasIcon   bool   `json:"has_icon"`
	LogoURL   string `json:"logo_url,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func toBrandingResponse(row eeconfig.Settings) brandingResponse {
	response := brandingResponse{HasLogo: len(row.Logo) > 0, HasIcon: len(row.Icon) > 0}
	response.Enabled = response.HasLogo || response.HasIcon
	if response.HasLogo {
		response.LogoURL = "/api/branding/assets/logo/" + row.LogoHash
	}
	if response.HasIcon {
		response.IconURL = "/api/branding/assets/icon/" + row.IconHash
	}
	if response.Enabled {
		response.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return response
}
func brandingJSON(ctx *fasthttp.RequestCtx, row eeconfig.Settings, err error) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if err != nil {
		brandingError(ctx, err)
		return
	}
	upstream.SendJSON(ctx, toBrandingResponse(row))
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
	if len(ctx.PostBody()) > maxBrandingBodyBytes {
		upstream.SendError(ctx, fasthttp.StatusRequestEntityTooLarge, msgBodyTooLarge)
		return
	}
	var p brandingPayload
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		upstream.SendError(ctx, fasthttp.StatusBadRequest, "invalid branding JSON")
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		upstream.SendError(ctx, fasthttp.StatusBadRequest, "expected one JSON object")
		return
	}
	if len(p.Logo) == 0 && len(p.Icon) == 0 && len(p.LogoMIME) == 0 && len(p.IconMIME) == 0 {
		upstream.SendError(ctx, fasthttp.StatusBadRequest, "no branding changes supplied")
		return
	}
	logo, err := decodeBrandingAsset(p.Logo, p.LogoMIME)
	if err != nil {
		brandingError(ctx, fmt.Errorf("logo: %w", err))
		return
	}
	icon, err := decodeBrandingAsset(p.Icon, p.IconMIME)
	if err != nil {
		brandingError(ctx, fmt.Errorf("icon: %w", err))
		return
	}
	row, err := h.store.Update(ctx, eeconfig.Patch{Logo: logo, Icon: icon})
	brandingJSON(ctx, row, err)
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

// 请求体上限用于 JSON/Base64 解码前的容量保护，与前端约束一致。
const maxBrandingBodyBytes = 3 << 20

var msgBodyTooLarge = fmt.Sprintf("branding payload exceeds %d MiB", maxBrandingBodyBytes>>20)

type brandingPayload struct {
	Logo     json.RawMessage `json:"logo"`
	LogoMIME json.RawMessage `json:"logo_mime"`
	Icon     json.RawMessage `json:"icon"`
	IconMIME json.RawMessage `json:"icon_mime"`
}

func readString(raw json.RawMessage) (string, error) {
	var value string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", errors.New("null is not supported")
	}
	err := json.Unmarshal(raw, &value)
	return value, err
}

// decodeBrandingAsset 按字段解析后构造业务图片，nil 表示不修改。
func decodeBrandingAsset(raw, rawMIME json.RawMessage) (*branding.Asset, error) {
	if len(raw) == 0 {
		if len(rawMIME) > 0 {
			return nil, &branding.ValidationError{Message: "mime requires image data"}
		}
		return nil, nil
	}
	encoded, err := readString(raw)
	if err != nil {
		return nil, &branding.ValidationError{Message: "image must be a base64 string"}
	}
	mime := ""
	if len(rawMIME) > 0 {
		mime, err = readString(rawMIME)
		if err != nil {
			return nil, &branding.ValidationError{Message: "mime must be a string"}
		}
	}
	return branding.DecodeAsset(encoded, mime)
}
