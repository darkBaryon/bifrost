// 本文件检查日志查询需要的额外权限，并在返回时隐藏账号无权查看的正文。
package host

import (
	"encoding/json"
	"strings"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/valyala/fasthttp"
)

var logSummaryFields = []string{"id", "inc_number", "parent_request_id", "timestamp", "object", "provider", "model", "alias", "status", "selected_key_id", "selected_key_name", "virtual_key_id", "virtual_key_name", "team_id", "team_name", "customer_id", "customer_name", "routing_rule_id", "routing_rule_name", "latency", "time_to_first_token", "token_usage", "cost", "cost_breakdown", "number_of_retries", "fallback_index", "is_large_payload_request", "is_large_payload_response", "content_hidden", "child_count", "children_cost", "children_tokens"}

var mcpSummaryFields = []string{"id", "timestamp", "request_id", "client_id", "client_name", "server_label", "tool_name", "status", "latency", "cost", "token_usage"}

// projectLogs 按已知日志结构保留计量信息，移除输入、输出、错误原文等正文。
func projectLogs(value any, pattern string) (any, error) {
	if isUserAgentMapping(pattern) {
		return value, nil
	}
	fields := logSummaryFields
	if strings.Contains(pattern, "mcp-logs") {
		fields = mcpSummaryFields
	}
	// 只把已声明的日志行位置当成日志，聚合对象使用下面的安全外层投影。
	var row func(any) any
	row = func(value any) any {
		switch v := value.(type) {
		case []any:
			out := make([]any, 0, len(v))
			for _, child := range v {
				out = append(out, row(child))
			}
			return out
		case map[string]any:
			return keep(v, fields)
		}
		return value
	}
	switch pattern {
	case "/api/logs/{id}", "/api/mcp-logs/{id}":
		if v, ok := value.(map[string]any); ok {
			if nested, ok := v["log"]; ok {
				v["log"] = row(nested)
				return v, nil
			}
		}
		return row(value), nil
	case "/api/logs", "/api/mcp-logs":
		if v, ok := value.(map[string]any); ok {
			for _, key := range []string{"logs", "items"} {
				if nested, ok := v[key]; ok {
					v[key] = row(nested)
				}
			}
			delete(v, "metadata")
			return v, nil
		}
		return row(value), nil
	}
	if v, ok := value.(map[string]any); ok && pattern == "/api/logs/sessions/{session_id}" {
		out := keep(v, logOperationFields)
		if logs, exists := v["logs"]; exists {
			out["logs"] = row(logs)
		}
		return out, nil
	}
	return projectLogAggregate(value, pattern)
}

// aggregateDTO 选择这个统计接口对应的响应类型；没有登记的结构不能直接返回。
func aggregateDTO(pattern string) any {
	switch pattern {
	case "/api/logs/stats":
		return &logstore.SearchStats{}
	case "/api/logs/dashboard":
		return &logstore.DashboardResult{}
	case "/api/logs/histogram":
		return &logstore.HistogramResult{}
	case "/api/logs/histogram/cost":
		return &logstore.CostHistogramResult{}
	case "/api/logs/histogram/tokens":
		return &logstore.TokenHistogramResult{}
	case "/api/logs/histogram/models":
		return &logstore.ModelHistogramResult{}
	case "/api/logs/histogram/latency":
		return &logstore.LatencyHistogramResult{}
	case "/api/logs/histogram/throughput":
		return &logstore.ThroughputHistogramResult{}
	case "/api/logs/histogram/cost/by-provider":
		return &logstore.ProviderCostHistogramResult{}
	case "/api/logs/histogram/tokens/by-provider":
		return &logstore.ProviderTokenHistogramResult{}
	case "/api/logs/histogram/latency/by-provider":
		return &logstore.ProviderLatencyHistogramResult{}
	case "/api/logs/histogram/throughput/by-provider":
		return &logstore.ProviderThroughputHistogramResult{}
	case "/api/logs/histogram/cost/by-dimension":
		return &logstore.DimensionCostHistogramResult{}
	case "/api/logs/histogram/tokens/by-dimension":
		return &logstore.DimensionTokenHistogramResult{}
	case "/api/logs/histogram/latency/by-dimension":
		return &logstore.DimensionLatencyHistogramResult{}
	case "/api/logs/rankings":
		return &logstore.ModelRankingResult{}
	case "/api/logs/rankings/by-dimension":
		return &logstore.DimensionRankingResult{}
	case "/api/logs/sessions/{session_id}/summary":
		return &logstore.SessionSummaryResult{}
	case "/api/mcp-logs/stats":
		return &logstore.MCPToolLogStats{}
	case "/api/mcp-logs/histogram":
		return &logstore.MCPHistogramResult{}
	case "/api/mcp-logs/histogram/cost":
		return &logstore.MCPCostHistogramResult{}
	case "/api/mcp-logs/histogram/top-tools":
		return &logstore.MCPTopToolsResult{}
	}
	return nil
}

