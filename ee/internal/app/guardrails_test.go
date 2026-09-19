// 本文件验证开发护栏必须显式选择场景，关闭或错误配置不会访问宿主插件链。
package app

import (
	"strings"
	"testing"
)

func TestDevelopmentGuardrailsOptIn(t *testing.T) {
	for _, mode := range []string{"true", "input-blok"} {
		t.Run(mode, func(t *testing.T) {
			if err := validateFakeMode(mode); err == nil || !strings.Contains(err.Error(), envGuardrailsFake) {
				t.Fatalf("invalid development mode accepted: %v", err)
			}
		})
	}
	for _, mode := range []string{fakeInputBlock, fakeInputObserve, fakeOutputBlock} {
		if err := validateFakeMode(mode); err != nil {
			t.Fatal(err)
		}
		if _, err := fakeChecker(mode); err != nil {
			t.Fatal(err)
		}
	}
}
