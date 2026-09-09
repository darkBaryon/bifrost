// 本文件处理品牌配置的读取、按字段保存和重置。
//
// Package branding 负责品牌配置的存储与迁移，不处理 HTTP 请求或界面状态。
package branding

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BrandingAsset 保存解码后的图片字节及其 MIME 类型。
type BrandingAsset struct {
	Data []byte
	MIME string
}

// BrandingPatch 表示两个图片位置的局部修改；nil 保持原值，空图片清除原值。
type BrandingPatch struct {
	Logo *BrandingAsset
	Icon *BrandingAsset
}

// BrandingStore 通过已有数据库连接读写当前部署的品牌配置。
type BrandingStore struct{ db *gorm.DB }

// NewBrandingStore 复用传入连接，不负责关闭连接。
func NewBrandingStore(db *gorm.DB) *BrandingStore { return &BrandingStore{db: db} }

// Read 读取品牌单例；读取失败时返回错误，不将故障当成默认配置。
func (s *BrandingStore) Read(ctx context.Context) (Branding, error) {
	var row Branding
	err := s.db.WithContext(ctx).First(&row, 1).Error
	return row, err
}

// Update 只更新 patch 指定的图片位置，并在同一事务中读取保存结果。
// 任一步失败都会回滚，未指定的位置保持原值。
func (s *BrandingStore) Update(ctx context.Context, patch BrandingPatch) (Branding, error) {
	var result Branding
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := Branding{ID: 1, UpdatedAt: time.Now().UTC()}
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
func assetColumns(asset *BrandingAsset) (data []byte, mime, hash string) {
	if len(asset.Data) == 0 {
		return nil, "", ""
	}
	return asset.Data, asset.MIME, fmt.Sprintf("%x", sha256.Sum256(asset.Data))
}

// Reset 清除 Logo 和小图标；重复调用仍保持默认状态。
func (s *BrandingStore) Reset(ctx context.Context) (Branding, error) {
	return s.Update(ctx, BrandingPatch{Logo: &BrandingAsset{}, Icon: &BrandingAsset{}})
}
