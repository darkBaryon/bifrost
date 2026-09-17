// 本文件钉住账号与状态响应的 JSON 键集合，防止业务类型变化意外改变接口输出。
package identityhttp

import (
	"encoding/json"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
)

func TestResponseKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		keys  []string
	}{
		{"account", toAccountDTO(identity.Account{}), []string{"id", "username", "display_name", "status", "must_change_password", "last_login_at"}},
		{"status", statusResponse{}, []string{"initialized", "auth_type", "is_auth_enabled", "has_valid_token", "has_valid_session", "must_change_password"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != len(tc.keys) {
				t.Fatalf("unexpected response fields: %s", data)
			}
			for _, name := range tc.keys {
				if _, ok := fields[name]; !ok {
					t.Fatal("missing response field", name)
				}
			}
		})
	}
}
