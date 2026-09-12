// 本文件按自定义厂商的接入主机或显式映射识别官网厂商。
package pricing

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Provider 是同步需要的部署厂商信息；BaseURL 为空包含上游 NetworkConfig 为 nil 的情况。
type Provider struct {
	Name    string
	Custom  bool
	BaseURL string
}

// Match 保留原始厂商名，覆盖作用域不得归一化。
type Match struct{ Provider, VendorID string }

type logger interface {
	Info(string, ...interface{})
	Warn(string, ...interface{})
}

// ParseVendorMap 校验手工映射并将键转小写，与上游厂商落库规则一致。
func ParseVendorMap(raw string, file PriceFile) (map[string]string, error) {
	result := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	for _, item := range strings.Split(raw, ",") {
		parts := strings.Split(item, "=")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("invalid vendor mapping %q", item)
		}
		key, value := strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(parts[1])
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate vendor mapping %q", key)
		}
		result[key] = value
	}
	if err := validateVendorMap(result, file); err != nil {
		return nil, err
	}
	return result, nil
}

func validateVendorMap(mapping map[string]string, file PriceFile) error {
	ids := map[string]bool{}
	for _, v := range file.Vendors {
		ids[v.ID] = true
	}
	for _, id := range mapping {
		if !ids[id] {
			return fmt.Errorf("unknown vendor id %q", id)
		}
	}
	return nil
}

func matchVendors(providers []Provider, file PriceFile, mapping map[string]string, log logger) ([]Match, []string, error) {
	if err := validateVendorMap(mapping, file); err != nil {
		return nil, nil, err
	}
	custom := map[string]bool{}
	for _, p := range providers {
		if p.Custom {
			custom[strings.ToLower(p.Name)] = true
		}
	}
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !custom[key] {
			log.Warn("pricing: vendor map key %s is not a deployed custom provider; ignored", key)
		}
	}
	var matches []Match
	var unknown []string
	for _, p := range providers {
		if !p.Custom {
			continue
		}
		if id, ok := mapping[strings.ToLower(p.Name)]; ok {
			matches = append(matches, Match{p.Name, id})
			continue
		}
		host := endpointHost(p.BaseURL)
		var matched string
		for _, v := range file.Vendors {
			for _, allowed := range v.EndpointHosts {
				allowed = strings.ToLower(allowed)
				if host != "" && (host == allowed || strings.HasSuffix(host, "."+allowed)) {
					if matched != "" && matched != v.ID {
						return nil, nil, fmt.Errorf("provider %s host %s matches multiple vendors", p.Name, host)
					}
					matched = v.ID
					break
				}
			}
		}
		if matched == "" {
			unknown = append(unknown, p.Name)
			log.Info("未识别厂商 %s（主机 %s），可用 EE_PRICING_VENDOR_MAP 指定", p.Name, host)
		} else {
			matches = append(matches, Match{p.Name, matched})
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].Provider < matches[j].Provider })
	sort.Strings(unknown)
	return matches, unknown, nil
}

func endpointHost(raw string) string {
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}
