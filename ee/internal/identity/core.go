// 本文件保留账号和密码服务的依赖与分页辅助；认证判定复用 auth，会话变更显式调用 session。
package identity

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/session"
	"time"
	"unicode/utf8"
)

// Options 是已由部署配置解析的认证规则，不包含全局 Server 或 Config。
type Options struct {
	InitialPassword string
	SetupToken      string
	SessionTTL      time.Duration
}

// Validate 检查部署值是否在业务允许范围内；初始密码同样受 bcrypt 字节上限约束。
func (o Options) Validate() error {
	if o.InitialPassword == "" || !utf8.ValidString(o.InitialPassword) || len(o.InitialPassword) > MaxPasswordBytes {
		return ErrInvalid
	}
	if o.SessionTTL < session.MinTTL || o.SessionTTL > session.MaxTTL {
		return ErrInvalid
	}
	return nil
}

// core 暂供账号和密码服务共用；不再持有登录、会话或哈希并发状态。
type core struct {
	repo     Repository
	hasher   PasswordHasher
	policy   AccountPolicy
	options  Options
	sessions *session.Service
}

// AccountService 是账号线：初始化、旧管理员导入、建号、启停与列表。
type AccountService struct{ *core }

// PasswordService 是密码线：本人改密、管理员重置、离线恢复与密码事件查询。
type PasswordService struct{ *core }

// Services 暂时收纳账号和密码服务；auth 和 session 由 app 独立创建。
type Services struct {
	Account  *AccountService
	Password *PasswordService
}

// New 创建账号和密码服务；哈希器与会话服务由装配层共享注入。
func New(repo Repository, hasher PasswordHasher, options Options, policy AccountPolicy, sessions *session.Service) (*Services, error) {
	if repo == nil || hasher == nil || sessions == nil {
		return nil, ErrInvalid
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if policy == nil {
		policy = ChiefPolicy{}
	}
	c := &core{repo: repo, hasher: hasher, policy: policy, options: options, sessions: sessions}
	return &Services{Account: &AccountService{c}, Password: &PasswordService{c}}, nil
}

func now() time.Time { return time.Now().UTC() }

// randomID 生成 UUIDv4；crypto/rand 失败时进程无法安全工作，直接终止。
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (s *core) hash(password string) (string, error) {
	hash, err := s.hasher.Hash(password)
	return hash, SafeError(err)
}

func (s *core) compare(hash, password string) (bool, error) {
	ok, err := s.hasher.Compare(hash, password)
	return ok, SafeError(err)
}

// validPassword 执行新密码规则：合法 UTF-8、字符数与字节数在范围内、不等于部署初始密码；不做 trim。
func (s *core) validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= MinPasswordRunes &&
		len(password) <= MaxPasswordBytes && password != s.options.InitialPassword
}

// current 重新读取调用方声明的会话，并核对账号与版本未变；事务内外都用这一个实现。
func current(lookup func(string) (auth.Record, error), claimed session.Principal) (session.Principal, AccountRecord, error) {
	record, err := lookup(claimed.SessionID)
	if err != nil {
		if errors.Is(err, auth.ErrNotFound) || errors.Is(err, ErrNotFound) {
			err = ErrUnauthorized
		}
		return session.Principal{}, AccountRecord{}, SafeError(err)
	}
	actual, err := auth.RevalidateRecord(record, claimed, now())
	c := record.Account
	account := AccountRecord{Account: Account{ID: c.ID, Username: c.Username, DisplayName: c.DisplayName,
		Status: AccountStatus(c.Status), MustChangePassword: c.MustChangePassword, CreatedAt: c.CreatedAt, LastLoginAt: c.LastLoginAt},
		PasswordHash: c.PasswordHash, AuthVersion: c.AuthVersion, UpdatedAt: c.UpdatedAt}
	return actual, account, SafeError(err)
}

func (s *core) current(ctx context.Context, p session.Principal) (session.Principal, AccountRecord, error) {
	return current(func(id string) (auth.Record, error) { return s.repo.AuthByID(ctx, id) }, p)
}

// fullSession 拒绝仍需改密的受限会话；受限会话只能使用本人资料、改密和登出。
func fullSession(p session.Principal) error {
	if p.MustChangePassword {
		return ErrForbidden
	}
	return nil
}

// State 返回初始化状态与固定恢复锚点账号；经嵌入提升到两个服务，外部从任意一条调用等价。
func (s *core) State(ctx context.Context) (State, error) {
	v, err := s.repo.State(ctx)
	return v, SafeError(err)
}

// RequireAccountManager 供既有宿主入口使用：重验正常会话并应用 CreateAccounts 策略；同 State，两个服务上等价。
func (s *core) RequireAccountManager(ctx context.Context, p session.Principal) error {
	return s.requireAction(ctx, p, CreateAccounts, "")
}

// page 校验分页参数并解码游标：limit 必须在 1 到 MaxPageSize 之间，游标为空或可解码；不改写 limit。
func page(cursor string, limit int) (Cursor, error) {
	if limit < 1 || limit > MaxPageSize {
		return Cursor{}, ErrInvalid
	}
	var c Cursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > maxCursorBytes {
			return Cursor{}, ErrInvalid
		}
		if json.Unmarshal(raw, &c) != nil || c.At.IsZero() || !uuidPattern.MatchString(c.ID) {
			return Cursor{}, ErrInvalid
		}
	}
	return c, nil
}

func encodeCursor(at time.Time, id string) string {
	b, _ := json.Marshal(Cursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}
