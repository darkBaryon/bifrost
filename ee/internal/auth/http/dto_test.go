// 本文件钉住对外字段，业务类型新增字段不能泄露到响应。
package authhttp

import (
	"encoding/json"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
)

func TestResponseKeys(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
		keys  []string
	}{
		{"account", toAccountDTO(auth.User{}), []string{"id", "username", "display_name", "status", "must_change_password", "last_login_at"}},
		{"status", statusResponse{}, []string{"initialized", "auth_type", "is_auth_enabled", "has_valid_token", "has_valid_session", "must_change_password"}},
		{"login", loginResponse{}, []string{"message", "account", "must_change_password", "expires_at"}},
		{"ticket", ticketResponse{}, []string{"ticket", "expires_in"}},
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
				t.Fatalf("unexpected fields: %s", data)
			}
			for _, key := range tc.keys {
				if _, ok := fields[key]; !ok {
					t.Fatalf("missing field %s: %s", key, data)
				}
			}
		})
	}
}