// typedValue 按明确的响应类型重新解析，只留下这个类型声明的字段。
func typedValue(value, typed any) (any, error) {
	if _, ok := value.(map[string]any); !ok {
		return nil, rbac.ErrUnavailable
	}
	raw, e := json.Marshal(value)
	if e != nil || json.Unmarshal(raw, typed) != nil {
		return nil, rbac.ErrUnavailable
	}
	raw, e = json.Marshal(typed)
	if e != nil {
		return nil, rbac.ErrUnavailable
	}
	var out any
	if json.Unmarshal(raw, &out) != nil {
		return nil, rbac.ErrUnavailable
	}
	return out, nil
}

var filterFields = []string{"models", "aliases", "selected_keys", "virtual_keys", "routing_rules", "routing_engines", "stop_reasons", "apps", "teams", "customers", "users", "business_units", "projects", "tool_names", "server_labels"}

var logOperationFields = []string{"id", "status", "total", "processed", "updated", "skipped", "unpriceable", "started_at", "updated_at", "success", "deleted_count", "dropped_requests", "session_id", "count", "returned_count", "has_more", "pagination", "stats", "has_logs"}

// projectLogAggregate 保留统计数值和允许展示的维度，不把未知字段带回前端。
func projectLogAggregate(value any, pattern string) (any, error) {
	if typed := aggregateDTO(pattern); typed != nil {
		return typedValue(value, typed)
	}
	if v, ok := value.(map[string]any); ok {
		if pattern == "/api/logs/filterdata" || pattern == "/api/mcp-logs/filterdata" {
			return keep(v, filterFields), nil
		}
		return keep(v, logOperationFields), nil
	}
	return nil, rbac.ErrUnavailable
}

// checkLogQuery 查询正文、敏感维度或全量结果时，检查额外的正文或导出权限。
func checkLogQuery(c *fasthttp.RequestCtx, r requestAccess) error {
	if isUserAgentMapping(r.Route.Pattern) {
		return nil
	}
	bad, sensitive := false, false
	seen := map[string]bool{}
	c.QueryArgs().VisitAll(func(k, v []byte) {
		key, value := string(k), string(v)
		if seen[key] {
			bad = true
		}
		seen[key] = true
		switch key {
		case "content_search", "metadata_filters", "metadata_key", "user_agents":
			sensitive = sensitive || value != ""
		case "dimension", "dimensions", "group_by":
			for _, part := range strings.Split(value, ",") {
				if strings.Contains(part, "metadata") || part == "user_agent" || part == "user_agents" {
					sensitive = true
				}
			}
		default:
			if strings.HasPrefix(key, "metadata_") || strings.HasPrefix(key, "metadata[") || strings.HasPrefix(key, "metadata.") {
				sensitive = true
			}
		}
	})
	if bad {
		return rbac.ErrInvalid
	}
	if sensitive && !r.Access.Allows(rbac.LogsRevealContent) {
		return rbac.ErrForbidden
	}
	if r.Route.Pattern == "/api/logs/rankings" || r.Route.Pattern == "/api/logs/rankings/by-dimension" || r.Route.Pattern == "/api/logs/dashboard" {
		all, e := queryBool(c, "all")
		if e != nil {
			return e
		}
		if all {
			return requirePermissions(r.Access, rbac.LogsExport)
		}
	}
	return nil
}

func isUserAgentMapping(pattern string) bool {
	return strings.Contains(pattern, "user-agent-mappings")
}
