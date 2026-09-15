// 本文件检查角色API严格输入，避免数值ID/重复键被宽松JSON解码接受。
package rbachttp

import (
	"encoding/json"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
	"github.com/valyala/fasthttp"
)

func TestStrictRoleID(t *testing.T) {
	for _, raw := range []string{`"1"`, `1.0`, `1e0`, `-1`, `0`, `null`, `9007199254740992`, `[]`, `true`} {
		if _, e := roleID(json.RawMessage(raw)); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	for _, raw := range []string{`1`, `9007199254740991`} {
		if _, e := roleID(json.RawMessage(raw)); e != nil {
			t.Errorf("rejected %s", raw)
		}
	}
}

func TestStrictEnvelope(t *testing.T) {
	for _, raw := range []string{`{"role_id":1,"role_id":2}`, `{"Role_ID":1}`, `{"role_id":null}`, `{"role_id":1} {}`, `[]`, `{}`, `{"role_id":1,"unknown":0}`} {
		c := &fasthttp.RequestCtx{}
		c.Request.Header.SetContentType("application/json")
		c.Request.SetBodyString(raw)
		if _, e := decode(c, []string{"role_id"}, []string{"role_id"}); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestMatrixDoesNotImplyViewOrReveal(t *testing.T) {
	m := matrix([]rbac.Grant{{Code: rbac.LogsManage}, {Code: rbac.GovernanceView}, {Code: rbac.PluginsLoadNative}})
	if !m["Logs"]["Update"] || m["Logs"]["View"] || m["Logs"]["Reveal"] || m["MCPLogs"]["Download"] {
		t.Fatal("Manage expanded to read/reveal")
	}
	if !m["Teams"]["Read"] || !m["Customers"]["View"] || m["Teams"]["Create"] {
		t.Fatal("governance resource expansion")
	}
	if !m["Plugins"]["LoadNative"] || m["Plugins"]["Update"] || m["Inference"]["View"] {
		t.Fatal("sensitive permission expanded")
	}
}
