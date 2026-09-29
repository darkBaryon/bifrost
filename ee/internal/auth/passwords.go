// 本文件限制共享密码算法的并发数量；账号修改和登录共同使用同一个实例。
package auth

// maxConcurrentHashes 沿用认证方案的每进程 bcrypt 并行上限，满额立即拒绝，不排队。
const maxConcurrentHashes = 4

// Passwords 为 Hash 与 Compare 共用计算槽；不持有账号、会话或权限服务。
type Passwords struct {
	hasher PasswordHasher
	slots  chan struct{}
}

// NewPasswords 创建供所有密码操作共享的受限算法；nil 算法在使用时返回不可用。
func NewPasswords(hasher PasswordHasher) *Passwords {
	return &Passwords{hasher: hasher, slots: make(chan struct{}, maxConcurrentHashes)}
}

func (p *Passwords) compute(fn func() error) error {
	if p == nil || p.hasher == nil {
		return ErrUnavailable
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return ErrLimited
	}
	return SafeError(fn())
}

// Hash 在共享并发限额内生成密码摘要。
func (p *Passwords) Hash(password string) (hash string, err error) {
	err = p.compute(func() error {
		var failure error
		hash, failure = p.hasher.Hash(password)
		return failure
	})
	return hash, err
}

// Compare 在与 Hash 相同的并发限额内校验密码。
func (p *Passwords) Compare(hash, password string) (matched bool, err error) {
	err = p.compute(func() error {
		var failure error
		matched, failure = p.hasher.Compare(hash, password)
		return failure
	})
	return matched, err
}

// ValidHash 只检查摘要格式，不执行昂贵的密码计算，因此不占用计算槽。
func (p *Passwords) ValidHash(hash string) bool {
	return p != nil && p.hasher != nil && p.hasher.ValidHash(hash)
}

var _ PasswordHasher = (*Passwords)(nil)
