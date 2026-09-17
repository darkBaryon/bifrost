// 本文件检查插件操作的额外权限，隐藏配置中的秘密，并在更新时恢复隐藏字段的原值。
package host

import (
	"encoding/json"
	"errors"
	"reflect"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/plugins/compat"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/maximhq/bifrost/plugins/logging"
	"github.com/maximhq/bifrost/plugins/maxim"
	"github.com/maximhq/bifrost/plugins/otel"
	"github.com/maximhq/bifrost/plugins/routing"
	"github.com/maximhq/bifrost/plugins/semanticcache"
	"github.com/maximhq/bifrost/plugins/telemetry"
	"github.com/tidwall/gjson"
	"github.com/valyala/fasthttp"
)

// typedPluginConfig 按内置插件自己的配置类型读取字段，再隐藏其中的凭据。
func typedPluginConfig(name string, raw object) object {
	var config any
	switch name {
	case "governance":
		config = &governance.Config{}
	case "logging":
		config = &logging.Config{}
	case "otel":
		config = &otel.Config{}
	case "telemetry":
		config = &telemetry.Config{}
	case "maxim":
		config = &maxim.Config{}
	case "compat":
		config = &compat.Config{}
	case "semantic_cache":
		config = &semanticcache.Config{}
	case "routing":
		config = &routing.Config{}
	default:
		return object{}
	}
	data, e := json.Marshal(raw)
	if e != nil || json.Unmarshal(data, config) != nil {
		return object{}
	}
	hideSecretVars(reflect.ValueOf(config))
	data, e = json.Marshal(config)
	if e != nil {
		return object{}
	}
	var out object
	if json.Unmarshal(data, &out) != nil {
		return object{}
	}
	if name == "semantic_cache" {
		if ttl, exists := raw["ttl"]; exists {
			out["ttl"] = ttl
		}
	}
	return out
}

var secretVarType = reflect.TypeOf(schemas.SecretVar{})

// hideSecretVars 沿配置结构找到SecretVar凭据，把它们替换成隐藏标记。
func hideSecretVars(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		if v.Elem().Type() == secretVarType && v.CanSet() {
			old := v.Interface().(*schemas.SecretVar)
			v.Set(reflect.ValueOf(old.FullyRedacted()))
			return
		}
		hideSecretVars(v.Elem())
		return
	}
	if v.Type() == secretVarType && v.CanSet() {
		old := v.Interface().(schemas.SecretVar)
		v.Set(reflect.ValueOf(*old.FullyRedacted()))
		return
	}
	switch v.Kind() {
	case reflect.Map:
		for _, key := range v.MapKeys() {
			value := reflect.New(v.Type().Elem()).Elem()
			value.Set(v.MapIndex(key))
			hideSecretVars(value)
			v.SetMapIndex(key, value)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanInterface() {
				hideSecretVars(v.Field(i))
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			hideSecretVars(v.Index(i))
		}
	}
}

// 内置插件可能修改其他功能；这里列出除插件管理外还需要的权限。
var builtinPermissions = map[string][]rbac.Permission{
	"governance":     {rbac.GovernanceManage, rbac.VirtualKeysManage, rbac.RoutingRulesManage},
	"logging":        {rbac.LogsManage},
	"semantic_cache": {rbac.SettingsManage},
	"telemetry":      {rbac.SettingsManage},
	"otel":           {rbac.SettingsManage, rbac.LogsRevealContent},
	"maxim":          {rbac.SettingsManage, rbac.LogsRevealContent},
	"prompts":        {rbac.PromptRepositoryManage},
	"routing":        {rbac.RoutingRulesManage},
	"compat":         {},
}

// checkPluginMutation 检查插件所属功能的权限；操作自定义原生插件还要有LoadNative权限。
func checkPluginMutation(c *fasthttp.RequestCtx, access rbac.Access) error {
	name, _ := c.UserValue("name").(string)
	if c.IsPost() {
		name = gjson.GetBytes(c.PostBody(), "name").String()
	}
	codes, builtin := builtinPermissions[name]
	if !builtin {
		return requirePermissions(access, rbac.PluginsLoadNative)
	}
	if e := requirePermissions(access, codes...); e != nil {
		return e
	}
	if name == "logging" && len(c.PostBody()) > 0 {
		config := gjson.GetBytes(c.PostBody(), "config")
		for _, field := range []string{"disable_content_logging", "retain_content_in_object_storage", "request_headers", "logging_headers"} {
			v := config.Get(field)
			if v.Exists() && (field != "disable_content_logging" || !v.Bool()) {
				return requirePermissions(access, rbac.LogsRevealContent)
			}
		}
	}
	return nil
}

