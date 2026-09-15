// 本文件定义三张权限表，不对身份表设置跨模块写入约束。
package persistence

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
)

type roleRow struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement"`
	Name        string    `gorm:"type:varchar(64);not null;uniqueIndex:ee_rbac_role_name"`
	Description string    `gorm:"type:varchar(512);not null;default:''"`
	SystemCode  *string   `gorm:"type:varchar(16);uniqueIndex:ee_rbac_system_code"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (roleRow) TableName() string { return "ee_rbac_roles" }

type permissionRow struct {
	RoleID         uint64  `gorm:"primaryKey;autoIncrement:false"`
	PermissionCode string  `gorm:"type:varchar(64);primaryKey"`
	Role           roleRow `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
}

func (permissionRow) TableName() string { return "ee_rbac_role_permissions" }

type accountRoleRow struct {
	AccountID string  `gorm:"type:varchar(36);primaryKey;index:ee_rbac_role_accounts,priority:2"`
	RoleID    uint64  `gorm:"primaryKey;autoIncrement:false;index:ee_rbac_role_accounts,priority:1"`
	Role      roleRow `gorm:"foreignKey:RoleID;constraint:OnDelete:RESTRICT"`
}

func (accountRoleRow) TableName() string { return "ee_rbac_account_roles" }

func (r roleRow) role() rbac.Role {
	out := rbac.Role{ID: rbac.RoleID(r.ID), Name: r.Name, Description: r.Description, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, PermissionCodes: []rbac.Permission{}}
	if r.SystemCode != nil {
		out.SystemCode = rbac.SystemCode(*r.SystemCode)
	}
	return out
}
