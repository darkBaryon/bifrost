// 本文件钉住账号响应的 JSON 键集合：业务类型 Account 增减字段不得改变接口输出。
package identityhttp

import (
	"encoding/json"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
)

func TestAccountDTOKeys(t *testing.T) {
	b, err := json.Marshal(toAccountDTO(identity.Account{}))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	want := []string{"id", "username", "display_name", "status", "must_change_password", "last_login_at"}
	if len(got) != len(want) {
		t.Fatalf("account DTO has %d keys, want %d: %s", len(got), len(want), b)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Fatalf("account DTO missing key %q: %s", k, b)
		}
	}
}

// TestStatusResponseKeys 保持匿名状态接口只返回登录状态，不混入账号资料。
func TestStatusResponseKeys(t *testing.T) {
	data, err := json.Marshal(statusResponse{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	want := []string{"initialized", "auth_type", "is_auth_enabled", "has_valid_token", "has_valid_session", "must_change_password"}
	if len(fields) != len(want) {
		t.Fatalf("unexpected status fields: %s", data)
	}
	for _, name := range want {
		if _, ok := fields[name]; !ok {
			t.Fatal("missing status field", name)
		}
	}
}
