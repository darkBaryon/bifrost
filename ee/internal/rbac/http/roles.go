// 本文件把角色CRUD输入转换为业务命令，业务服务在自己的快照或事务内判权。
package rbachttp

import (
	"encoding/json"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/valyala/fasthttp"
)

func (h *Handler) list(r request) (int, any, error) {
	fields, e := decode(r.c, []string{"cursor", "limit"}, nil)
	if e != nil {
		return 0, nil, e
	}
	cursor, e := textField(fields, "cursor")
	if e != nil {
		return 0, nil, e
	}
	limit := rbac.DefaultPageSize
	if v, ok := fields["limit"]; ok {
		if json.Unmarshal(v, &limit) != nil {
			return 0, nil, rbac.ErrInvalid
		}
	}
	page, e := h.service.ListRoles(r.ctx, r.p, cursor, limit)
	if e != nil {
		return 0, nil, e
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

func getID(r request) (rbac.RoleID, error) {
	fields, e := decode(r.c, []string{"role_id"}, []string{"role_id"})
	if e != nil {
		return 0, e
	}
	return roleID(fields["role_id"])
}

func (h *Handler) get(r request) (int, any, error) {
	id, e := getID(r)
	if e != nil {
		return 0, nil, e
	}
	role, e := h.service.GetRole(r.ctx, r.p, id)
	if e != nil {
		return 0, nil, e
	}
	return fasthttp.StatusOK, map[string]any{"role": roleResponse(role)}, nil
}

func (h *Handler) delete(r request) (int, any, error) {
	id, e := getID(r)
	if e != nil {
		return 0, nil, e
	}
	e = h.service.DeleteRole(r.ctx, r.p, id)
	if e != nil {
		return 0, nil, e
	}
	return fasthttp.StatusOK, map[string]string{"message": "Role deleted"}, nil
}

func (h *Handler) create(r request) (int, any, error) {
	fields, e := decode(r.c, []string{"name", "description", "permission_codes"}, []string{"name", "permission_codes"})
	if e != nil {
		return 0, nil, e
	}
	in, e := roleInput(fields)
	if e != nil {
		return 0, nil, e
	}
	role, e := h.service.CreateRole(r.ctx, r.p, in)
	if e != nil {
		return 0, nil, e
	}
	return fasthttp.StatusCreated, map[string]any{"role": roleResponse(role)}, nil
}

func (h *Handler) update(r request) (int, any, error) {
	keys := []string{"role_id", "name", "description", "permission_codes"}
	fields, e := decode(r.c, keys, keys)
	if e != nil {
		return 0, nil, e
	}
	id, e := roleID(fields["role_id"])
	if e != nil {
		return 0, nil, e
	}
	in, e := roleInput(fields)
	if e != nil {
		return 0, nil, e
	}
	role, e := h.service.UpdateRole(r.ctx, r.p, id, in)
	if e != nil {
		return 0, nil, e
	}
	return fasthttp.StatusOK, map[string]any{"role": roleResponse(role)}, nil
}

func (h *Handler) getRoles(r request) (int, any, error) {
	fields, e := decode(r.c, []string{"account_id"}, []string{"account_id"})
	if e != nil {
		return 0, nil, e
	}
	id, e := accountID(fields)
	if e != nil {
		return 0, nil, e
	}
	eff, e := h.service.GetAccountRoles(r.ctx, r.p, id)
	if e != nil {
		return 0, nil, e
	}
	return fasthttp.StatusOK, effectiveResponse(eff), nil
}

func (h *Handler) setRoles(r request) (int, any, error) {
	keys := []string{"account_id", "role_ids"}
	fields, e := decode(r.c, keys, keys)
	if e != nil {
		return 0, nil, e
	}
	id, e := accountID(fields)
	if e != nil {
		return 0, nil, e
	}
	var raw []json.RawMessage
	if json.Unmarshal(fields["role_ids"], &raw) != nil || raw == nil {
		return 0, nil, rbac.ErrInvalid
	}
	ids := []rbac.RoleID{}
	for _, v := range raw {
		rid, e := roleID(v)
		if e != nil {
			return 0, nil, e
		}
		ids = append(ids, rid)
	}
	eff, e := h.service.SetAccountRoles(r.ctx, r.p, id, ids)
	if e != nil {
		return 0, nil, e
	}
	return fasthttp.StatusOK, effectiveResponse(eff), nil
}

func (h *Handler) me(r request) (int, any, error) {
	if _, e := decode(r.c, nil, nil); e != nil {
		return 0, nil, e
	}
	eff, e := h.service.Me(r.ctx, r.p)
	if e != nil {
		return 0, nil, e
	}
	return fasthttp.StatusOK, effectiveResponse(eff), nil
}

func (h *Handler) permissions(r request) (int, any, error) {
	if _, e := decode(r.c, nil, nil); e != nil {
		return 0, nil, e
	}
	if e := h.service.Authorize(r.ctx, r.p, rbac.UsersView); e != nil {
		return 0, nil, e
	}
	modules := []moduleDTO{}
	for _, m := range rbac.Catalogue() {
		out := moduleDTO{Code: m.Code, Name: m.Name, Permissions: []permissionDTO{}}
		for _, p := range m.Permissions {
			out.Permissions = append(out.Permissions, permissionDTO{p.Code, p.Name, p.Available})
		}
		modules = append(modules, out)
	}
	return fasthttp.StatusOK, map[string]any{"modules": modules}, nil
}
