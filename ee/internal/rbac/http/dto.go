// 本文件只输出安全业务字段；矩阵是权限展示投影，不参与业务判权。
package rbachttp

import (
	"time"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
)

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

func systemCode(c rbac.SystemCode) *rbac.SystemCode {
	if c == "" {
		return nil
	}
	return &c
}

func roleResponse(r rbac.Role) roleDTO {
	return roleDTO{r.ID, r.Name, r.Description, systemCode(r.SystemCode), append([]rbac.Permission{}, r.PermissionCodes...), r.AccountCount, r.CreatedAt.UTC(), r.UpdatedAt.UTC()}
}

type roleSummary struct {
	ID         rbac.RoleID      `json:"id"`
	Name       string           `json:"name"`
	SystemCode *rbac.SystemCode `json:"system_code"`
}

type grantDTO struct {
	Code    rbac.Permission `json:"code"`
	RoleIDs []rbac.RoleID   `json:"role_ids"`
}

type effectiveDTO struct {
	AccountID   string                                  `json:"account_id"`
	Roles       []roleSummary                           `json:"roles"`
	Permissions []grantDTO                              `json:"permissions"`
	Matrix      map[resourceCode]map[operationCode]bool `json:"matrix"`
}

type permissionDTO struct {
	Code      rbac.Permission `json:"code"`
	Name      string          `json:"name"`
	Available bool            `json:"available"`
}

type moduleDTO struct {
	Code        rbac.ModuleCode `json:"code"`
	Name        string          `json:"name"`
	Permissions []permissionDTO `json:"permissions"`
}

func effectiveResponse(e rbac.Effective) effectiveDTO {
	out := effectiveDTO{AccountID: e.AccountID, Roles: []roleSummary{}, Permissions: []grantDTO{}, Matrix: matrix(e.Permissions)}
	for _, r := range e.Roles {
		out.Roles = append(out.Roles, roleSummary{r.ID, r.Name, systemCode(r.SystemCode)})
	}
	for _, p := range e.Permissions {
		out.Permissions = append(out.Permissions, grantDTO{p.Code, append([]rbac.RoleID{}, p.RoleIDs...)})
	}
	return out
}

// 资源和操作编码沿用基线UI；权限映射显式按Permission常量声明，不解析编码字符串。
type resourceCode string

type operationCode string

const (
	resourceGuardrailsConfig         resourceCode  = "GuardrailsConfig"
	resourceGuardrailsProviders      resourceCode  = "GuardrailsProviders"
	resourceGuardrailRules           resourceCode  = "GuardrailRules"
	resourceUserProvisioning         resourceCode  = "UserProvisioning"
	resourceCluster                  resourceCode  = "Cluster"
	resourceSettings                 resourceCode  = "Settings"
	resourceUsers                    resourceCode  = "Users"
	resourceLogs                     resourceCode  = "Logs"
	resourceObservability            resourceCode  = "Observability"
	resourceDashboard                resourceCode  = "Dashboard"
	resourceVirtualKeys              resourceCode  = "VirtualKeys"
	resourceModelProvider            resourceCode  = "ModelProvider"
	resourcePlugins                  resourceCode  = "Plugins"
	resourceMCPGateway               resourceCode  = "MCPGateway"
	resourceMCPToolGroups            resourceCode  = "MCPToolGroups"
	resourceMCPLogs                  resourceCode  = "MCPLogs"
	resourceAdaptiveRouter           resourceCode  = "AdaptiveRouter"
	resourceAuditLogs                resourceCode  = "AuditLogs"
	resourceCustomers                resourceCode  = "Customers"
	resourceTeams                    resourceCode  = "Teams"
	resourceRBAC                     resourceCode  = "RBAC"
	resourceGovernance               resourceCode  = "Governance"
	resourceRoutingRules             resourceCode  = "RoutingRules"
	resourcePromptRepository         resourceCode  = "PromptRepository"
	resourcePromptDeploymentStrategy resourceCode  = "PromptDeploymentStrategy"
	resourceAccessProfiles           resourceCode  = "AccessProfiles"
	resourceProjects                 resourceCode  = "Projects"
	resourceAPIKeys                  resourceCode  = "APIKeys"
	resourceInference                resourceCode  = "Inference"
	resourceMetrics                  resourceCode  = "Metrics"
	resourceFeatureFlags             resourceCode  = "FeatureFlags"
	resourceCircuitBreaker           resourceCode  = "CircuitBreaker"
	resourceDevices                  resourceCode  = "Devices"
	resourceInventory                resourceCode  = "Inventory"
	resourceEdgeConfig               resourceCode  = "EdgeConfig"
	resourceSkillsRepository         resourceCode  = "SkillsRepository"
	resourceNotifications            resourceCode  = "Notifications"
	operationView                    operationCode = "View"
	operationRead                    operationCode = "Read"
	operationCreate                  operationCode = "Create"
	operationUpdate                  operationCode = "Update"
	operationDelete                  operationCode = "Delete"
	operationReveal                  operationCode = "Reveal"
	operationDownload                operationCode = "Download"
	operationLoadNative              operationCode = "LoadNative"
	operationAuthConfig              operationCode = "AuthConfig"
	operationImportConfig            operationCode = "ImportConfig"
)

var resources = []resourceCode{resourceGuardrailsConfig, resourceGuardrailsProviders, resourceGuardrailRules, resourceUserProvisioning, resourceCluster, resourceSettings, resourceUsers, resourceLogs, resourceObservability, resourceDashboard, resourceVirtualKeys, resourceModelProvider, resourcePlugins, resourceMCPGateway, resourceMCPToolGroups, resourceMCPLogs, resourceAdaptiveRouter, resourceAuditLogs, resourceCustomers, resourceTeams, resourceRBAC, resourceGovernance, resourceRoutingRules, resourcePromptRepository, resourcePromptDeploymentStrategy, resourceAccessProfiles, resourceProjects, resourceAPIKeys, resourceInference, resourceMetrics, resourceFeatureFlags, resourceCircuitBreaker, resourceDevices, resourceInventory, resourceEdgeConfig, resourceSkillsRepository, resourceNotifications}

type matrixGrant struct {
	resources  []resourceCode
	operations []operationCode
}

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
}

func matrix(grants []rbac.Grant) map[resourceCode]map[operationCode]bool {
	out := map[resourceCode]map[operationCode]bool{}
	for _, resource := range resources {
		out[resource] = map[operationCode]bool{operationView: false, operationRead: false, operationCreate: false, operationUpdate: false, operationDelete: false, operationReveal: false, operationDownload: false}
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
