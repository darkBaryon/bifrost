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

// Asset 保存解码后的图片字节和内容识别出的 MIME 类型，空图片用于清除。
type Asset struct {
	Data []byte
	MIME string
}

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
	return &Asset{Data: data, MIME: actualMIME}, nil
}

// ValidationError 是可向用户说明的图片输入错误，TooLarge 区分容量超限。
type ValidationError struct {
	Message  string
	TooLarge bool
}

// Error 返回可公开的校验说明。
func (e *ValidationError) Error() string { return e.Message }
