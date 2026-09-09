// 本文件校验品牌请求字段和图片内容，校验失败时不产生存储操作。
package branding

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"

	eeconfig "github.com/darkBaryon/bifrost/ee/framework/configstore/branding"
	"github.com/valyala/fasthttp"
)

// 上传限制与 ee/ui/app/enterprise/lib/schemas/branding.ts 保持一致。
const (
	maxBrandingImageBytes = 1 << 20   // 单张图片解码后的上限，1 MiB
	maxBrandingBodyBytes  = 3 << 20   // Base64 解码前整个 JSON 请求体的上限，3 MiB
	maxBrandingEdgePx     = 4096      // 单边像素上限
	maxBrandingPixels     = 4_000_000 // 宽乘高的总像素上限
)

var (
	msgImageTooLarge   = fmt.Sprintf("image exceeds %d MiB", maxBrandingImageBytes>>20)
	msgBodyTooLarge    = fmt.Sprintf("branding payload exceeds %d MiB", maxBrandingBodyBytes>>20)
	msgImageDimensions = fmt.Sprintf("image dimensions exceed %d px or %d million pixels", maxBrandingEdgePx, maxBrandingPixels/1_000_000)
)

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

// decodeBrandingAsset 区分不修改（缺字段）、清除（空串）和替换（有效图片）。
// 返回的状态码为 0 表示通过，否则同时返回可公开的校验提示。
func decodeBrandingAsset(raw, rawMIME json.RawMessage) (*eeconfig.BrandingAsset, int, string) {
	if len(raw) == 0 {
		if len(rawMIME) > 0 {
			return nil, fasthttp.StatusBadRequest, "mime requires image data"
		}
		return nil, 0, ""
	}
	encoded, err := readString(raw)
	if err != nil {
		return nil, fasthttp.StatusBadRequest, "image must be a base64 string"
	}
	mime := ""
	if len(rawMIME) > 0 {
		mime, err = readString(rawMIME)
		if err != nil {
			return nil, fasthttp.StatusBadRequest, "mime must be a string"
		}
	}
	if encoded == "" {
		if mime != "" {
			return nil, fasthttp.StatusBadRequest, "mime requires image data"
		}
		return &eeconfig.BrandingAsset{}, 0, ""
	}
	if strings.HasPrefix(encoded, "data:") {
		header, data, ok := strings.Cut(encoded, ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return nil, fasthttp.StatusBadRequest, "invalid image data URI"
		}
		uriMIME := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		if mime != "" && mime != uriMIME {
			return nil, fasthttp.StatusBadRequest, "image mime mismatch"
		}
		mime = uriMIME
		encoded = data
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(maxBrandingImageBytes) {
		return nil, fasthttp.StatusRequestEntityTooLarge, msgImageTooLarge
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fasthttp.StatusBadRequest, "invalid image base64"
	}
	if len(data) > maxBrandingImageBytes {
		return nil, fasthttp.StatusRequestEntityTooLarge, msgImageTooLarge
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, fasthttp.StatusBadRequest, "only valid PNG and JPEG images are supported"
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxBrandingEdgePx || cfg.Height > maxBrandingEdgePx || int64(cfg.Width)*int64(cfg.Height) > maxBrandingPixels {
		return nil, fasthttp.StatusBadRequest, msgImageDimensions
	}
	actualMIME := "image/" + format
	if mime != "" && mime != actualMIME {
		return nil, fasthttp.StatusBadRequest, "image mime mismatch"
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, fasthttp.StatusBadRequest, "image is damaged or incomplete"
	}
	return &eeconfig.BrandingAsset{Data: data, MIME: actualMIME}, 0, ""
}
