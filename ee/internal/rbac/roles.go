// 本文件实现角色CRUD、预置保护和游标分页。
package rbac

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

func input(in RoleInput) (RoleInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	if !utf8.ValidString(in.Name) || !utf8.ValidString(in.Description) || in.Name == "" ||
		utf8.RuneCountInString(in.Name) > MaxRoleNameRunes || utf8.RuneCountInString(in.Description) > MaxRoleDescriptionRunes {
		return in, ErrInvalid
	}
	for _, r := range in.Name {
		if unicode.IsControl(r) {
			return in, ErrInvalid
		}
	}
	set := map[Permission]bool{}
	for _, p := range in.PermissionCodes {
		if !KnownPermission(p) {
			return in, ErrInvalid
		}
		set[p] = true
	}
	in.PermissionCodes = []Permission{}
	for _, p := range Permissions() {
		if set[p] {
			in.PermissionCodes = append(in.PermissionCodes, p)
		}
	}
	return in, nil
}

func parseCursor(raw string) (RoleID, error) {
	if raw == "" {
		return 0, nil
	}
	if len(raw) > base64.RawURLEncoding.EncodedLen(MaxCursorBytes) {
		return 0, ErrInvalid
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil || len(b) > MaxCursorBytes {
		return 0, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	t, e := d.Token()
	if e != nil || t != json.Delim('{') {
		return 0, ErrInvalid
	}
	t, e = d.Token()
	if e != nil || t != "before_id" {
		return 0, ErrInvalid
	}
	token, err := d.Token()
	number, ok := token.(json.Number)
	if err != nil || !ok {
		return 0, ErrInvalid
	}
	n, e := strconv.ParseUint(string(number), 10, 64)
	if e != nil || n == 0 || RoleID(n) > MaxRoleID {
		return 0, ErrInvalid
	}
	t, e = d.Token()
	if e != nil || t != json.Delim('}') {
		return 0, ErrInvalid
	}
	if _, e = d.Token(); e != io.EOF {
		return 0, ErrInvalid
	}
	return RoleID(n), nil
}

func cursor(id RoleID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(`{"before_id":` + strconv.FormatUint(uint64(id), 10) + `}`))
}

// ListRoles 在单个快照中读取一页，新增角色不会插入已有游标之后。
func (s *Service) ListRoles(ctx context.Context, p Subject, after string, limit int) (Page, error) {
	out := Page{Items: []Role{}}
	e := s.read(ctx, func(v ReadView) error {
		if e := require(v, p, UsersView); e != nil {
			return e
		}
		if limit < 1 || limit > MaxPageSize {
			return ErrInvalid
		}
		before, e := parseCursor(after)
		if e != nil {
			return e
		}
		rows, e := v.Roles(before, limit+1)
		if e != nil {
			return e
		}
		if len(rows) > limit {
			rows = rows[:limit]
			out.NextCursor = cursor(rows[len(rows)-1].ID)
		}
		for i := range rows {
			rows[i] = presentedRole(rows[i])
		}
		out.Items = rows
		return nil
	})
	return out, e
}

// GetRole 授权先于存在性查询。
func (s *Service) GetRole(ctx context.Context, p Subject, id RoleID) (Role, error) {
	var out Role
	e := s.read(ctx, func(v ReadView) error {
		if e := require(v, p, UsersView); e != nil {
			return e
		}
		var e error
		out, e = v.Role(id)
		if e == nil {
			out = presentedRole(out)
		}
		return e
	})
	return out, e
}

// CreateRole 创建自定义角色；空权限集合法。
func (s *Service) CreateRole(ctx context.Context, p Subject, in RoleInput) (Role, error) {
	var out Role
	e := s.write(ctx, func(tx Tx) error {
		if e := require(tx, p, UsersManage); e != nil {
			return e
		}
		normalized, e := input(in)
		if e != nil {
			return e
		}
		out, e = tx.InsertRole(normalized)
		return e
	})
	return out, e
}

// UpdateRole 完整替换元信息/权限；主管理员只能改元信息，重复请求不更新时间。
func (s *Service) UpdateRole(ctx context.Context, p Subject, id RoleID, in RoleInput) (Role, error) {
	var out Role
	e := s.write(ctx, func(tx Tx) error {
		if e := require(tx, p, UsersManage); e != nil {
			return e
		}
		old, e := tx.Role(id)
		if e != nil {
			return e
		}
		old = presentedRole(old)
		normalized, e := input(in)
		if e != nil {
			return e
		}
		if old.SystemCode == SystemChief && !slices.Equal(normalized.PermissionCodes, rolePermissions(old)) {
			return ErrConflict
		}
		if old.Name == normalized.Name && old.Description == normalized.Description && slices.Equal(old.PermissionCodes, normalized.PermissionCodes) {
			out = old
			return nil
		}
		normalized.PermissionCodes = storedPermissions(old, normalized.PermissionCodes)
		out, e = tx.UpdateRole(id, normalized)
		if e == nil {
			out = presentedRole(out)
		}
		return e
	})
	return out, e
}

// DeleteRole 拒绝预置角色及任何仍被账号引用的角色，含停用账号。
func (s *Service) DeleteRole(ctx context.Context, p Subject, id RoleID) error {
	return s.write(ctx, func(tx Tx) error {
		if e := require(tx, p, UsersManage); e != nil {
			return e
		}
		role, e := tx.Role(id)
		if e != nil {
			return e
		}
		if role.SystemCode != "" {
			return ErrConflict
		}
		if role.AccountCount > 0 {
			return &ConflictError{AccountCount: role.AccountCount}
		}
		return tx.DeleteRole(id)
	})
}
