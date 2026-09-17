// 本文件检查设置修改的权限，隐藏和恢复秘密，并检查代理携带旧凭据修改连接设置的操作。
package host

import (
	"context"
	"errors"
	"reflect"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/transports/bifrost-http/handlers"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// 非零/非空才更新的字段来自宿主 updateConfig；其余字段比较解码后的默认值。
var clientOptional = map[string]bool{"initial_pool_size": true, "max_request_body_size_mb": true, "mcp_agent_depth": true, "mcp_tool_execution_timeout": true, "mcp_code_mode_binding_level": true, "mcp_server_auth_mode": true, "dual_credential_conflict_behavior": true, "async_job_result_ttl": true, "routing_chain_max_depth": true, "enable_logging": true, "oauth2_server_config": true}

// settingsUpdate 比较新旧设置，逐项检查真正发生的改动需要哪些额外权限。
func (a *Adapter) settingsUpdate(_ context.Context, desired *handlers.ConsoleSettingsUpdate, access rbac.Access) error {
	if a.config == nil || desired == nil || desired.Client == nil || desired.Framework == nil {
		return rbac.ErrUnavailable
	}
	a.config.Mu.RLock()
	current := a.config.ClientConfig
	framework := a.config.FrameworkConfig
	if current == nil {
		a.config.Mu.RUnlock()
		return rbac.ErrUnavailable
	}
	copied := *current
	a.config.Mu.RUnlock()
	current = &copied
	if next := desired.Client.MCPExternalClientURL; next != nil && next.IsRedacted() {
		old := current.MCPExternalClientURL
		if old == nil || !next.Equals(old.Redacted()) {
			return rbac.ErrInvalid
		}
		restored := *old
		desired.Client.MCPExternalClientURL = &restored
	}
	before := reflect.ValueOf(*current)
	after := reflect.ValueOf(*desired.Client)
	typ := after.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if name == "-" {
			continue
		}
		permissions, ok := clientPermissions[name]
		if !ok {
			return rbac.ErrUnavailable
		}
		value := after.Field(i)
		if clientOptional[name] && value.IsZero() {
			continue
		}
		if reflect.DeepEqual(before.Field(i).Interface(), value.Interface()) {
			continue
		}
		if name == "enable_logging" && current.EnableLogging == nil && desired.Client.EnableLogging != nil && *desired.Client.EnableLogging {
			continue
		}
		for _, code := range permissions {
			if !access.Allows(code) {
				return rbac.ErrForbidden
			}
		}
	}
	normalized, _, _ := lib.ResolveFrameworkPricingConfig(nil, framework)
	for _, pair := range []struct {
		desired **string
		current *string
	}{
		{&desired.Framework.PricingURL, normalized.PricingURL}, {&desired.Framework.ModelParametersURL, normalized.ModelParametersURL}, {&desired.Framework.MCPLibraryURL, normalized.MCPLibraryURL},
	} {
		if e := restoreURL(pair.desired, pair.current); e != nil {
			return e
		}
	}
	return nil
}

var clientPermissions = map[string][]rbac.Permission{
	"drop_excess_requests":                       {},
	"initial_pool_size":                          {},
	"prometheus_labels":                          {},
	"enable_logging":                             {rbac.LogsManage},
	"disable_content_logging":                    {rbac.LogsManage, rbac.LogsRevealContent},
	"retain_content_in_object_storage":           {rbac.LogsManage, rbac.LogsRevealContent},
	"allow_per_request_content_storage_override": {rbac.LogsManage, rbac.LogsRevealContent},
	"allow_per_request_raw_override":             {rbac.LogsManage, rbac.LogsRevealContent},
	"allow_direct_keys":                          {rbac.GovernanceManage},
	"vk_rotation_cooldown":                       {rbac.VirtualKeysManage},
	"disable_db_pings_in_health":                 {},
	"log_retention_days":                         {rbac.LogsManage},
	"enforce_auth_on_inference":                  {rbac.GovernanceManage},
	"dual_credential_conflict_behavior":          {rbac.GovernanceManage},
	"enforce_governance_header":                  {}, // 上游按 enforce_auth_on_inference 派生，不直接采用此字段。
	"enforce_scim_auth":                          {}, // 上游按 enforce_auth_on_inference 派生，不直接采用此字段。
	"allowed_origins":                            {},
	"allowed_headers":                            {},
	"max_request_body_size_mb":                   {},
	"compat":                                     {rbac.PluginsManage},
	"mcp_agent_depth":                            {rbac.MCPGatewayManage},
	"mcp_tool_execution_timeout":                 {rbac.MCPGatewayManage},
	"mcp_code_mode_binding_level":                {rbac.MCPGatewayManage},
	"mcp_tool_sync_interval":                     {rbac.MCPGatewayManage},
	"mcp_disable_auto_tool_inject":               {rbac.MCPGatewayManage},
	"mcp_enable_temp_token_auth":                 {rbac.MCPGatewayManage},
	"header_filter_config":                       {},
	"async_job_result_ttl":                       {},
	"required_headers":                           {},
	"logging_headers":                            {rbac.LogsManage, rbac.LogsRevealContent},
	"whitelisted_routes":                         {}, // 由身份适配检查，不在这里重复判权。
	"hide_deleted_virtual_keys_in_filters":       {rbac.LogsManage},
	"routing_chain_max_depth":                    {rbac.RoutingRulesManage},
	"mcp_external_client_url":                    {rbac.MCPGatewayManage},
	"mcp_server_auth_mode":                       {rbac.MCPGatewayManage},
	"oauth2_server_config":                       {rbac.MCPGatewayManage},
	"dump_errors_in_console_logs":                {rbac.LogsManage, rbac.LogsRevealContent},
	"webhook_config":                             {rbac.NotificationsManage},
}

