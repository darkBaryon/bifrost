// 本文件处理品牌设置的查询、保存、重置及响应转换。
package branding

import (
	"bytes"
	"encoding/json"
	"io"
	"time"

	eeconfig "github.com/darkBaryon/bifrost/ee/framework/configstore/branding"
	upstream "github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/valyala/fasthttp"
)

type brandingResponse struct {
	Enabled   bool   `json:"enabled"`
	HasLogo   bool   `json:"has_logo"`
	HasIcon   bool   `json:"has_icon"`
	LogoURL   string `json:"logo_url,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func toBrandingResponse(row eeconfig.Branding) brandingResponse {
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
func brandingJSON(ctx *fasthttp.RequestCtx, row eeconfig.Branding, err error) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if err != nil {
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "branding storage unavailable")
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
	logo, status, msg := decodeBrandingAsset(p.Logo, p.LogoMIME)
	if status != 0 {
		upstream.SendError(ctx, status, "logo: "+msg)
		return
	}
	icon, status, msg := decodeBrandingAsset(p.Icon, p.IconMIME)
	if status != 0 {
		upstream.SendError(ctx, status, "icon: "+msg)
		return
	}
	row, err := h.store.Update(ctx, eeconfig.BrandingPatch{Logo: logo, Icon: icon})
	brandingJSON(ctx, row, err)
}
