package identity

import (
	"errors"
	"fmt"
	"testing"

	auth "github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/darkBaryon/bifrost/ee/internal/rbac"
)

// 两侧错误枚举独立定义，验证包装错误与未映射值不会穿透边界。
func TestErrorTranslation(t *testing.T) {
	for _, pair := range errorPairs {
		if got := IdentityError(fmt.Errorf("wrapped: %w", pair.permission)); !errors.Is(got, pair.identity) {
			t.Fatalf("identity %v: %v", pair.permission, got)
		}
		if got := PermissionError(fmt.Errorf("wrapped: %w", pair.identity)); !errors.Is(got, pair.permission) {
			t.Fatalf("permission %v: %v", pair.identity, got)
		}
	}
	for _, err := range []error{auth.ErrLimited, auth.Error("future_error"), errors.New("SECRET")} {
		if got := PermissionError(err); got != rbac.ErrUnavailable {
			t.Fatalf("unknown identity error: %v", got)
		}
	}
	if IdentityError(rbac.Error("future_error")) != auth.ErrUnavailable {
		t.Fatal("unknown permission error escaped")
	}
	if IdentityError(nil) != nil || PermissionError(nil) != nil {
		t.Fatal("nil changed")
	}
}