// projectSettings 隐藏设置中的带凭据地址和请求头。
func projectSettings(value any) {
	objects(value, []string{"client_config", "framework_config", "proxy_config", "restart_required", "config", "client", "framework", "proxy"}, func(v object) {
		for _, key := range []string{"pricing_url", "model_parameters_url", "mcp_library_url"} {
			maskURL(v, key)
		}
		maskURLWithParser(v, "url", parseProxyURL)
		maskHeaderMaps(v)
	})
}

// proxyUpdate 恢复隐藏的代理地址，并检查旧凭据是否改用了新地址或TLS信任设置。
func (a *Adapter) proxyUpdate(ctx context.Context, desired *tables.GlobalProxyConfig, access rbac.Access) error {
	if desired == nil {
		return consoleError(rbac.ErrInvalid)
	}
	if a.config == nil || a.config.ConfigStore == nil {
		return consoleError(rbac.ErrUnavailable)
	}
	current, e := a.config.ConfigStore.GetProxyConfig(ctx)
	if e != nil && !errors.Is(e, configstore.ErrNotFound) {
		return consoleError(rbac.ErrUnavailable)
	}
	if desired.URL == redacted {
		if current == nil || current.URL == "" || current.URL == redacted {
			return consoleError(rbac.ErrInvalid)
		}
		desired.URL = current.URL
	}
	if current == nil {
		return nil
	}
	password := desired.Password
	if password == redacted {
		password = current.Password
	}
	// 即使本次先停用，保存的新地址也不能为以后重新启用留下绕过。
	retained := retainedValues([]string{current.Password}, []string{password})
	// URL内认证与独立Password都可能继续使用，分别检查是否保留。
	nextCredential, err := proxyURLCredential(desired.URL)
	if err != nil {
		return consoleError(rbac.ErrInvalid)
	}
	oldCredential, err := proxyURLCredential(current.URL)
	// 旧地址无法识别时保守要求权限，仍允许有权限的人修正坏配置。
	retained = retained || err != nil || retainedValues([]string{oldCredential}, []string{nextCredential})

	target := func(p *tables.GlobalProxyConfig) any {
		return struct {
			Type, URL     string
			SkipTLSVerify bool
		}{string(p.Type), p.URL, p.SkipTLSVerify}
	}
	before, after := target(current), target(desired)
	return consoleError(credentialDestination(access, retained, before, after))
}

// restoreURL 恢复隐藏地址；没有旧地址时拒绝保存占位文字。
func restoreURL(desired **string, current *string) error {
	if *desired == nil || **desired != redacted {
		return nil
	}
	if current == nil || *current == "" || *current == redacted {
		return rbac.ErrInvalid
	}
	restored := *current
	*desired = &restored
	return nil
}

// checkSettingsInput 拒绝未知配置字段，避免新增字段绕过权限检查。
func checkSettingsInput(c *fasthttp.RequestCtx) error {
	allowed := map[string]bool{"client_config": true, "framework_config": true, "auth_config": true, "env_label": true, "proxy_config": true, "restart_required": true, "metadata": true, "is_governance_enabled": true, "is_auth_enabled": true, "is_logging_enabled": true, "is_db_connected": true, "is_git_available": true, "is_cache_connected": true, "is_logs_connected": true, "is_object_storage_connected": true}
	invalid := false
	gjson.ParseBytes(c.PostBody()).ForEach(func(k, v gjson.Result) bool {
		if !allowed[k.String()] {
			invalid = true
		}
		return true
	})
	if invalid {
		return rbac.ErrInvalid
	}
	client := gjson.GetBytes(c.PostBody(), "client_config")
	client.ForEach(func(k, _ gjson.Result) bool {
		if _, ok := clientPermissions[k.String()]; !ok {
			invalid = true
		}
		return true
	})
	if invalid {
		return rbac.ErrInvalid
	}
	return nil
}
