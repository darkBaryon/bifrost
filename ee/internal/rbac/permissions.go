// 本文件是固定权限目录及预置角色的唯一来源。
package rbac

// Permission 是可持久化的固定权限编码。
type Permission string

const (
	ModelProviderView      Permission = "ModelProvider.View"
	ModelProviderManage    Permission = "ModelProvider.Manage"
	ModelProviderRevealKey Permission = "ModelProvider.RevealKey"
	VirtualKeysView        Permission = "VirtualKeys.View"
	VirtualKeysManage      Permission = "VirtualKeys.Manage"
	VirtualKeysRevealKey   Permission = "VirtualKeys.RevealKey"
	GovernanceView         Permission = "Governance.View"
	GovernanceManage       Permission = "Governance.Manage"
	RoutingRulesView       Permission = "RoutingRules.View"
	RoutingRulesManage     Permission = "RoutingRules.Manage"
	LogsView               Permission = "Logs.View"
	LogsManage             Permission = "Logs.Manage"
	LogsRevealContent      Permission = "Logs.RevealContent"
	LogsExport             Permission = "Logs.Export"
	MCPGatewayView         Permission = "MCPGateway.View"
	MCPGatewayManage       Permission = "MCPGateway.Manage"
	PluginsView            Permission = "Plugins.View"
	PluginsManage          Permission = "Plugins.Manage"
	PluginsLoadNative      Permission = "Plugins.LoadNative"
	PromptRepositoryView   Permission = "PromptRepository.View"
	PromptRepositoryManage Permission = "PromptRepository.Manage"
	SettingsView           Permission = "Settings.View"
	SettingsManage         Permission = "Settings.Manage"
	SettingsAuthConfig     Permission = "Settings.AuthConfig"
	SettingsImportConfig   Permission = "Settings.ImportConfig"
	NotificationsView      Permission = "Notifications.View"
	NotificationsManage    Permission = "Notifications.Manage"
	UsersView              Permission = "Users.View"
	UsersManage            Permission = "Users.Manage"
)

// PermissionDefinition 描述权限显示名及该基线是否有合法操作入口。
type PermissionDefinition struct {
	Code      Permission
	Name      string
	Available bool
}

// ModuleCode 是权限目录的稳定产品分组编码。
type ModuleCode string

const (
	ModuleModelProvider    ModuleCode = "ModelProvider"
	ModuleVirtualKeys      ModuleCode = "VirtualKeys"
	ModuleGovernance       ModuleCode = "Governance"
	ModuleRoutingRules     ModuleCode = "RoutingRules"
	ModuleLogs             ModuleCode = "Logs"
	ModuleMCPGateway       ModuleCode = "MCPGateway"
	ModulePlugins          ModuleCode = "Plugins"
	ModulePromptRepository ModuleCode = "PromptRepository"
	ModuleSettings         ModuleCode = "Settings"
	ModuleNotifications    ModuleCode = "Notifications"
	ModuleUsers            ModuleCode = "Users"
)

// Module 是目录中的产品分组。
type Module struct {
	Code        ModuleCode
	Name        string
	Permissions []PermissionDefinition
}

