// 本文件验证长期消息回调每次重验权限，并拒绝未知消息与不透明载荷。
package host

import (
	"context"
	"errors"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/rbac"
)

func TestMessageFilterRevalidatesAndUnderstandsEnvelope(t *testing.T) {
	repo := &routeRepository{codes: []rbac.Permission{rbac.NotificationsView}}
	filter := NewAdapter(rbac.New(repo), nil, nil).messageFilter(rbac.Subject{AccountID: "member", SessionID: "session", AuthVersion: 1})
	tests := []struct {
		body    string
		allowed bool
		failure bool
	}{
		{`{"type":"heartbeat"}`, true, false},
		{`{"type":"notification","data":{"id":"n","audience":"all","title":"public notification"}}`, true, false},
		{`{"type":"notification","data":{"id":"n","audience":"roles","role_ids":[4]}}`, true, false},
		{`{"type":"notification","data":{"id":"n","audience":"roles","role_ids":[5]}}`, false, false},
		{`{"type":"future-content","data":{"payload":"unclassified"}}`, false, false},
		{`{"type":"notification","data":{"id":"n","audience":"all","future_payload":"unclassified"}}`, false, true},
		{`{"type":"heartbeat","data":{"payload":"unclassified"}}`, false, true},
		{`{"type":"heartbeat","extra":"unclassified"}`, false, true},
		{`{"type":"notification","data":[]}`, false, true},
	}
	for _, tt := range tests {
		allowed, e := filter(context.Background(), []byte(tt.body))
		if allowed != tt.allowed || (e != nil) != tt.failure {
			t.Fatalf("message %s: allowed=%v err=%v", tt.body, allowed, e)
		}
	}
	repo.codes = nil
	if ok, e := filter(context.Background(), []byte(`{"type":"heartbeat"}`)); ok || !errors.Is(e, rbac.ErrForbidden) {
		t.Fatal("reused stale permissions", ok, e)
	}
	repo.codes = []rbac.Permission{rbac.NotificationsView}
	repo.failure = rbac.ErrUnavailable
	if ok, e := filter(context.Background(), []byte(`{"type":"heartbeat"}`)); ok || !errors.Is(e, rbac.ErrUnavailable) {
		t.Fatal("storage failure allowed delivery", ok, e)
	}
}
