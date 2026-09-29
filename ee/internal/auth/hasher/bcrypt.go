// Package hasher 提供现有 bcrypt 算法与摘要格式检查，不依赖认证业务包。
package hasher

import (
	"regexp"

	"github.com/maximhq/bifrost/framework/encrypt"
	"golang.org/x/crypto/bcrypt"
)

// Bcrypt 沿用宿主默认 bcrypt 成本，不截断超过算法字节上限的密码。
type Bcrypt struct{}

// Hash 生成 bcrypt 摘要。
func (Bcrypt) Hash(password string) (string, error) { return encrypt.Hash(password) }

// Compare 比较密码；密码不匹配返回 false 而不是故障。
func (Bcrypt) Compare(hash, password string) (bool, error) {
	return encrypt.CompareHash(hash, password)
}

// bcryptHashPattern 同时检查完整盐与摘要，弥补 bcrypt.Cost 只解析版本和成本的不足。
var bcryptHashPattern = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)

// ValidHash 检查导入的摘要是否为支持的完整 bcrypt 格式。
func (Bcrypt) ValidHash(hash string) bool {
	_, err := bcrypt.Cost([]byte(hash))
	return err == nil && bcryptHashPattern.MatchString(hash)
}
