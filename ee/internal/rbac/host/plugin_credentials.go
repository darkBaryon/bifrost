// 本文件检查内置插件携带旧凭据更换上报地址的操作；不影响插件普通参数更新。
package host

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
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
		headers := func(p *otel.Profile) []string {
			out := headerValues(p.Headers)
			out = append(out, headerValues(p.TraceHeaders)...)
			return append(out, headerValues(p.MetricsHeaders)...)
		}
		target := func(p *otel.Profile) any {
			return struct {
				Collector, Metrics, Protocol, Type, CA string
				Insecure                               bool
			}{secretText(p.CollectorURL), secretText(p.MetricsEndpoint), string(p.Protocol), string(p.TraceType), p.TLSCACert, p.Insecure}
		}
		// 相同凭据可能本来就用于多个目标。重排或原样保存不应被拒绝，新增的目标仍须授权。
		for _, next := range after.Profiles {
			if next == nil {
				continue
			}
			for _, value := range headers(next) {
				if value == "" {
					continue
				}
				known, paired := false, false
				for _, old := range before.Profiles {
					if old == nil || !slices.Contains(headers(old), value) {
						continue
					}
					known = true
					paired = paired || reflect.DeepEqual(target(old), target(next))
				}
				if known && !paired {
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
		retained := retainedValues([]string{secretText(old.BasicAuth.Password)}, []string{secretText(next.BasicAuth.Password)})
		return credentialDestination(access, retained, secretText(old.PushGatewayURL), secretText(next.PushGatewayURL))
	}
	return nil
}
