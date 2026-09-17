// 本文件按真实日志DTO验证安全计量和维度保留，避免空对象也通过秘密扫描。
package host

import (
	"strings"
	"testing"
)

func TestLogAggregateStructuresPreserveMeasurements(t *testing.T) {
	tests := []struct {
		path, body string
		want       map[string]string
	}{
		{"/api/logs/stats", `{"total_requests":8,"total_cost":2.5,"future_payload":"SECRET"}`, map[string]string{"total_requests": "8", "total_cost": "2.5"}},
		{"/api/logs/dashboard", `{"meta":{"generated_at":"2026-09-13T00:00:00Z","bucket_size_seconds":60},"overview":{"stats":{"total_requests":8}},"future_payload":"SECRET"}`, map[string]string{"meta.bucket_size_seconds": "60", "overview.stats.total_requests": "8"}},
		{"/api/logs/histogram", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","count":8}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.count": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/cost", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","total_cost":8,"by_model":{"model":8}}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.total_cost": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/tokens", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","total_tokens":8}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.total_tokens": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/models", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_model":{"model":{"total":8}}}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_model.model.total": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/latency", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","avg_latency":8}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.avg_latency": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/throughput", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","tokens_per_second":8}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.tokens_per_second": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/cost/by-provider", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_provider":{"openai":8}}],"bucket_size_seconds":60,"providers":["openai"],"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_provider.openai": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/tokens/by-provider", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_provider":{"openai":{"total_tokens":8}}}],"bucket_size_seconds":60,"providers":["openai"],"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_provider.openai.total_tokens": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/latency/by-provider", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_provider":{"openai":{"avg_latency":8}}}],"bucket_size_seconds":60,"providers":["openai"],"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_provider.openai.avg_latency": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/throughput/by-provider", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_provider":{"openai":{"tokens_per_second":8}}}],"bucket_size_seconds":60,"providers":["openai"],"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_provider.openai.tokens_per_second": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/cost/by-dimension", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_dimension":{"team":8}}],"bucket_size_seconds":60,"dimension":"team_id","dimension_values":["team"],"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_dimension.team": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/tokens/by-dimension", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_dimension":{"team":{"total_tokens":8}}}],"bucket_size_seconds":60,"dimension":"team_id","dimension_values":["team"],"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_dimension.team.total_tokens": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/histogram/latency/by-dimension", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","by_dimension":{"team":{"avg_latency":8}}}],"bucket_size_seconds":60,"dimension":"team_id","dimension_values":["team"],"future_payload":"SECRET"}`, map[string]string{"buckets.0.by_dimension.team.avg_latency": "8", "buckets.0.timestamp": "2026-09-13T00:00:00Z", "bucket_size_seconds": "60"}},
		{"/api/logs/rankings", `{"rankings":[{"model":"model","provider":"openai","total_requests":8,"trend":{"has_previous_period":true,"requests_trend":2}}],"future_payload":"SECRET"}`, map[string]string{"rankings.0.model": "model", "rankings.0.total_requests": "8", "rankings.0.trend.requests_trend": "2"}},
		{"/api/logs/rankings/by-dimension", `{"dimension":"team_id","rankings":[{"id":"team","total_requests":8}],"future_payload":"SECRET"}`, map[string]string{"dimension": "team_id", "rankings.0.total_requests": "8"}},
		{"/api/logs/sessions/{session_id}/summary", `{"session_id":"session","count":8,"total_tokens":9,"duration_ms":12,"future_payload":"SECRET"}`, map[string]string{"count": "8", "total_tokens": "9", "duration_ms": "12"}},
		{"/api/mcp-logs/stats", `{"total_executions":8,"total_cost":3,"future_payload":"SECRET"}`, map[string]string{"total_executions": "8", "total_cost": "3"}},
		{"/api/mcp-logs/histogram", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","count":8}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.count": "8", "bucket_size_seconds": "60"}},
		{"/api/mcp-logs/histogram/cost", `{"buckets":[{"timestamp":"2026-09-13T00:00:00Z","total_cost":8}],"bucket_size_seconds":60,"future_payload":"SECRET"}`, map[string]string{"buckets.0.total_cost": "8", "bucket_size_seconds": "60"}},
		{"/api/mcp-logs/histogram/top-tools", `{"tools":[{"tool_name":"lookup","count":8,"cost":2}],"future_payload":"SECRET"}`, map[string]string{"tools.0.tool_name": "lookup", "tools.0.count": "8"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			body := projected(t, test.path, projectionLogsContent, test.body)
			assertProjectionFields(t, body, test.want)
			if strings.Contains(body, "SECRET") {
				t.Fatal("unknown aggregate payload survived")
			}
		})
	}
}

func TestLogEnvelopeStructuresPreserveMetadata(t *testing.T) {
	tests := []struct {
		path, body string
		want       map[string]string
	}{
		{"/api/logs/filterdata", `{"models":["model"],"teams":["team"],"metadata_keys":["SECRET"],"user_agents":["SECRET"]}`, map[string]string{"models.0": "model", "teams.0": "team"}},
		{"/api/mcp-logs/filterdata", `{"tool_names":["lookup"],"server_labels":["server"],"metadata":{"token":"SECRET"}}`, map[string]string{"tool_names.0": "lookup", "server_labels.0": "server"}},
		{"/api/logs/sessions/{session_id}", `{"session_id":"session","count":8,"pagination":{"limit":20},"stats":{"total_cost":2},"logs":[{"id":"log","cost":2,"input":"SECRET"}]}`, map[string]string{"count": "8", "pagination.limit": "20", "stats.total_cost": "2", "logs.0.id": "log", "logs.0.cost": "2"}},
		{"/api/logs/recalculate-cost/status", `{"status":"running","processed":8,"updated":3,"error":"SECRET"}`, map[string]string{"status": "running", "processed": "8", "updated": "3"}},
		{"/api/logs/dropped", `{"dropped_requests":8,"error":"SECRET"}`, map[string]string{"dropped_requests": "8"}},
		{"/api/logs/user-agent-mappings", `{"mappings":[{"pattern":"Mozilla.*","label":"browser"}]}`, map[string]string{"mappings.0.pattern": "Mozilla.*", "mappings.0.label": "browser"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			body := projected(t, test.path, projectionLogsContent, test.body)
			assertProjectionFields(t, body, test.want)
			if strings.Contains(body, "SECRET") {
				t.Fatal("log envelope leaked")
			}
		})
	}
}
