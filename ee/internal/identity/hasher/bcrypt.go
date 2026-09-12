// Package hasher 把上游 framework/encrypt 的 bcrypt 适配为 identity.PasswordHasher；不涉及持久化或宿主对象。
package hasher

import (
	"regexp"

	"github.com/darkBaryon/bifrost/ee/internal/identity"
	"github.com/maximhq/bifrost/framework/encrypt"
	"golang.org/x/crypto/bcrypt"
)

// Bcrypt 复用宿主 bcrypt，超过算法上限的密码由业务层拒绝而不是截断。
type Bcrypt struct{}

func (Bcrypt) Hash(p string) (string, error) { return encrypt.Hash(p) }

func (Bcrypt) Compare(h, p string) (bool, error) { return encrypt.CompareHash(h, p) }

// bcryptHashPattern 补充 bcrypt.Cost 只解析版本和 cost 的不足，要求完整的 22 字节盐与 31 字节摘要。
var bcryptHashPattern = regexp.MustCompile(`^\$2[aby]\$[0-9]{2}\$[./A-Za-z0-9]{53}$`)

// ValidHash 判断字符串是否为完整的 bcrypt 哈希，用于旧管理员凭据导入。
func (Bcrypt) ValidHash(h string) bool {
	_, err := bcrypt.Cost([]byte(h))
	return err == nil && bcryptHashPattern.MatchString(h)
}

var _ identity.PasswordHasher = Bcrypt{}
