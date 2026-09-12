// 本文件集中定义品牌表、修改参数和事务读写；复用注入的数据库连接。
package persistence

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/branding"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Settings 是整个部署共用的品牌配置单例，ID 固定为 1。
// 图片存在配置数据库中，资源 URL 由内容哈希生成。
type Settings struct {
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
func (Settings) TableName() string { return "ee_branding" }

// Patch 表示局部修改：nil 保持，空图片清除；图片应先经过 branding.DecodeAsset 校验。
type Patch struct {
	Logo *branding.Asset
	Icon *branding.Asset
}

// BrandingStore 通过已有数据库连接读写当前部署的品牌配置。
type BrandingStore struct{ db *gorm.DB }

// NewBrandingStore 复用传入连接，不负责关闭连接。
func NewBrandingStore(db *gorm.DB) *BrandingStore { return &BrandingStore{db: db} }

// Read 读取品牌单例；读取失败时返回错误，不将故障当成默认配置。
func (s *BrandingStore) Read(ctx context.Context) (Settings, error) {
	var row Settings
	err := s.db.WithContext(ctx).First(&row, 1).Error
	return row, err
}

// Update 只更新 patch 指定的图片位置，并在同一事务中读取保存结果。
// 任一步失败都会回滚，未指定的位置保持原值。
func (s *BrandingStore) Update(ctx context.Context, patch Patch) (Settings, error) {
	var result Settings
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := Settings{ID: 1, UpdatedAt: time.Now().UTC()}
		columns := []string{"updated_at"}
		if patch.Logo != nil {
			row.Logo, row.LogoMIME, row.LogoHash = assetColumns(patch.Logo)
			columns = append(columns, "logo", "logo_mime", "logo_hash")
		}
		if patch.Icon != nil {
			row.Icon, row.IconMIME, row.IconHash = assetColumns(patch.Icon)
			columns = append(columns, "icon", "icon_mime", "icon_hash")
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns(columns),
		}).Create(&row).Error; err != nil {
			return err
		}
		return tx.First(&result, 1).Error
	})
	return result, err
}

// assetColumns 同时生成图片字节、类型和内容哈希，清除图片时三者一起清空。
func assetColumns(asset *branding.Asset) (data []byte, mime, hash string) {
	data = asset.Data
	if len(data) == 0 {
		return nil, "", ""
	}
	return data, asset.MIME, fmt.Sprintf("%x", sha256.Sum256(data))
}

// Reset 通过一次原子更新清除两张图片。
func (s *BrandingStore) Reset(ctx context.Context) (Settings, error) {
	return s.Update(ctx, Patch{Logo: &branding.Asset{}, Icon: &branding.Asset{}})
}
