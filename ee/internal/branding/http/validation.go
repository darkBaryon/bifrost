// 本文件解析品牌 JSON 字段，保留缺字段、空串和 null 的不同含义。
package brandinghttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkBaryon/bifrost/ee/internal/branding"
)

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
