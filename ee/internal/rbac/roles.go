// 本文件定义角色和三个预置角色，并负责角色的新增、修改、删除和分页查询。
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
	"time"
	"unicode"
	"unicode/utf8"
)

// RoleID 是角色的数字编号；取值不能超过 MaxRoleID，避免浏览器读取时丢失精度。
type RoleID uint64

// SystemCode 标记系统预置角色，例如 chief 表示主管理员。修改角色名称不会改变这个标记。
type SystemCode string

// 初始化时，三个预置角色使用下面固定的标记和编号，之后不能修改。
// 判断是不是主管理员等预置角色时，看 SystemCode，不看角色名称。
const (
	SystemChief     SystemCode = "chief"
	SystemDeveloper SystemCode = "developer"
	SystemReadonly  SystemCode = "readonly"
	ChiefRoleID     RoleID     = 1
	DeveloperRoleID RoleID     = 2
	ReadonlyRoleID  RoleID     = 3
	// MaxRoleID 是 JavaScript 能精确表示的最大整数，保证角色编号传到浏览器后不变。
	MaxRoleID RoleID = 1<<53 - 1
	// 以下限制沿用角色管理接口约定。
	MaxRoleNameRunes        = 64  // 角色名称最多64个字符，中文也按字符计数。
	MaxRoleDescriptionRunes = 512 // 角色说明最多512个字符。
	MaxRolesPerAccount      = 64  // 一次分配最多提交64个角色编号，去重前计数。
	DefaultPageSize         = 20  // 列表接口默认每页20个角色。
	MaxPageSize             = 100 // 每页最多100个角色。
	MaxCursorBytes          = 128 // 翻页标记解码后最多128字节。
)

// Role 描述一个角色：叫什么、有哪些权限、多少账号正在使用它。
type Role struct {
	ID                   RoleID
	Name, Description    string
	SystemCode           SystemCode
	PermissionCodes      []Permission
	AccountCount         int // 使用该角色的账号数，包含已停用的账号。
	CreatedAt, UpdatedAt time.Time
}

// RoleInput 是创建或修改角色时提交的内容；修改时替换全部名称、说明和权限。
type RoleInput struct {
	Name, Description string
	PermissionCodes   []Permission
}

// Page 是一页角色，按编号从大到小排列。NextCursor 是取下一页的标记，为空表示没有下一页。
// 每一页都重新读取数据库，不保留第一页查询时的数据状态。
type Page struct {
	Items      []Role
	NextCursor string
}

// PresetRoles 提供首次创建的三个预置角色。调用方不能在每次启动时用这些默认值覆盖用户已修改的角色。
// 主管理员不保存逐项权限，读取时自动拥有整个目录的权限。
func PresetRoles() []Role {
	return []Role{
		{
			ID:              ChiefRoleID,
			Name:            "主管理员",
			SystemCode:      SystemChief,
			PermissionCodes: []Permission{},
		},
		{
			ID:         DeveloperRoleID,
			Name:       "开发者",
			SystemCode: SystemDeveloper,
			PermissionCodes: []Permission{
				ModelProviderView, ModelProviderManage, // 模型厂商
				VirtualKeysView, VirtualKeysManage, // 虚拟密钥
				GovernanceView, GovernanceManage, // 治理
				RoutingRulesView, RoutingRulesManage, // 路由规则
				LogsView, LogsManage, // 日志与费用
				MCPGatewayView, MCPGatewayManage, // MCP工具
				PluginsView, PluginsManage, // 插件
				PromptRepositoryView, PromptRepositoryManage, // 提示词库
				SettingsView, SettingsManage, // 系统设置
				NotificationsView, NotificationsManage, // 通知
			},
		},
		{
			ID:         ReadonlyRoleID,
			Name:       "只读",
			SystemCode: SystemReadonly,
			PermissionCodes: []Permission{
				ModelProviderView,    // 模型厂商
				VirtualKeysView,      // 虚拟密钥
				GovernanceView,       // 治理
				RoutingRulesView,     // 路由规则
				LogsView,             // 日志与费用
				MCPGatewayView,       // MCP工具
				PluginsView,          // 插件
				PromptRepositoryView, // 提示词库
				SettingsView,         // 系统设置
				NotificationsView,    // 通知
				UsersView,            // 账号与角色
			},
		},
	}
}

// rolePermissions 返回角色拥有的权限；主管理员返回全部目录权限，其他角色返回各自配置的权限。
func rolePermissions(role Role) []Permission {
	if role.SystemCode == SystemChief {
		return Permissions()
	}
	return append([]Permission{}, role.PermissionCodes...)
}

// presentedRole 把数据库中的角色整理成对外返回的结果，补齐主管理员的全部权限。
func presentedRole(role Role) Role {
	role.PermissionCodes = rolePermissions(role)
	return role
}

// storedPermissions 决定写入数据库的权限列表；主管理员写空列表，因为它的全权由读取时计算。
func storedPermissions(role Role, codes []Permission) []Permission {
	if role.SystemCode == SystemChief {
		return []Permission{}
	}
	return append([]Permission{}, codes...)
}

// input 检查角色名称、说明和权限是否合法，并去掉名称两端的空白和重复的权限。
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

// parseCursor 从翻页标记中取出上一页最后一个角色的编号；空标记表示从第一页开始。
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

// ListRoles 按角色编号从大到小取一页；带着上次的翻页标记继续查询时，新建角色不会挤进后续页。
func (s *Service) ListRoles(ctx context.Context, p Subject, after string, limit int) (Page, error) {
	out := Page{Items: []Role{}}
	e := s.read(ctx, func(queries Queries) error {
		if e := checkPermission(queries, p, UsersView); e != nil {
			return e
		}
		if limit < 1 || limit > MaxPageSize {
			return ErrInvalid
		}
		before, e := parseCursor(after)
		if e != nil {
			return e
		}
		rows, e := queries.Roles(before, limit+1)
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

// GetRole 先检查操作者有没有查看权限，再查角色；无权的人不能借此探测角色是否存在。
func (s *Service) GetRole(ctx context.Context, p Subject, id RoleID) (Role, error) {
	var out Role
	e := s.read(ctx, func(queries Queries) error {
		if e := checkPermission(queries, p, UsersView); e != nil {
			return e
		}
		var e error
		out, e = queries.Role(id)
		if e == nil {
			out = presentedRole(out)
		}
		return e
	})
	return out, e
}

// CreateRole 创建一个角色，允许暂时不给它配置任何权限。
func (s *Service) CreateRole(ctx context.Context, p Subject, in RoleInput) (Role, error) {
	var out Role
	e := s.write(ctx, func(tx Tx) error {
		if e := checkPermission(tx, p, UsersManage); e != nil {
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

// UpdateRole 替换角色的名称、说明和全部权限。主管理员角色只能改名称和说明，不能改权限。
// 提交内容与当前内容相同时不写入，也不改变更新时间。
func (s *Service) UpdateRole(ctx context.Context, p Subject, id RoleID, in RoleInput) (Role, error) {
	var out Role
	e := s.write(ctx, func(tx Tx) error {
		if e := checkPermission(tx, p, UsersManage); e != nil {
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

// DeleteRole 删除角色。预置角色不能删；只要还有账号使用该角色，即使账号已停用，也不能删。
func (s *Service) DeleteRole(ctx context.Context, p Subject, id RoleID) error {
	return s.write(ctx, func(tx Tx) error {
		if e := checkPermission(tx, p, UsersManage); e != nil {
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
