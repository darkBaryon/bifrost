// 本文件处理品牌设置的查询、保存、重置及响应转换。
package brandinghttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/branding"
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

func toBrandingResponse(row branding.Settings) brandingResponse {
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
func brandingJSON(ctx *fasthttp.RequestCtx, row branding.Settings, err error) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	if err != nil {
		brandingError(ctx, err)
		return
	}
	upstream.SendJSON(ctx, toBrandingResponse(row))
}
func (h *BrandingHandler) get(ctx *fasthttp.RequestCtx) {
	row, err := h.service.Read(ctx)
	brandingJSON(ctx, row, err)
}
func (h *BrandingHandler) reset(ctx *fasthttp.RequestCtx) {
	row, err := h.service.Reset(ctx)
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
	row, err := h.service.Update(ctx, branding.Patch{Logo: logo, Icon: icon})
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
	} else if errors.Is(err, branding.ErrAssetNotFound) {
		upstream.SendError(ctx, fasthttp.StatusNotFound, branding.ErrAssetNotFound.Error())
	} else {
		upstream.SendError(ctx, fasthttp.StatusInternalServerError, "branding storage unavailable")
	}
}
