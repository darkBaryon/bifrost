// 本文件测试权限目录和非法翻页标记，直接调用业务代码，不依赖 HTTP 层拦截错误输入。
package rbac

import (
	"encoding/base64"
	"testing"
)

func TestCursorRejectsAmbiguousIDs(t *testing.T) {
	for _, raw := range []string{`{"before_id":"4"}`, `{"before_id":4.0}`, `{"before_id":4e0}`, `{"before_id":0}`, `{"before_id":-1}`, `{"before_id":null}`, `{"before_id":9007199254740992}`, `{"before_id":4,"before_id":3}`, `{"before_id":4,"other":1}`, `{"before_id":4} {}`, `[]`} {
		if _, e := parseCursor(base64.RawURLEncoding.EncodeToString([]byte(raw))); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	if got, e := parseCursor(cursor(4)); e != nil || got != 4 {
		t.Fatal(got, e)
	}
}

func TestPresetCatalogue(t *testing.T) {
	codes := Permissions()
	if len(codes) != 29 {
		t.Fatal(len(codes))
	}
	seen := map[Permission]bool{}
	for _, p := range codes {
		if seen[p] {
			t.Fatal("duplicate", p)
		}
		seen[p] = true
	}
	roles := PresetRoles()
	for _, p := range roles[1].PermissionCodes {
		if p == UsersView || p == UsersManage {
			t.Fatal("developer got Users")
		}
	}
	catalogue := Catalogue()
	catalogue[0].Permissions[0].Code = "changed"
	if Catalogue()[0].Permissions[0].Code != ModelProviderView {
		t.Fatal("catalogue shared memory")
	}
}
