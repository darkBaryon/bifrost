// 本文件验证开发护栏必须显式选择场景，关闭或错误配置不会访问宿主插件链。
package app

import (
	"context"
	"strings"
	"testing"
)

func TestDevelopmentGuardrailsOptIn(t *testing.T) {
	for _, mode := range []string{"", "true", "input-blok"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(envGuardrailsFake, mode)
			err := assembleGuardrails(context.Background(), nil, nil)
			if mode == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), envGuardrailsFake) {
				t.Fatalf("invalid development mode accepted: %v", err)
			}
		})
	}
}
