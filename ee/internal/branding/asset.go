// 本文件构造有效品牌图片，负责编码、格式、MIME、容量与尺寸校验。
package branding

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
)

// MaxImageBytes 是单张图片字节上限，与前端上传约束保持一致。
const MaxImageBytes = 1 << 20
const (
	maxEdgePx = 4096
	maxPixels = 4_000_000
)

var (
	msgImageTooLarge   = fmt.Sprintf("image exceeds %d MiB", MaxImageBytes>>20)
	msgImageDimensions = fmt.Sprintf("image dimensions exceed %d px or %d million pixels", maxEdgePx, maxPixels/1_000_000)
)

// Asset 是已验证图片；字段私有以避免绕过内容校验，零值用于清除图片。
type Asset struct {
	data []byte
	mime string
}

// Data 返回图片副本，调用方修改返回值不会改变已验证内容。
func (a Asset) Data() []byte { return bytes.Clone(a.data) }

// MIME 返回经内容识别的图片类型；清除图片时为空。
func (a Asset) MIME() string { return a.mime }

// DecodeAsset 接受 Base64 或 data URI，空串表示清除；失败不产生可保存的值。
func DecodeAsset(encoded, mime string) (*Asset, error) {
	if encoded == "" {
		if mime != "" {
			return nil, &ValidationError{Message: "mime requires image data"}
		}
		return &Asset{}, nil
	}
	if strings.HasPrefix(encoded, "data:") {
		header, data, ok := strings.Cut(encoded, ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return nil, &ValidationError{Message: "invalid image data URI"}
		}
		uriMIME := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		if mime != "" && mime != uriMIME {
			return nil, &ValidationError{Message: "image mime mismatch"}
		}
		mime = uriMIME
		encoded = data
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxImageBytes) {
		return nil, &ValidationError{Message: msgImageTooLarge, TooLarge: true}
	}
	data, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, &ValidationError{Message: "invalid image base64"}
	}
	if len(data) > MaxImageBytes {
		return nil, &ValidationError{Message: msgImageTooLarge, TooLarge: true}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return nil, &ValidationError{Message: "only valid PNG and JPEG images are supported"}
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxEdgePx || cfg.Height > maxEdgePx || int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, &ValidationError{Message: msgImageDimensions}
	}
	actualMIME := "image/" + format
	if mime != "" && mime != actualMIME {
		return nil, &ValidationError{Message: "image mime mismatch"}
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, &ValidationError{Message: "image is damaged or incomplete"}
	}
	return &Asset{data: data, mime: actualMIME}, nil
}
