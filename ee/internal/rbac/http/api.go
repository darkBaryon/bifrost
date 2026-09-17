// 本文件处理九个角色权限接口，按角色管理、账号角色分配和权限查询分组；各组的输入和返回结构就近放置。
package rbachttp

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/valyala/fasthttp"
)

// —— 角色管理 ——

// listRoles 读取分页参数并查询角色列表；是否有查看权限，由业务服务检查。
func (h *Handler) listRoles(r request) (int, any, error) {
	fields, err := decode(r.http, []string{"cursor", "limit"}, nil)
	if err != nil {
		return 0, nil, err
	}
	cursor, err := textField(fields, "cursor")
	if err != nil {
		return 0, nil, err
	}
	limit := rbac.DefaultPageSize
	if v, ok := fields["limit"]; ok {
		if json.Unmarshal(v, &limit) != nil {
			return 0, nil, rbac.ErrInvalid
		}
	}
	page, err := h.service.ListRoles(r.ctx, r.subject, cursor, limit)
	if err != nil {
		return 0, nil, err
	}
	items := []roleDTO{}
	for _, role := range page.Items {
		items = append(items, roleResponse(role))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	return fasthttp.StatusOK, map[string]any{"items": items, "next_cursor": next}, nil
}

// decodeRoleID 用于详情和删除接口：请求必须只包含role_id，并且是合法的角色编号。
func decodeRoleID(r request) (rbac.RoleID, error) {
	fields, err := decode(r.http, []string{"role_id"}, []string{"role_id"})
	if err != nil {
		return 0, err
	}
	return roleID(fields["role_id"])
}

// getRole 按编号查询一个角色，返回它的名称、权限和使用人数等信息。
func (h *Handler) getRole(r request) (int, any, error) {
	id, err := decodeRoleID(r)
	if err != nil {
		return 0, nil, err
	}
	role, err := h.service.GetRole(r.ctx, r.subject, id)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, map[string]any{"role": roleResponse(role)}, nil
}

// deleteRole 请求删除角色；业务服务会检查管理权限、预置角色保护和账号关联。
func (h *Handler) deleteRole(r request) (int, any, error) {
	id, err := decodeRoleID(r)
	if err != nil {
		return 0, nil, err
	}
	err = h.service.DeleteRole(r.ctx, r.subject, id)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, map[string]string{"message": "Role deleted"}, nil
}

// createRole 读取角色名称、可选说明和权限列表，交给业务服务创建角色。
func (h *Handler) createRole(r request) (int, any, error) {
	fields, err := decode(r.http, []string{"name", "description", "permission_codes"}, []string{"name", "permission_codes"})
	if err != nil {
		return 0, nil, err
	}
	in, err := roleInput(fields)
	if err != nil {
		return 0, nil, err
	}
	role, err := h.service.CreateRole(r.ctx, r.subject, in)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusCreated, map[string]any{"role": roleResponse(role)}, nil
}

// updateRole 用提交的名称、说明和权限列表替换角色内容，这些字段都必须传。
func (h *Handler) updateRole(r request) (int, any, error) {
	keys := []string{"role_id", "name", "description", "permission_codes"}
	fields, err := decode(r.http, keys, keys)
	if err != nil {
		return 0, nil, err
	}
	id, err := roleID(fields["role_id"])
	if err != nil {
		return 0, nil, err
	}
	in, err := roleInput(fields)
	if err != nil {
		return 0, nil, err
	}
	role, err := h.service.UpdateRole(r.ctx, r.subject, id, in)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, map[string]any{"role": roleResponse(role)}, nil
}

// roleDTO 是角色接口返回的字段；与根包的Role分开，避免业务结构变化时意外改变JSON。
type roleDTO struct {
	ID              rbac.RoleID       `json:"id"`
	Name            string            `json:"name"`
	Description     string            `json:"description"`
	SystemCode      *rbac.SystemCode  `json:"system_code"`
	PermissionCodes []rbac.Permission `json:"permission_codes"`
	AccountCount    int               `json:"account_count"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// systemCode 让自建角色的system_code返回null；预置角色返回chief、developer或readonly。
func systemCode(c rbac.SystemCode) *rbac.SystemCode {
	if c == "" {
		return nil
	}
	return &c
}

// roleResponse 把业务角色转成接口结果；没有权限时返回空数组，时间统一为UTC。
func roleResponse(r rbac.Role) roleDTO {
	return roleDTO{
		ID:              r.ID,
		Name:            r.Name,
		Description:     r.Description,
		SystemCode:      systemCode(r.SystemCode),
		PermissionCodes: append([]rbac.Permission{}, r.PermissionCodes...),
		AccountCount:    r.AccountCount,
		CreatedAt:       r.CreatedAt.UTC(),
		UpdatedAt:       r.UpdatedAt.UTC(),
	}
}

// roleInput 将名称、说明和权限列表整理成业务参数；名称长度、权限是否存在等规则由业务服务检查。
func roleInput(fields map[string]json.RawMessage) (rbac.RoleInput, error) {
	var in rbac.RoleInput
	var err error
	in.Name, err = textField(fields, "name")
	if err != nil {
		return in, err
	}
	in.Description, err = textField(fields, "description")
	if err != nil {
		return in, err
	}
	// 空数组表示不给角色配置任何权限；null或含非字符串元素的数组不接受。
	var raw []json.RawMessage
	if json.Unmarshal(fields["permission_codes"], &raw) != nil || raw == nil {
		return in, rbac.ErrInvalid
	}
	in.PermissionCodes = []rbac.Permission{}
	for _, v := range raw {
		var code string
		if bytes.Equal(v, []byte("null")) || json.Unmarshal(v, &code) != nil {
			return in, rbac.ErrInvalid
		}
		in.PermissionCodes = append(in.PermissionCodes, rbac.Permission(code))
	}
	return in, nil
}

// —— 账号角色分配 ——

// getAccountRoles 查看指定账号有哪些角色、哪些权限，以及每个权限来自哪个角色。
func (h *Handler) getAccountRoles(r request) (int, any, error) {
	fields, err := decode(r.http, []string{"account_id"}, []string{"account_id"})
	if err != nil {
		return 0, nil, err
	}
	id, err := accountID(fields)
	if err != nil {
		return 0, nil, err
	}
	eff, err := h.service.GetAccountRoles(r.ctx, r.subject, id)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, effectiveResponse(eff), nil
}

// setAccountRoles 用role_ids替换账号的全部角色；传空数组就是移除所有角色。
// 能否分配、是否修改自己、是否会移除最后一个主管理员，都由业务服务检查。
func (h *Handler) setAccountRoles(r request) (int, any, error) {
	keys := []string{"account_id", "role_ids"}
	fields, err := decode(r.http, keys, keys)
	if err != nil {
		return 0, nil, err
	}
	id, err := accountID(fields)
	if err != nil {
		return 0, nil, err
	}
	var raw []json.RawMessage
	if json.Unmarshal(fields["role_ids"], &raw) != nil || raw == nil {
		return 0, nil, rbac.ErrInvalid
	}
	ids := []rbac.RoleID{}
	for _, v := range raw {
		rid, err := roleID(v)
		if err != nil {
			return 0, nil, err
		}
		ids = append(ids, rid)
	}
	eff, err := h.service.SetAccountRoles(r.ctx, r.subject, id, ids)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, effectiveResponse(eff), nil
}

// roleSummary 是账号角色列表里的一项，只返回角色编号、名称和预置角色标识。
type roleSummary struct {
	ID         rbac.RoleID      `json:"id"`
	Name       string           `json:"name"`
	SystemCode *rbac.SystemCode `json:"system_code"`
}

// grantDTO 说明一个权限来自哪些角色；例如两个角色都允许查看日志，就返回这两个角色的编号。
type grantDTO struct {
	Code    rbac.Permission `json:"code"`
	RoleIDs []rbac.RoleID   `json:"role_ids"`
}

// effectiveDTO 返回账号的角色、权限来源，以及前端用于显示功能入口和按钮的权限表。
type effectiveDTO struct {
	AccountID   string                                  `json:"account_id"`
	Roles       []roleSummary                           `json:"roles"`
	Permissions []grantDTO                              `json:"permissions"`
	Matrix      map[resourceCode]map[operationCode]bool `json:"matrix"`
}

// effectiveResponse 整理账号的权限结果；没有角色或权限时返回空数组，不返回null。
func effectiveResponse(effective rbac.Effective) effectiveDTO {
	out := effectiveDTO{
		AccountID:   effective.AccountID,
		Roles:       []roleSummary{},
		Permissions: []grantDTO{},
		Matrix:      matrix(effective.Permissions),
	}
	for _, r := range effective.Roles {
		out.Roles = append(out.Roles, roleSummary{
			ID:         r.ID,
			Name:       r.Name,
			SystemCode: systemCode(r.SystemCode),
		})
	}
	for _, p := range effective.Permissions {
		out.Permissions = append(out.Permissions, grantDTO{
			Code:    p.Code,
			RoleIDs: append([]rbac.RoleID{}, p.RoleIDs...),
		})
	}
	return out
}

// —— 权限查询 ——

// myPermissions 让账号查看自己的角色和权限，无需Users.View，但必须正常登录且已完成强制改密。
func (h *Handler) myPermissions(r request) (int, any, error) {
	if _, err := decode(r.http, nil, nil); err != nil {
		return 0, nil, err
	}
	eff, err := h.service.Me(r.ctx, r.subject)
	if err != nil {
		return 0, nil, err
	}
	return fasthttp.StatusOK, effectiveResponse(eff), nil
}

// listPermissions 返回所有可配置的权限，按功能模块分组；需要Users.View。
func (h *Handler) listPermissions(r request) (int, any, error) {
	if _, err := decode(r.http, nil, nil); err != nil {
		return 0, nil, err
	}
	if err := h.service.Authorize(r.ctx, r.subject, rbac.UsersView); err != nil {
		return 0, nil, err
	}
	modules := []moduleDTO{}
	for _, m := range rbac.Catalogue() {
		out := moduleDTO{Code: m.Code, Name: m.Name, Permissions: []permissionDTO{}}
		for _, p := range m.Permissions {
			out.Permissions = append(out.Permissions, permissionDTO{
				Code:      p.Code,
				Name:      p.Name,
				Available: p.Available,
			})
		}
		modules = append(modules, out)
	}
	return fasthttp.StatusOK, map[string]any{"modules": modules}, nil
}

// permissionDTO 告诉前端一个权限的标识、显示名称，以及有没有对应的操作入口。
// Available不表示当前账号有权限，也不表示入口已经接上权限检查。
type permissionDTO struct {
	Code      rbac.Permission `json:"code"`
	Name      string          `json:"name"`
	Available bool            `json:"available"`
}

// moduleDTO 将同一模块的权限放在一起，例如“日志”下面有查看、管理和导出。
type moduleDTO struct {
	Code        rbac.ModuleCode `json:"code"`
	Name        string          `json:"name"`
	Permissions []permissionDTO `json:"permissions"`
}
