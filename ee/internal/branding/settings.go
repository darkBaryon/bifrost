// 本文件定义品牌设置及局部修改的业务类型，不包含数据库或 HTTP 字段。
package branding

import "time"

// Settings 是整个部署共用的品牌设置，图片版本由内容哈希标识。
type Settings struct {
	Logo      []byte
	LogoMIME  string
	LogoHash  string
	Icon      []byte
	IconMIME  string
	IconHash  string
	UpdatedAt time.Time
}

// Patch 中 nil 表示保持原图，零值 Asset 表示清除，否则替换为已验证图片。
type Patch struct {
	Logo *Asset
	Icon *Asset
}
