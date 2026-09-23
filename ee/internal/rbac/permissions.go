// 本文件统一定义有哪些模块、每个模块有哪些权限，并提供权限查询。
package rbac

// ModuleCode 是模块的英文标识，例如 ModelProvider 表示“模型厂商”。
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
	ModuleSecurity         ModuleCode = "Security"
	ModuleUsage            ModuleCode = "Usage"
)

// Module 表示一个功能模块，包含模块名称和它下面的权限项。
type Module struct {
	Code        ModuleCode
	Name        string
	Permissions []PermissionDefinition
}

// Permission 是具体操作的权限标识，例如 ModelProvider.View 表示“查看模型厂商”。
type Permission string

// PermissionDefinition 描述一个权限叫什么，以及是否有对应的操作入口。
type PermissionDefinition struct {
	Code      Permission
	Name      string
	Available bool // 是否有对应的操作入口；不表示当前账号有权限，也不表示接口已接入权限检查。
}

// Catalogue 列出所有模块及其权限项。每次返回新的一份，修改返回值不会影响后续调用。
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
		{Code: ModuleSecurity, Name: "安全", Permissions: []PermissionDefinition{
			{Code: SecurityChangeCredentialDestination, Name: "允许将已有凭据用于新地址", Available: true},
		}},
		{Code: ModuleUsage, Name: "个人额度", Permissions: []PermissionDefinition{
			{Code: UsageView, Name: "查看", Available: true},
			{Code: UsageManage, Name: "管理", Available: true},
		}},
		{Code: ModuleUsers, Name: "账号与角色", Permissions: []PermissionDefinition{
			{Code: UsersView, Name: "查看", Available: true},
			{Code: UsersManage, Name: "管理", Available: true},
		}},
	}
}

// 下面按模块列出权限标识和用途；哪些接口使用这些权限，由各模块接入时决定。
// 查看、管理和敏感操作分别授权；有管理权限，不代表能查看或执行敏感操作。
const (
	// 个人额度：查看和管理分别授权，当前只提供模板管理。
	UsageView   Permission = "Usage.View"
	UsageManage Permission = "Usage.Manage"

	// 模型与厂商：网关连接的模型服务商、模型及厂商密钥。
	ModelProviderView      Permission = "ModelProvider.View"      // 查看厂商、模型和密钥列表，不含完整密钥。
	ModelProviderManage    Permission = "ModelProvider.Manage"    // 新增、修改和删除厂商、模型及密钥配置。
	ModelProviderRevealKey Permission = "ModelProvider.RevealKey" // 预留敏感权限：查看厂商密钥完整值，当前无开放入口。

	// 虚拟密钥：调用方访问网关时使用的凭据，与厂商密钥不同。
	VirtualKeysView      Permission = "VirtualKeys.View"      // 查看虚拟密钥列表与详情，不含完整密钥。
	VirtualKeysManage    Permission = "VirtualKeys.Manage"    // 新增、修改、删除和轮换虚拟密钥。
	VirtualKeysRevealKey Permission = "VirtualKeys.RevealKey" // 敏感权限：查看虚拟密钥完整值。

	// 治理：团队、客户、预算和请求限流的管理。
	GovernanceView   Permission = "Governance.View"   // 查看团队、客户、预算和限流配置。
	GovernanceManage Permission = "Governance.Manage" // 新增、修改和删除上述治理配置。

	// 路由规则：决定请求交给哪个厂商或模型处理。
	RoutingRulesView   Permission = "RoutingRules.View"   // 查看请求路由规则。
	RoutingRulesManage Permission = "RoutingRules.Manage" // 新增、修改和删除请求路由规则。

	// 日志与费用：模型调用记录、统计和费用报表。
	LogsView          Permission = "Logs.View"          // 查看日志列表、统计和报表，不含请求与响应正文。
	LogsManage        Permission = "Logs.Manage"        // 清理日志、重算费用等管理操作。
	LogsRevealContent Permission = "Logs.RevealContent" // 敏感权限：查看请求与响应正文。
	LogsExport        Permission = "Logs.Export"        // 敏感权限：导出日志数据；正文访问仍受正文权限限制。

	// MCP网关：通过MCP协议连接供模型使用的外部工具服务。
	MCPGatewayView   Permission = "MCPGateway.View"   // 查看工具服务及工具列表。
	MCPGatewayManage Permission = "MCPGateway.Manage" // 管理工具服务、连接和授权配置。

	// 插件：扩展网关处理能力；原生插件可以在网关服务器上执行代码。
	PluginsView       Permission = "Plugins.View"       // 查看插件列表与配置。
	PluginsManage     Permission = "Plugins.Manage"     // 新增、修改和删除插件配置。
	PluginsLoadNative Permission = "Plugins.LoadNative" // 敏感权限：加载在服务器上执行代码的原生插件。

	// 提示词库：集中维护供模型调用使用的提示词与技能。
	PromptRepositoryView   Permission = "PromptRepository.View"   // 查看提示词与技能。
	PromptRepositoryManage Permission = "PromptRepository.Manage" // 新增、修改和删除提示词与技能。

	// 系统设置：网关常规配置，以及单独授权的认证配置和配置导入。
	SettingsView         Permission = "Settings.View"         // 查看允许展示的系统配置。
	SettingsManage       Permission = "Settings.Manage"       // 修改常规配置、代理、功能开关和缓存设置。
	SettingsAuthConfig   Permission = "Settings.AuthConfig"   // 预留敏感权限：修改登录认证设置，当前无开放入口。
	SettingsImportConfig Permission = "Settings.ImportConfig" // 预留敏感权限：导入整份配置，当前无开放入口。

	// 通知与Webhook：控制台消息，以及向其他系统发送事件数据的通知机制。
	NotificationsView   Permission = "Notifications.View"   // 查看通知及Webhook配置。
	NotificationsManage Permission = "Notifications.Manage" // 管理通知及Webhook配置、执行相关管理操作。

	// 安全：跨模块的敏感操作，仍需同时拥有对应模块的管理权限。
	SecurityChangeCredentialDestination Permission = "Security.ChangeCredentialDestination" // 沿用已有凭据更换发送地址、代理或TLS信任设置；不包含明文读取。

	// 账号与角色：控制台使用者，以及授予他们的操作权限。
	UsersView   Permission = "Users.View"   // 查看账号、角色和角色分配。
	UsersManage Permission = "Users.Manage" // 管理账号、增删改角色、分配角色，仍要遵守管理员保护规则。
)

// Permissions 按目录顺序列出所有权限标识，包括已定义但还没有开放入口的权限。
func Permissions() []Permission {
	out := []Permission{}
	for _, m := range Catalogue() {
		for _, p := range m.Permissions {
			out = append(out, p.Code)
		}
	}
	return out
}

// KnownPermission 检查权限标识是否在目录里；目录里没有的权限，不能配置给角色。
func KnownPermission(p Permission) bool {
	for _, v := range Permissions() {
		if p == v {
			return true
		}
	}
	return false
}
