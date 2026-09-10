// 本文件定义品牌配置表及固定表名。
package persistence

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/branding"
)

// brandingRow 是整个部署共用的品牌配置单例，ID 固定为 1。
// 图片存在配置数据库中，资源 URL 由内容哈希生成。
type brandingRow struct {
	ID        int `gorm:"primaryKey;autoIncrement:false"`
	Logo      []byte
	LogoMIME  string
	LogoHash  string
	Icon      []byte
	IconMIME  string
	IconHash  string
	UpdatedAt time.Time
}

// TableName 保持既有表名，Go 包路径变化不影响已保存的数据。
func (brandingRow) TableName() string { return "ee_branding" }

// settings 将数据库行转换为业务设置，ID 与 ORM 细节留在存储包。
func (row brandingRow) settings() branding.Settings {
	return branding.Settings{Logo: row.Logo, LogoMIME: row.LogoMIME, LogoHash: row.LogoHash, Icon: row.Icon, IconMIME: row.IconMIME, IconHash: row.IconHash, UpdatedAt: row.UpdatedAt}
}