// Catalogue 返回独立目录副本，调用方不能修改程序定义。
func Catalogue() []Module {
	return []Module{
		{Code: ModuleModelProvider, Name: "模型厂商", Permissions: []PermissionDefinition{
			{Code: ModelProviderView, Name: "查看", Available: true},
			{Code: ModelProviderManage, Name: "管理", Available: true},
			{Code: ModelProviderRevealKey, Name: "查看密钥", Available: false},
		}},
		{Code: ModuleVirtualKeys, Name: "虚拟密钥", Permissions: []PermissionDefinition{
			{Code: VirtualKeysView, Name: "查看", Available: true},
			{Code: VirtualKeysManage, Name: "管理", Available: true},
			{Code: VirtualKeysRevealKey, Name: "查看密钥", Available: true},
		}},
		{Code: ModuleGovernance, Name: "治理", Permissions: []PermissionDefinition{
			{Code: GovernanceView, Name: "查看", Available: true},
			{Code: GovernanceManage, Name: "管理", Available: true},
		}},
		{Code: ModuleRoutingRules, Name: "路由规则", Permissions: []PermissionDefinition{
			{Code: RoutingRulesView, Name: "查看", Available: true},
			{Code: RoutingRulesManage, Name: "管理", Available: true},
		}},
		{Code: ModuleLogs, Name: "日志", Permissions: []PermissionDefinition{
			{Code: LogsView, Name: "查看", Available: true},
			{Code: LogsManage, Name: "管理", Available: true},
			{Code: LogsRevealContent, Name: "查看正文", Available: true},
			{Code: LogsExport, Name: "导出", Available: true},
		}},
		{Code: ModuleMCPGateway, Name: "MCP网关", Permissions: []PermissionDefinition{
			{Code: MCPGatewayView, Name: "查看", Available: true},
			{Code: MCPGatewayManage, Name: "管理", Available: true},
		}},
		{Code: ModulePlugins, Name: "插件", Permissions: []PermissionDefinition{
			{Code: PluginsView, Name: "查看", Available: true},
			{Code: PluginsManage, Name: "管理", Available: true},
			{Code: PluginsLoadNative, Name: "加载原生插件", Available: true},
		}},
		{Code: ModulePromptRepository, Name: "提示词库", Permissions: []PermissionDefinition{
			{Code: PromptRepositoryView, Name: "查看", Available: true},
			{Code: PromptRepositoryManage, Name: "管理", Available: true},
		}},
		{Code: ModuleSettings, Name: "设置", Permissions: []PermissionDefinition{
			{Code: SettingsView, Name: "查看", Available: true},
			{Code: SettingsManage, Name: "管理", Available: true},
			{Code: SettingsAuthConfig, Name: "认证配置", Available: false},
			{Code: SettingsImportConfig, Name: "导入配置", Available: false},
		}},
		{Code: ModuleNotifications, Name: "通知", Permissions: []PermissionDefinition{
			{Code: NotificationsView, Name: "查看", Available: true},
			{Code: NotificationsManage, Name: "管理", Available: true},
		}},
		{Code: ModuleUsers, Name: "账号与角色", Permissions: []PermissionDefinition{
			{Code: UsersView, Name: "查看", Available: true},
			{Code: UsersManage, Name: "管理", Available: true},
		}},
	}
}

// Permissions 按固定目录次序列出全部权限，包括已预留但未开放的权限。
func Permissions() []Permission {
	out := []Permission{}
	for _, m := range Catalogue() {
		for _, p := range m.Permissions {
			out = append(out, p.Code)
		}
	}
	return out
}

// KnownPermission 判断是否属于本版目录；未知码不允许写入角色。
func KnownPermission(p Permission) bool {
	for _, v := range Permissions() {
		if p == v {
			return true
		}
	}
	return false
}

// PresetRoles 返回首次迁移用的存储形态（chief 不含逐项权限），重启不覆盖已编辑角色。
func PresetRoles() []Role {
	chief := Role{ID: ChiefRoleID, Name: "主管理员", SystemCode: SystemChief, PermissionCodes: []Permission{}}
	dev := Role{ID: DeveloperRoleID, Name: "开发者", SystemCode: SystemDeveloper, PermissionCodes: []Permission{ModelProviderView, ModelProviderManage, VirtualKeysView, VirtualKeysManage, GovernanceView, GovernanceManage, RoutingRulesView, RoutingRulesManage, LogsView, LogsManage, MCPGatewayView, MCPGatewayManage, PluginsView, PluginsManage, PromptRepositoryView, PromptRepositoryManage, SettingsView, SettingsManage, NotificationsView, NotificationsManage}}
	read := Role{ID: ReadonlyRoleID, Name: "只读", SystemCode: SystemReadonly, PermissionCodes: []Permission{ModelProviderView, VirtualKeysView, GovernanceView, RoutingRulesView, LogsView, MCPGatewayView, PluginsView, PromptRepositoryView, SettingsView, NotificationsView, UsersView}}
	return []Role{chief, dev, read}
}

// rolePermissions 在读取时展开chief全权；写入时由storedPermissions保持无逐项关系。
func rolePermissions(role Role) []Permission {
	if role.SystemCode == SystemChief {
		return Permissions()
	}
	return append([]Permission{}, role.PermissionCodes...)
}

func presentedRole(role Role) Role {
	role.PermissionCodes = rolePermissions(role)
	return role
}

// storedPermissions 遵循首版存储合同：chief 不存逐项关系，读时动态展开目录。
func storedPermissions(role Role, codes []Permission) []Permission {
	if role.SystemCode == SystemChief {
		return []Permission{}
	}
	return append([]Permission{}, codes...)
}
