// 本文件验证基础状态字段白名单、连接布尔语义和重启状态故障时的脱敏降级。
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/logstore"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/maximhq/bifrost/transports/bifrost-http/server"
)

type consoleConfigStore struct {
	configstore.ConfigStore
	restart *tables.RestartRequiredConfig
	err     error
	reads   int
}

func (s *consoleConfigStore) GetRestartRequiredConfig(context.Context) (*tables.RestartRequiredConfig, error) {
	s.reads++
	return s.restart, s.err
}

type consoleLogStore struct{ logstore.LogStore }
type consoleWarnings struct{ messages []string }

func (l *consoleWarnings) Warn(message string, args ...any) {
	l.messages = append(l.messages, fmt.Sprintf(message, args...))
}

func TestConsoleBootstrapProjectionAndRestartDegradation(t *testing.T) {
	a, admin, _ := testAdapter(t)
	r := router.New()
	a.RegisterSessionRoutes(r, a.APIMiddleware())
	for _, tc := range []struct {
		name, env string
		config    bool
		logs      bool
		restart   *tables.RestartRequiredConfig
		failure   bool
		logger    bool
	}{
		{name: "no stores"},
		{name: "logs only", logs: true},
		{name: "database only", config: true},
		{name: "restart false", env: "test", config: true, logs: true, restart: &tables.RestartRequiredConfig{Required: false, Reason: "secret path"}},
		{name: "restart true", env: "staging", config: true, logs: true, restart: &tables.RestartRequiredConfig{Required: true, Reason: "secret path"}},
		{name: "restart failure", config: true, logs: true, restart: &tables.RestartRequiredConfig{Required: true, Reason: "secret path"}, failure: true, logger: true},
		{name: "restart failure without logger", config: true, failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &lib.Config{EnvLabel: tc.env}
			store := &consoleConfigStore{restart: tc.restart}
			if tc.failure {
				store.err = errors.New("database-password-secret")
			}
			if tc.config {
				cfg.ConfigStore = store
			}
			if tc.logs {
				cfg.LogsStore = &consoleLogStore{}
			}
			log := &consoleWarnings{}
			a.host = &server.BifrostHTTPServer{Config: cfg}
			a.consoleLog = nil
			if tc.logger {
				WithConsoleLogger(log)(a)
			}
			c := consoleRequest(r.Handler, "POST", "/api/console/bootstrap", "{}", admin.Token, a.http.Origin(), nil)
			assertConsoleStatus(t, c, 200)
			var got map[string]any
			if err := json.Unmarshal(c.Response.Body(), &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"is_db_connected": tc.config, "is_logs_connected": tc.logs, "env_label": nil}
			if tc.env != "" {
				want["env_label"] = tc.env
			}
			if tc.restart != nil && !tc.failure {
				want["restart_required"] = map[string]any{"required": tc.restart.Required}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("whitelist mismatch: got=%v want=%v", got, want)
			}
			expectedReads := 0
			if tc.config {
				expectedReads = 1
			}
			if store.reads != expectedReads {
				t.Fatalf("restart reads=%d want=%d", store.reads, expectedReads)
			}
			if tc.failure && tc.logger {
				wantLog := "EE console bootstrap reason=restart_read_failed request_id=" + string(c.Response.Header.Peek("X-Request-ID"))
				if !reflect.DeepEqual(log.messages, []string{wantLog}) {
					t.Fatalf("diagnostic=%v", log.messages)
				}
			} else if len(log.messages) != 0 {
				t.Fatal("unexpected diagnostic")
			}
			if strings.Contains(string(c.Response.Body()), "secret") || strings.Contains(strings.Join(log.messages, ""), "secret") {
				t.Fatal("restart metadata leaked")
			}
		})
	}
}
