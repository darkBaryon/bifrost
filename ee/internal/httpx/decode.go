// 本文件负责控制台接口的严格 JSON 对象解码，兼容旧接口的空正文由调用方显式选择。
package httpx

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"unicode/utf8"

	"github.com/valyala/fasthttp"
)

// MaxBodyBytes 是复用严格对象解码的控制台接口正文上限；完整宿主配置另有自己的限制。
const MaxBodyBytes = 16 << 10

const maxJSONDepth = 64 // 沿用控制台协议的对象与数组嵌套深度上限。

// DecodeJSONObject 要求非空的单个 JSON 对象，并检查媒体类型、大小、UTF-8、重复键与未知字段。
// v 应为请求 DTO 的指针；空结构体指针只接受空对象。
func DecodeJSONObject(c *fasthttp.RequestCtx, v any) error {
	return decode(c, v, false)
}

// DecodeOptionalJSONObject 沿用旧会话接口的空正文兼容：空正文按 {} 解码，且无需 JSON 媒体类型。
// 非空正文遵循 DecodeJSONObject 的所有检查。
func DecodeOptionalJSONObject(c *fasthttp.RequestCtx, v any) error {
	return decode(c, v, true)
}

func decode(c *fasthttp.RequestCtx, v any, allowEmpty bool) error {
	body := c.PostBody()
	if len(body) > MaxBodyBytes || !utf8.Valid(body) {
		return ErrInvalid
	}
	media, _, err := mime.ParseMediaType(string(c.Request.Header.ContentType()))
	if err != nil || media != "application/json" {
		if !allowEmpty || len(body) != 0 {
			return ErrInvalid
		}
	}
	if allowEmpty && len(body) == 0 {
		body = []byte("{}")
	}
	if err := ValidateJSONObject(body); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil {
		return ErrInvalid
	}
	return nil
}

// ValidateJSONObject 要求正文是单个 JSON 对象，拒绝重复键、多个值和过深嵌套。
// 本函数只检查 JSON 结构；大小、媒体类型和 DTO 字段检查由入口负责。
func ValidateJSONObject(body []byte) error {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var walk func(depth int) error
	walk = func(depth int) error {
		if depth > maxJSONDepth {
			return ErrInvalid
		}
		token, err := d.Token()
		if err != nil {
			return ErrInvalid
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return ErrInvalid
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return ErrInvalid
				}
				seen[k] = true
				if err = walk(depth + 1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim('}') {
				return ErrInvalid
			}
		case '[':
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			if end, err := d.Token(); err != nil || end != json.Delim(']') {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	return nil
}
