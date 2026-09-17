// 本文件把账号权限整理成前端使用的权限表，例如Logs.Update表示能否显示日志修改操作。
// 前端据此显示入口和按钮；能否真正执行操作，仍由后端接口检查。
package rbachttp

import "github.com/darkBaryon/bifrost/ee/internal/rbac"

// resourceCode 是前端已有的功能标识，例如Logs表示日志、Teams表示团队。
type resourceCode string

// operationCode 是前端已有的操作标识，例如View表示查看、Update表示修改。
type operationCode string

const (
	resourceGuardrailsConfig         resourceCode = "GuardrailsConfig"
	resourceGuardrailsProviders      resourceCode = "GuardrailsProviders"
	resourceGuardrailRules           resourceCode = "GuardrailRules"
	resourceUserProvisioning         resourceCode = "UserProvisioning"
	resourceCluster                  resourceCode = "Cluster"
	resourceSettings                 resourceCode = "Settings"
	resourceUsers                    resourceCode = "Users"
	resourceLogs                     resourceCode = "Logs"
	resourceObservability            resourceCode = "Observability"
	resourceDashboard                resourceCode = "Dashboard"
	resourceVirtualKeys              resourceCode = "VirtualKeys"
	resourceModelProvider            resourceCode = "ModelProvider"
	resourcePlugins                  resourceCode = "Plugins"
	resourceMCPGateway               resourceCode = "MCPGateway"
	resourceMCPToolGroups            resourceCode = "MCPToolGroups"
	resourceMCPLogs                  resourceCode = "MCPLogs"
	resourceAdaptiveRouter           resourceCode = "AdaptiveRouter"
	resourceAuditLogs                resourceCode = "AuditLogs"
	resourceCustomers                resourceCode = "Customers"
	resourceTeams                    resourceCode = "Teams"
	resourceRBAC                     resourceCode = "RBAC"
	resourceGovernance               resourceCode = "Governance"
	resourceRoutingRules             resourceCode = "RoutingRules"
	resourcePromptRepository         resourceCode = "PromptRepository"
	resourcePromptDeploymentStrategy resourceCode = "PromptDeploymentStrategy"
	resourceAccessProfiles           resourceCode = "AccessProfiles"
	resourceProjects                 resourceCode = "Projects"
	resourceAPIKeys                  resourceCode = "APIKeys"
	resourceInference                resourceCode = "Inference"
	resourceMetrics                  resourceCode = "Metrics"
	resourceFeatureFlags             resourceCode = "FeatureFlags"
	resourceCircuitBreaker           resourceCode = "CircuitBreaker"
	resourceDevices                  resourceCode = "Devices"
	resourceInventory                resourceCode = "Inventory"
	resourceEdgeConfig               resourceCode = "EdgeConfig"
	resourceSkillsRepository         resourceCode = "SkillsRepository"
	resourceNotifications            resourceCode = "Notifications"
)

// 这些操作名沿用现有前端；View和Read都属于查看，Create、Update和Delete分别表示新增、修改、删除。
const (
	operationView         operationCode = "View"
	operationRead         operationCode = "Read"
	operationCreate       operationCode = "Create"
	operationUpdate       operationCode = "Update"
	operationDelete       operationCode = "Delete"
	operationReveal       operationCode = "Reveal"
	operationDownload     operationCode = "Download"
	operationLoadNative   operationCode = "LoadNative"
	operationAuthConfig   operationCode = "AuthConfig"
	operationImportConfig operationCode = "ImportConfig"
)

// 列出前端已有的功能，包括尚未接入角色授权的功能；没有权限映射的功能始终返回false。
var resources = []resourceCode{
	resourceGuardrailsConfig,
	resourceGuardrailsProviders,
	resourceGuardrailRules,
	resourceUserProvisioning,
	resourceCluster,
	resourceSettings,
	resourceUsers,
	resourceLogs,
	resourceObservability,
	resourceDashboard,
	resourceVirtualKeys,
	resourceModelProvider,
	resourcePlugins,
	resourceMCPGateway,
	resourceMCPToolGroups,
	resourceMCPLogs,
	resourceAdaptiveRouter,
	resourceAuditLogs,
	resourceCustomers,
	resourceTeams,
	resourceRBAC,
	resourceGovernance,
	resourceRoutingRules,
	resourcePromptRepository,
	resourcePromptDeploymentStrategy,
	resourceAccessProfiles,
	resourceProjects,
	resourceAPIKeys,
	resourceInference,
	resourceMetrics,
	resourceFeatureFlags,
	resourceCircuitBreaker,
	resourceDevices,
	resourceInventory,
	resourceEdgeConfig,
	resourceSkillsRepository,
	resourceNotifications,
}

// matrixGrant 说明一个后端权限要打开前端哪些功能的哪些操作。
type matrixGrant struct {
	resources  []resourceCode
	operations []operationCode
}

