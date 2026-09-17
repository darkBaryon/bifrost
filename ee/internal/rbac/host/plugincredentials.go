// 本文件检查内置插件携带旧凭据更换上报地址的操作；不影响插件普通参数更新。
package host

import (
	"encoding/json"
	"maps"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/plugins/otel"
	"github.com/maximhq/bifrost/plugins/telemetry"
)

// pluginStoredShape 统一OTEL的旧单对象/新profiles格式与SecretVar格式，再恢复隐藏值。
func pluginStoredShape(name string, value any) (any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, rbac.ErrInvalid
	}
	switch name {
	case "otel":
		var c otel.Config
		if json.Unmarshal(raw, &c) != nil {
			return nil, rbac.ErrInvalid
		}
		raw, err = c.MarshalForStorage()
	case "telemetry":
		var c telemetry.Config
		if json.Unmarshal(raw, &c) != nil {
			return nil, rbac.ErrInvalid
		}
		raw, err = c.MarshalForStorage()
	default:
		return value, nil
	}
	if err != nil {
		return nil, rbac.ErrInvalid
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil, rbac.ErrInvalid
	}
	return out, nil
}

func pluginDestination(name string, current, desired any, access rbac.Access) error {
	oldJSON, err := json.Marshal(current)
	if err != nil {
		return rbac.ErrUnavailable
	}
	nextJSON, err := json.Marshal(desired)
	if err != nil {
		return rbac.ErrInvalid
	}
	switch name {
	case "otel":
		var before, after otel.Config
		if json.Unmarshal(oldJSON, &before) != nil || json.Unmarshal(nextJSON, &after) != nil {
			return rbac.ErrInvalid
		}
		oldBindings, nextBindings := otelBindings(before.Profiles), otelBindings(after.Profiles)
		for credential, targets := range nextBindings {
			previous, known := oldBindings[credential]
			if !known {
				continue
			}
			for target := range targets {
				if !previous[target] {
					if err := requirePermissions(access, rbac.SecurityChangeCredentialDestination); err != nil {
						return err
					}
				}
			}
		}
	case "telemetry":
		var before, after telemetry.Config
		if json.Unmarshal(oldJSON, &before) != nil || json.Unmarshal(nextJSON, &after) != nil {
			return rbac.ErrInvalid
		}
		old, next := before.PushGateway, after.PushGateway
		if old == nil || next == nil || old.BasicAuth == nil || next.BasicAuth == nil {
			return nil
		}
		retained := retainedValues([]string{schemas.SecretVarAsString(old.BasicAuth.Password)}, []string{schemas.SecretVarAsString(next.BasicAuth.Password)})
		return credentialDestination(access, retained, schemas.SecretVarAsString(old.PushGatewayURL), schemas.SecretVarAsString(next.PushGatewayURL))
	}
	return nil
}

// hasDestinationCheck 与本文件的两处switch共同登记需要检查上报目标的内置插件。
func hasDestinationCheck(name string) bool {
	return name == "otel" || name == "telemetry"
}

// otelTarget 区分两种上报协议的实际目标；同一profile里的trace和metrics可发往不同地址。
type otelTarget struct {
	URL, Protocol, Signal, CA string
	Insecure                  bool
}

// otelBindings 按宿主的覆盖顺序合并请求头，再记录每个凭据对应的发送目标。
// 被覆盖的旧头仍是已保存凭据，但没有该目标的发送许可；移除覆盖时需要重新判定。
func otelBindings(profiles []*otel.Profile) map[string]map[otelTarget]bool {
	bindings := map[string]map[otelTarget]bool{}
	for _, profile := range profiles {
		if profile == nil {
			continue
		}
		for _, headers := range []map[string]string{profile.Headers, profile.TraceHeaders, profile.MetricsHeaders} {
			for _, value := range headers {
				if value != "" && bindings[value] == nil {
					bindings[value] = map[otelTarget]bool{}
				}
			}
		}
		for _, signal := range []struct {
			name, url string
			headers   map[string]string
		}{
			{"traces", schemas.SecretVarAsString(profile.CollectorURL), profile.TraceHeaders},
			{"metrics", schemas.SecretVarAsString(profile.MetricsEndpoint), profile.MetricsHeaders},
		} {
			if signal.url == "" {
				continue
			}
			headers := make(map[string]string, len(profile.Headers)+len(signal.headers))
			maps.Copy(headers, profile.Headers)
			maps.Copy(headers, signal.headers)
			target := otelTarget{signal.url, string(profile.Protocol), signal.name, profile.TLSCACert, profile.Insecure}
			for _, value := range headers {
				if value != "" {
					bindings[value][target] = true
				}
			}
		}
	}
	return bindings
}
