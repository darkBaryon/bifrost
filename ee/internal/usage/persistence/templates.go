// Package persistence 在身份事务内检查权限并保存额度模板。
package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	policy "github.com/darkBaryon/bifrost/ee/internal/rbac/identity"
	rbacstore "github.com/darkBaryon/bifrost/ee/internal/rbac/persistence"
	"github.com/darkBaryon/bifrost/ee/internal/usage"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Scope 由身份存储提供，事务内复核身份与权限，视图不得逸出。
type Scope func(context.Context, func(*gorm.DB, identity.Queries) error) error

// Store 使用宿主身份事务，不拥有数据库连接。
type Store struct{ read, write Scope }

// NewStore 绑定读快照和写事务；缺少范围时操作拒绝。
func NewStore(read, write Scope) *Store { return &Store{read, write} }

type templateRow struct {
	ID                            string `gorm:"primaryKey"`
	Name, Description, ConfigJSON string
	Version                       int64
	CreatedAt, UpdatedAt          time.Time
}

func (templateRow) TableName() string { return "ee_usage_templates" }

// 独立持久结构避免业务类型增字段改变已保存的配置格式。
type configRecord struct {
	MaxCostUSD           float64
	ResetDuration        usage.ResetDuration
	AllowedModels        []string
	TokenMax, RequestMax int64
	WindowSeconds        int
}

func encode(c usage.Config) string {
	v := configRecord{MaxCostUSD: c.MaxCostUSD, ResetDuration: c.ResetDuration, AllowedModels: c.AllowedModels}
	if c.RateLimit != nil {
		v.TokenMax, v.RequestMax, v.WindowSeconds = c.RateLimit.TokenMax, c.RateLimit.RequestMax, c.RateLimit.WindowSeconds
	}
	b, _ := json.Marshal(v) // Normalize已拒绝非有限金额；其余字段都是JSON可表示的值。
	return string(b)
}

func (r templateRow) template() (usage.Template, error) {
	var v configRecord
	if e := json.Unmarshal([]byte(r.ConfigJSON), &v); e != nil {
		return usage.Template{}, e
	}
	c := usage.Config{MaxCostUSD: v.MaxCostUSD, ResetDuration: v.ResetDuration, AllowedModels: v.AllowedModels}
	if v.WindowSeconds != 0 {
		c.RateLimit = &usage.RateLimit{TokenMax: v.TokenMax, RequestMax: v.RequestMax, WindowSeconds: v.WindowSeconds}
	}
	return usage.Template{ID: r.ID, Name: r.Name, Description: r.Description, Config: c, Version: r.Version}, nil
}

func within(ctx context.Context, scope Scope, actor identity.Principal, permission rbac.Permission, fn func(*gorm.DB) error) error {
	if scope == nil {
		return identity.ErrUnavailable
	}
	e := scope(ctx, func(db *gorm.DB, view identity.Queries) error {
		if e := rbac.New(rbacstore.Bind(db, view)).Authorize(ctx, policy.Subject(actor), permission); e != nil {
			return policy.IdentityError(e)
		}
		return fn(db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}))
	})
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return identity.ErrNotFound
	}
	return identity.SafeError(e)
}

// List 按ID分页；next为空代表没有下一页。
func (s *Store) List(ctx context.Context, actor identity.Principal, after string, limit int) (items []usage.Template, next string, err error) {
	items = []usage.Template{}
	if limit < 1 || limit > usage.MaxPageSize {
		return items, "", identity.ErrInvalid
	}
	err = within(ctx, s.read, actor, rbac.UsageView, func(db *gorm.DB) error {
		items, next = []usage.Template{}, ""
		var rows []templateRow
		if e := db.Where("id > ?", after).Order("id ASC").Limit(limit + 1).Find(&rows).Error; e != nil {
			return e
		}
		if len(rows) > limit {
			rows = rows[:limit]
			next = rows[limit-1].ID
		}
		for _, row := range rows {
			item, e := row.template()
			if e != nil {
				return e
			}
			items = append(items, item)
		}
		return nil
	})
	return
}

// Create 生成服务端ID；同名模板允许独立存在。
func (s *Store) Create(ctx context.Context, actor identity.Principal, t usage.Template) (usage.Template, error) {
	t.ID, t.Version = uuid.NewString(), 0
	return s.save(ctx, actor, t)
}

// Update 要求ID和预期版本匹配，即使配置相同也递增版本。
func (s *Store) Update(ctx context.Context, actor identity.Principal, t usage.Template) (usage.Template, error) {
	if t.ID == "" || t.Version < 1 || t.Version == math.MaxInt64 {
		return usage.Template{}, identity.ErrInvalid
	}
	return s.save(ctx, actor, t)
}

func (s *Store) save(ctx context.Context, actor identity.Principal, t usage.Template) (usage.Template, error) {
	t, e := usage.Normalize(t)
	if e != nil {
		return usage.Template{}, identity.ErrInvalid
	}
	row := templateRow{ID: t.ID, Name: t.Name, Description: t.Description, ConfigJSON: encode(t.Config), Version: t.Version + 1}
	e = within(ctx, s.write, actor, rbac.UsageManage, func(db *gorm.DB) error {
		if t.Version == 0 {
			return db.Create(&row).Error
		}
		if e := matchVersion(db, t.ID, t.Version); e != nil {
			return e
		}
		return db.Model(&templateRow{}).Where("id = ? AND version = ?", t.ID, t.Version).Updates(map[string]any{
			"name": row.Name, "description": row.Description, "config_json": row.ConfigJSON, "version": row.Version,
		}).Error
	})
	t.Version = row.Version
	return t, e
}

// Delete 按预期版本硬删除；没有员工副本或级联操作。
func (s *Store) Delete(ctx context.Context, actor identity.Principal, id string, version int64) error {
	return within(ctx, s.write, actor, rbac.UsageManage, func(db *gorm.DB) error {
		if e := matchVersion(db, id, version); e != nil {
			return e
		}
		return db.Where("id = ? AND version = ?", id, version).Delete(&templateRow{}).Error
	})
}

func matchVersion(db *gorm.DB, id string, version int64) error {
	var row templateRow
	if e := db.Select("version").Where("id = ?", id).First(&row).Error; e != nil {
		return e
	}
	if row.Version != version {
		return identity.ErrConflict
	}
	return nil
}
