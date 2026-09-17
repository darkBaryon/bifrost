// 本文件执行角色及关系读写；调用者已经持有身份事务或一致快照。
package persistence

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"gorm.io/gorm/clause"
)

func (s *BoundStore) enrich(row roleRow) (rbac.Role, error) {
	out := row.role()
	var count int64
	if e := s.db.Model(&accountRoleRow{}).Where("role_id = ?", row.ID).Count(&count).Error; e != nil {
		return out, e
	}
	out.AccountCount = int(count)
	var rows []permissionRow
	if e := s.db.Where("role_id = ?", row.ID).Find(&rows).Error; e != nil {
		return out, e
	}
	found := map[rbac.Permission]bool{}
	for _, r := range rows {
		found[rbac.Permission(r.PermissionCode)] = true
	}
	for _, code := range rbac.Permissions() {
		if found[code] {
			out.PermissionCodes = append(out.PermissionCodes, code)
		}
	}
	return out, nil
}

func (s *BoundStore) Role(id rbac.RoleID) (rbac.Role, error) {
	var row roleRow
	if e := s.db.First(&row, uint64(id)).Error; e != nil {
		return rbac.Role{}, translate(e)
	}
	return s.enrich(row)
}

func (s *BoundStore) roles(rows []roleRow) ([]rbac.Role, error) {
	out := make([]rbac.Role, 0, len(rows))
	for _, row := range rows {
		r, e := s.enrich(row)
		if e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *BoundStore) Roles(before rbac.RoleID, limit int) ([]rbac.Role, error) {
	rows := []roleRow{}
	q := s.db.Order("id DESC").Limit(limit)
	if before != 0 {
		q = q.Where("id < ?", uint64(before))
	}
	if e := q.Find(&rows).Error; e != nil {
		return nil, e
	}
	return s.roles(rows)
}

func (s *BoundStore) AccountRoles(id string) ([]rbac.Role, error) {
	rows := []roleRow{}
	if e := s.db.Where("id IN (?)", s.db.Model(&accountRoleRow{}).Select("role_id").Where("account_id = ?", id)).Order("id ASC").Find(&rows).Error; e != nil {
		return nil, e
	}
	return s.roles(rows)
}

func (s *BoundStore) RoleAccountIDs(id rbac.RoleID) ([]string, error) {
	out := []string{}
	e := s.db.Model(&accountRoleRow{}).Where("role_id = ?", uint64(id)).Order("account_id ASC").Pluck("account_id", &out).Error
	return out, e
}

func (s *BoundStore) permissions(id rbac.RoleID, codes []rbac.Permission) error {
	if e := s.db.Where("role_id = ?", uint64(id)).Delete(&permissionRow{}).Error; e != nil {
		return e
	}
	for _, code := range codes {
		if e := s.db.Create(&permissionRow{RoleID: uint64(id), PermissionCode: string(code)}).Error; e != nil {
			return translate(e)
		}
	}
	return nil
}

func (s *BoundStore) InsertRole(input rbac.RoleInput) (rbac.Role, error) {
	at := time.Now().UTC()
	row := roleRow{Name: input.Name, Description: input.Description, CreatedAt: at, UpdatedAt: at}
	if e := s.db.Create(&row).Error; e != nil {
		return rbac.Role{}, translate(e)
	}
	if row.ID > uint64(rbac.MaxRoleID) {
		return rbac.Role{}, rbac.ErrUnavailable
	}
	if e := s.permissions(rbac.RoleID(row.ID), input.PermissionCodes); e != nil {
		return rbac.Role{}, e
	}
	return s.Role(rbac.RoleID(row.ID))
}

func (s *BoundStore) UpdateRole(id rbac.RoleID, input rbac.RoleInput) (rbac.Role, error) {
	if e := s.db.Model(&roleRow{}).Where("id = ?", uint64(id)).Updates(map[string]any{"name": input.Name, "description": input.Description, "updated_at": time.Now().UTC()}).Error; e != nil {
		return rbac.Role{}, translate(e)
	}
	if e := s.permissions(id, input.PermissionCodes); e != nil {
		return rbac.Role{}, e
	}
	return s.Role(id)
}

func (s *BoundStore) DeleteRole(id rbac.RoleID) error {
	return translate(s.db.Delete(&roleRow{}, uint64(id)).Error)
}

func (s *BoundStore) ReplaceAccountRoles(id string, ids []rbac.RoleID) error {
	if e := s.db.Where("account_id = ?", id).Delete(&accountRoleRow{}).Error; e != nil {
		return e
	}
	for _, role := range ids {
		if e := s.AddAccountRole(id, role); e != nil {
			return e
		}
	}
	return nil
}

func (s *BoundStore) AddAccountRole(id string, role rbac.RoleID) error {
	return s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&accountRoleRow{AccountID: id, RoleID: uint64(role)}).Error
}
