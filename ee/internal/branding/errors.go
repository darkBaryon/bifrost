// 本文件定义可供入口适配识别的品牌业务错误。
package branding

import "errors"

// ErrAssetNotFound 表示图片位置、版本或当前图片不存在。
var ErrAssetNotFound = errors.New("branding asset not found")

// ValidationError 表示可公开的输入错误，TooLarge 区分上传容量超限。
type ValidationError struct {
	Message  string
	TooLarge bool
}

// Error 返回可公开的校验说明，HTTP 入口再补充图片位置。
func (e *ValidationError) Error() string { return e.Message }
