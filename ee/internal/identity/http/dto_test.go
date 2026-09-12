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
	want := []string{"id", "username", "display_name", "status", "must_change_password"}
	if len(got) != len(want) {
		t.Fatalf("account DTO has %d keys, want %d: %s", len(got), len(want), b)
	}
	for _, k := range want {
		if _, ok := got[k]; !ok {
			t.Fatalf("account DTO missing key %q: %s", k, b)
		}
	}
}
