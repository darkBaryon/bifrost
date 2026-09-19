// 本文件定义配置表与带版本号的读写；配置正文按 JSON 原样保存，解析与校验在 config 包。
package persistence

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Row 是整个部署共用的内容安全配置单例，ID 固定为 1；不存在即未配置。
type Row struct {
	ID        int    `gorm:"primaryKey;autoIncrement:false"`
	Config    string `gorm:"type:text"`
	Version   int
	UpdatedAt time.Time
}

// TableName 固定表名，Go 包路径变化不影响已保存的数据。
func (Row) TableName() string { return "ee_guardrails" }

// 单例 ID 与"未配置"的版本号约定：未配置时 Read 返回 ErrNotConfigured、接口返回版本 0，首次写入须带 0。
const (
	singletonID    = 1
	UnsetVersion   = 0
	firstVersionNo = 1
)

// 未配置与版本冲突分别用哨兵错误表达，HTTP 层据此选状态码。
var (
	ErrNotConfigured   = errors.New("guardrails: not configured")
	ErrVersionConflict = errors.New("guardrails: version conflict")
)

// Store 通过已有数据库连接读写配置。
type Store struct{ db *gorm.DB }

// NewStore 复用传入连接，不负责关闭连接。
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Read 返回当前配置；无行返回 ErrNotConfigured，其他错误原样返回，不把故障当成未配置。
func (s *Store) Read(ctx context.Context) (Row, error) {
	var row Row
	err := s.db.WithContext(ctx).First(&row, singletonID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Row{}, ErrNotConfigured
	}
	return row, err
}

// Update 以乐观锁写入：expectedVersion 必须等于当前版本（未配置为 UnsetVersion），成功后版本加一。
func (s *Store) Update(ctx context.Context, configJSON string, expectedVersion int) (Row, error) {
	var result Row
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Row
		err := tx.First(&current, singletonID).Error
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			if expectedVersion != UnsetVersion {
				return ErrVersionConflict
			}
			result = Row{ID: singletonID, Config: configJSON, Version: firstVersionNo, UpdatedAt: time.Now().UTC()}
			// 并发首次写入时只有一方能插入；另一方主键冲突不报错但影响 0 行，按版本冲突处理。
			insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&result)
			if insert.Error != nil {
				return insert.Error
			}
			if insert.RowsAffected != 1 {
				return ErrVersionConflict
			}
			return nil
		case err != nil:
			return err
		case current.Version != expectedVersion:
			return ErrVersionConflict
		}
		result = Row{ID: singletonID, Config: configJSON, Version: current.Version + 1, UpdatedAt: time.Now().UTC()}
		// 条件更新必须恰好影响一行；读到的版本在此期间被改掉时影响 0 行，同样是版本冲突。
		update := tx.Model(&Row{}).Where("id = ? AND version = ?", singletonID, expectedVersion).
			Updates(map[string]any{"config": result.Config, "version": result.Version, "updated_at": result.UpdatedAt})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrVersionConflict
		}
		return nil
	})
	return result, err
}

// Reset 删除配置行；本来就没有也算成功。
func (s *Store) Reset(ctx context.Context) error {
	return s.db.WithContext(ctx).Delete(&Row{}, singletonID).Error
}