// 逐项列出权限对应关系，例如Governance.View允许查看治理、团队和客户。
// Manage只对应新增、修改和删除，不自动包含查看或敏感操作。
var matrixGrants = map[rbac.Permission]matrixGrant{
	rbac.ModelProviderView:      {[]resourceCode{resourceModelProvider}, []operationCode{operationView, operationRead}},
	rbac.ModelProviderManage:    {[]resourceCode{resourceModelProvider}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.VirtualKeysView:        {[]resourceCode{resourceVirtualKeys}, []operationCode{operationView, operationRead}},
	rbac.VirtualKeysManage:      {[]resourceCode{resourceVirtualKeys}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.GovernanceView:         {[]resourceCode{resourceGovernance, resourceTeams, resourceCustomers}, []operationCode{operationView, operationRead}},
	rbac.GovernanceManage:       {[]resourceCode{resourceGovernance, resourceTeams, resourceCustomers}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.RoutingRulesView:       {[]resourceCode{resourceRoutingRules}, []operationCode{operationView, operationRead}},
	rbac.RoutingRulesManage:     {[]resourceCode{resourceRoutingRules}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.LogsView:               {[]resourceCode{resourceLogs, resourceMCPLogs, resourceDashboard}, []operationCode{operationView, operationRead}},
	rbac.LogsManage:             {[]resourceCode{resourceLogs, resourceMCPLogs, resourceDashboard}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.MCPGatewayView:         {[]resourceCode{resourceMCPGateway, resourceMCPToolGroups}, []operationCode{operationView, operationRead}},
	rbac.MCPGatewayManage:       {[]resourceCode{resourceMCPGateway, resourceMCPToolGroups}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.PluginsView:            {[]resourceCode{resourcePlugins}, []operationCode{operationView, operationRead}},
	rbac.PluginsManage:          {[]resourceCode{resourcePlugins}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.PromptRepositoryView:   {[]resourceCode{resourcePromptRepository, resourceSkillsRepository, resourcePromptDeploymentStrategy}, []operationCode{operationView, operationRead}},
	rbac.PromptRepositoryManage: {[]resourceCode{resourcePromptRepository, resourceSkillsRepository, resourcePromptDeploymentStrategy}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.SettingsView:           {[]resourceCode{resourceSettings, resourceFeatureFlags}, []operationCode{operationView, operationRead}},
	rbac.SettingsManage:         {[]resourceCode{resourceSettings, resourceFeatureFlags}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.NotificationsView:      {[]resourceCode{resourceNotifications}, []operationCode{operationView, operationRead}},
	rbac.NotificationsManage:    {[]resourceCode{resourceNotifications}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.UsersView:              {[]resourceCode{resourceUsers, resourceRBAC, resourceUserProvisioning}, []operationCode{operationView, operationRead}},
	rbac.UsersManage:            {[]resourceCode{resourceUsers, resourceRBAC, resourceUserProvisioning}, []operationCode{operationCreate, operationUpdate, operationDelete}},
	rbac.ModelProviderRevealKey: {[]resourceCode{resourceModelProvider}, []operationCode{operationReveal}},
	rbac.VirtualKeysRevealKey:   {[]resourceCode{resourceVirtualKeys}, []operationCode{operationReveal}},
	rbac.LogsRevealContent:      {[]resourceCode{resourceLogs, resourceMCPLogs}, []operationCode{operationReveal}},
	rbac.LogsExport:             {[]resourceCode{resourceLogs, resourceMCPLogs}, []operationCode{operationDownload}},
	rbac.PluginsLoadNative:      {[]resourceCode{resourcePlugins}, []operationCode{operationLoadNative}},
	rbac.SettingsAuthConfig:     {[]resourceCode{resourceSettings}, []operationCode{operationAuthConfig}},
	rbac.SettingsImportConfig:   {[]resourceCode{resourceSettings}, []operationCode{operationImportConfig}},

	// 前端没有独立操作开关；保存配置时由后端检查完整权限组合。
	rbac.SecurityChangeCredentialDestination: {},
}

// matrix 先将所有操作设为false，再打开账号已有权限对应的操作；不根据权限字符串猜测关系。
func matrix(grants []rbac.Grant) map[resourceCode]map[operationCode]bool {
	out := map[resourceCode]map[operationCode]bool{}
	for _, resource := range resources {
		out[resource] = map[operationCode]bool{
			operationView:     false,
			operationRead:     false,
			operationCreate:   false,
			operationUpdate:   false,
			operationDelete:   false,
			operationReveal:   false,
			operationDownload: false,
		}
	}
	out[resourcePlugins][operationLoadNative] = false
	out[resourceSettings][operationAuthConfig] = false
	out[resourceSettings][operationImportConfig] = false
	for _, grant := range grants {
		mapping := matrixGrants[grant.Code]
		for _, resource := range mapping.resources {
			for _, operation := range mapping.operations {
				out[resource][operation] = true
			}
		}
	}
	return out
}
