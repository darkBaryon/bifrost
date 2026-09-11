// 本文件定义品牌接口的请求与响应，负责字段解析、图片校验调用和响应转换。
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
)

// 请求体上限用于 JSON/Base64 解码前的容量保护，与前端约束一致。
const maxBrandingBodyBytes = 3 << 20

var msgBodyTooLarge = fmt.Sprintf("branding payload exceeds %d MiB", maxBrandingBodyBytes>>20)

type updateRequest struct {
	Logo     json.RawMessage `json:"logo"`
	LogoMIME json.RawMessage `json:"logo_mime"`
	Icon     json.RawMessage `json:"icon"`
	IconMIME json.RawMessage `json:"icon_mime"`
}

type settingsResponse struct {
	Enabled   bool   `json:"enabled"`
	HasLogo   bool   `json:"has_logo"`
	HasIcon   bool   `json:"has_icon"`
	LogoURL   string `json:"logo_url,omitempty"`
	IconURL   string `json:"icon_url,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// parseUpdateRequest 完整解析并校验两张图片，成功后才交给 Store 保存。
func parseUpdateRequest(body []byte) (eeconfig.Patch, error) {
	if len(body) > maxBrandingBodyBytes {
		return eeconfig.Patch{}, &branding.ValidationError{Message: msgBodyTooLarge, TooLarge: true}
	}
	var p updateRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return eeconfig.Patch{}, &branding.ValidationError{Message: "invalid branding JSON"}
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return eeconfig.Patch{}, &branding.ValidationError{Message: "expected one JSON object"}
	}
	if len(p.Logo) == 0 && len(p.Icon) == 0 && len(p.LogoMIME) == 0 && len(p.IconMIME) == 0 {
		return eeconfig.Patch{}, &branding.ValidationError{Message: "no branding changes supplied"}
	}
	logo, err := decodeBrandingAsset(p.Logo, p.LogoMIME)
	if err != nil {
		return eeconfig.Patch{}, fmt.Errorf("logo: %w", err)
	}
	icon, err := decodeBrandingAsset(p.Icon, p.IconMIME)
	if err != nil {
		return eeconfig.Patch{}, fmt.Errorf("icon: %w", err)
	}
	return eeconfig.Patch{Logo: logo, Icon: icon}, nil
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

func toSettingsResponse(row eeconfig.Settings) settingsResponse {
	response := settingsResponse{HasLogo: len(row.Logo) > 0, HasIcon: len(row.Icon) > 0}
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
