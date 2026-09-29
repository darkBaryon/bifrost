// 本文件验证算法兼容与完整摘要检查，不依赖认证存储。
package hasher

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBcryptCompatibility(t *testing.T) {
	algorithm := Bcrypt{}
	hash, err := algorithm.Hash("Password-1")
	if err != nil || !algorithm.ValidHash(hash) {
		t.Fatalf("hash generation failed: %v", err)
	}
	if cost, err := bcrypt.Cost([]byte(hash)); err != nil || cost != bcrypt.DefaultCost {
		t.Fatalf("default cost changed: %d %v", cost, err)
	}
	if matched, err := algorithm.Compare(hash, "Password-1"); err != nil || !matched {
		t.Fatalf("valid password rejected: %v", err)
	}
	if matched, err := algorithm.Compare(hash, "wrong"); err != nil || matched {
		t.Fatalf("mismatch should not be infrastructure failure: %v", err)
	}
	if _, err := algorithm.Hash(strings.Repeat("a", 73)); err == nil {
		t.Fatal("oversized password was truncated")
	}
	for _, invalid := range []string{"", hash[:29], hash + "x", hash[:59] + "!"} {
		if algorithm.ValidHash(invalid) {
			t.Fatal("malformed bcrypt hash accepted")
		}
	}
}
