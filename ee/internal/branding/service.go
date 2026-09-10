// Package branding 负责部署级品牌规则，通过窄接口访问存储。
// 本文件实现设置读取、局部修改、恢复默认与当前图片选择。
package branding

import "context"

// Service 供 HTTP 等入口复用品牌操作，不持有数据库或上游服务对象。
type Service struct{ repository Repository }

// NewService 注入现有存储，不创建或关闭共享资源。
func NewService(repository Repository) *Service { return &Service{repository: repository} }

// Read 读取设置，存储故障不会被转换为默认值。
func (s *Service) Read(ctx context.Context) (Settings, error) {
	return s.repository.Read(ctx)
}

// Update 保存已通过 DecodeAsset 构造的图片，拒绝无实际字段的请求。
func (s *Service) Update(ctx context.Context, patch Patch) (Settings, error) {
	if patch.Logo == nil && patch.Icon == nil {
		return Settings{}, &ValidationError{Message: "no branding changes supplied"}
	}
	return s.repository.Update(ctx, patch)
}

// Reset 原子清除两张图片，重复调用仍保持默认状态。
func (s *Service) Reset(ctx context.Context) (Settings, error) {
	return s.Update(ctx, Patch{Logo: &Asset{}, Icon: &Asset{}})
}

// Asset 只返回当前版本的图片；无效路径先于存储访问判定。
func (s *Service) Asset(ctx context.Context, slot, hash string) (Asset, error) {
	if (slot != "logo" && slot != "icon") || len(hash) != 64 {
		return Asset{}, ErrAssetNotFound
	}
	settings, err := s.repository.Read(ctx)
	if err != nil {
		return Asset{}, err
	}
	data, mime, currentHash := settings.Logo, settings.LogoMIME, settings.LogoHash
	if slot == "icon" {
		data, mime, currentHash = settings.Icon, settings.IconMIME, settings.IconHash
	}
	if len(data) == 0 || currentHash != hash {
		return Asset{}, ErrAssetNotFound
	}
	return Asset{data: data, mime: mime}, nil
}
