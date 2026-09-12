// 本文件是账号列表分页的表征测试：先于分页契约的结构整理落地，整理前后都必须原样通过。
package persistence

import (
	"context"
	"testing"
)

// TestAccountsPagination 证明：limit 生效、next_cursor 能继续、两页并集无重复无遗漏、最后一页 next_cursor 为空。
func TestAccountsPagination(t *testing.T) {
	s, _, _, admin := fixture(t)
	ctx := context.Background()
	want := map[string]bool{admin.Account.ID: true}
	for _, name := range []string{"alice", "bob", "carol"} {
		a, e := s.CreateAccount(ctx, admin.Principal, name, "")
		if e != nil {
			t.Fatal(e)
		}
		want[a.ID] = true
	}
	first, e := s.ListAccounts(ctx, admin.Principal, "", 2)
	if e != nil {
		t.Fatal(e)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %d items, cursor %q", len(first.Items), first.NextCursor)
	}
	second, e := s.ListAccounts(ctx, admin.Principal, first.NextCursor, 2)
	if e != nil {
		t.Fatal(e)
	}
	if len(second.Items) != 2 || second.NextCursor != "" {
		t.Fatalf("second page: %d items, cursor %q", len(second.Items), second.NextCursor)
	}
	seen := map[string]bool{}
	for _, a := range append(first.Items, second.Items...) {
		if seen[a.ID] {
			t.Fatalf("duplicate account %s across pages", a.ID)
		}
		seen[a.ID] = true
		if !want[a.ID] {
			t.Fatalf("unexpected account %s", a.ID)
		}
		if a.Username == "" || a.Status == "" {
			t.Fatalf("account %s missing fields: %+v", a.ID, a)
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("pages cover %d accounts, want %d", len(seen), len(want))
	}
}