// projectPlugins 隐藏插件配置的秘密和无权查看的运行日志。
func projectPlugins(value any, access rbac.Access) {
	objects(value, []string{"plugins", "plugin"}, func(v object) {
		if status, ok := v["status"].(map[string]any); ok && !access.Allows(rbac.LogsRevealContent) {
			status["logs"] = []any{}
		}
		config, exists := v["config"]
		if !exists {
			return
		}
		name, _ := v["name"].(string)
		_, builtin := builtinPermissions[name]
		if !builtin {
			if !access.Allows(rbac.PluginsLoadNative) {
				v["config"] = object{}
			}
			return
		}
		cfg, ok := config.(map[string]any)
		if !ok {
			v["config"] = object{}
			return
		}
		cfg = typedPluginConfig(name, cfg)
		v["config"] = cfg
		switch name {
		case "governance":
			projectGovernance(cfg, access)
		case "routing":
			projectRouting(cfg, access)
		case "logging", "otel", "maxim":
			if !access.Allows(rbac.LogsRevealContent) {
				for _, key := range []string{"request_headers", "headers", "attributes", "resource_attributes", "logs", "metadata"} {
					delete(cfg, key)
				}
			}
			if _, ok := cfg["api_key"]; ok {
				cfg["api_key"] = redacted
			}
		}
		objects(cfg, []string{"exporter", "connection", "config", "push_gateway", "vector_store", "log_store", "otel_config", "logging_config", "maxim_config", "profiles"}, func(v object) {
			maskHeaderMaps(v)
			for _, key := range []string{"url", "endpoint", "push_gateway_url"} {
				maskURL(v, key)
			}
		})
	})
}

// validatePluginNames 确认内置或已加载插件列表仍然是字符串列表。
func validatePluginNames(value any) error {
	outer, ok := value.(map[string]any)
	if !ok {
		return rbac.ErrUnavailable
	}
	names, exists := outer["plugins"]
	if !exists {
		return rbac.ErrUnavailable
	}
	if names == nil {
		return nil
	}
	rows, ok := names.([]any)
	if !ok {
		return rbac.ErrUnavailable
	}
	for _, row := range rows {
		if _, ok := row.(string); !ok {
			return rbac.ErrUnavailable
		}
	}
	return nil
}

// hasMarker 检查请求是否包含要求保留原值的隐藏标记。
func hasMarker(v any) bool {
	switch v := v.(type) {
	case string:
		return v == redacted
	case map[string]any:
		for _, child := range v {
			if hasMarker(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if hasMarker(child) {
				return true
			}
		}
	}
	return false
}

// restoreMarkers 把请求里的隐藏标记换回旧值；找不到旧值就拒绝。
func restoreMarkers(desired, current any) (any, error) {
	if s, ok := desired.(string); ok && s == redacted {
		if current == nil || current == redacted {
			return nil, rbac.ErrInvalid
		}
		return current, nil
	}
	switch d := desired.(type) {
	case map[string]any:
		// SecretVar的完整遮盖代表整个凭据；数据库格式可能仍是字符串/env引用。
		if value, ok := d["value"].(string); ok && value == redacted {
			if current == nil {
				return nil, rbac.ErrInvalid
			}
			return current, nil
		}
		old, _ := current.(map[string]any)
		for key, child := range d {
			restored, e := restoreMarkers(child, old[key])
			if e != nil {
				return nil, e
			}
			d[key] = restored
		}
	case []any:
		old, _ := current.([]any)
		for i, child := range d {
			var previous any
			if i < len(old) {
				previous = old[i]
			}
			restored, e := restoreMarkers(child, previous)
			if e != nil {
				return nil, e
			}
			d[i] = restored
		}
	}
	return desired, nil
}

// restorePlugin 更新内置插件时读取旧配置，恢复被前端原样传回的隐藏字段。
func (a *Adapter) restorePlugin(c *fasthttp.RequestCtx) error {
	if !c.IsPut() && !c.IsPost() {
		return nil
	}
	var body object
	if json.Unmarshal(c.PostBody(), &body) != nil {
		return rbac.ErrInvalid
	}
	config, ok := body["config"]
	if !ok || !hasMarker(config) {
		return nil
	}
	if c.IsPost() {
		return rbac.ErrInvalid
	}
	name, _ := c.UserValue("name").(string)
	// 自定义配置由具备 LoadNative 的操作者完全控制。
	if _, builtin := builtinPermissions[name]; !builtin {
		return nil
	}
	if a.config == nil || a.config.ConfigStore == nil {
		return rbac.ErrUnavailable
	}
	stored, e := a.config.ConfigStore.GetPlugin(c, name)
	if e != nil {
		if errors.Is(e, configstore.ErrNotFound) {
			return rbac.ErrInvalid
		}
		return rbac.ErrUnavailable
	}
	if stored == nil {
		return rbac.ErrInvalid
	}
	raw, e := json.Marshal(stored.Config)
	if e != nil {
		return rbac.ErrUnavailable
	}
	var current any
	if json.Unmarshal(raw, &current) != nil {
		return rbac.ErrUnavailable
	}
	body["config"], e = restoreMarkers(config, current)
	if e != nil {
		return e
	}
	raw, e = json.Marshal(body)
	if e != nil {
		return rbac.ErrUnavailable
	}
	c.Request.SetBody(raw)
	return nil
}
