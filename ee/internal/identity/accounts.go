// 本文件实现账号操作：旧账号导入、初始化、建号、启停与分页列表。
package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"
)

// BootstrapLegacy 在未初始化时把旧管理员凭据导入为主管理员，用户名与哈希字节原样保留；已初始化则忽略输入。
func (s *Service) BootstrapLegacy(ctx context.Context, username, passwordHash string) error {
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		if tx.State().Initialized {
			return nil
		}
		if username == "" || !s.hasher.ValidHash(passwordHash) {
			return ErrInvalid
		}
		c := AccountRecord{Account: Account{ID: randomID(), Username: username, Status: StatusActive, CreatedAt: now()},
			PasswordHash: passwordHash, AuthVersion: 1, UpdatedAt: now()}
		if err := tx.InsertAccount(c); err != nil {
			return err
		}
		return tx.SaveState(State{Initialized: true, ChiefAccountID: c.ID})
	}))
}

// Initialize 用部署初始化密钥创建主管理员；只允许成功一次，不签发会话。
func (s *Service) Initialize(ctx context.Context, setupToken, username, password string) (Account, error) {
	state, err := s.State(ctx)
	if err != nil {
		return Account{}, err
	}
	if state.Initialized {
		return Account{}, ErrConflict
	}
	provided, expected := sha256.Sum256([]byte(setupToken)), sha256.Sum256([]byte(s.options.SetupToken))
	if s.options.SetupToken == "" || subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
		return Account{}, ErrForbidden
	}
	if !usernamePattern.MatchString(username) || !s.validPassword(password) {
		return Account{}, ErrInvalid
	}
	hash, err := s.hash(password)
	if err != nil {
		return Account{}, err
	}
	var out Account
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		// 两个节点同时初始化时，后进入事务的一方在这里看到已初始化。
		if tx.State().Initialized {
			return ErrConflict
		}
		c := AccountRecord{Account: Account{ID: randomID(), Username: username, Status: StatusActive, CreatedAt: now()},
			PasswordHash: hash, AuthVersion: 1, UpdatedAt: now()}
		if err := tx.InsertAccount(c); err != nil {
			return err
		}
		if err := tx.SaveState(State{Initialized: true, ChiefAccountID: c.ID}); err != nil {
			return err
		}
		out = c.Account
		return nil
	})
	return out, SafeError(err)
}

// CreateAccount 由主管理员建号，使用部署初始密码并要求首次登录改密。
// 用户名重复返回 ErrConflict；哈希在事务外计算，事务内再次核对调用方权限。
func (s *Service) CreateAccount(ctx context.Context, p Principal, username, displayName string) (Account, error) {
	if err := s.RequireAccountManager(ctx, p); err != nil {
		return Account{}, err
	}
	if !usernamePattern.MatchString(username) || !utf8.ValidString(displayName) || utf8.RuneCountInString(displayName) > MaxDisplayNameRunes {
		return Account{}, ErrInvalid
	}
	hash, err := s.hash(s.options.InitialPassword)
	if err != nil {
		return Account{}, err
	}
	var out Account
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		if _, err := s.manager(tx, p); err != nil {
			return err
		}
		if _, err := tx.AccountByName(username); err == nil {
			return ErrConflict
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		c := AccountRecord{Account: Account{ID: randomID(), Username: username, DisplayName: displayName, Status: StatusActive, MustChangePassword: true, CreatedAt: now()},
			PasswordHash: hash, AuthVersion: 1, UpdatedAt: now()}
		if err := tx.InsertAccount(c); err != nil {
			return err
		}
		out = c.Account
		return nil
	})
	return out, SafeError(err)
}

// SetAccountStatus 启用或停用账号并撤销其全部会话；同状态幂等，不升版本也不撤销。
// 主管理员不能停用自己；启用不恢复旧会话。
func (s *Service) SetAccountStatus(ctx context.Context, p Principal, id string, status AccountStatus) (Account, error) {
	if status != StatusActive && status != StatusDisabled {
		return Account{}, ErrInvalid
	}
	var out Account
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		if _, err := s.manager(tx, p); err != nil {
			return err
		}
		if id == p.AccountID && status == StatusDisabled {
			return ErrForbidden
		}
		c, err := tx.Account(id)
		if err != nil {
			return err
		}
		if c.Status != status {
			c.Status = status
			c.AuthVersion++
			c.UpdatedAt = now()
			if err = tx.SaveAccount(c); err != nil {
				return err
			}
			if err = tx.RevokeSessions(c.ID, now()); err != nil {
				return err
			}
		}
		out = c.Account
		return nil
	})
	return out, SafeError(err)
}

// page 解析分页参数：limit 必须在 1 到 MaxPageSize 之间，游标为空或可解码。
func page(cursor string, limit int) (Cursor, int, error) {
	if limit < 1 || limit > MaxPageSize {
		return Cursor{}, 0, ErrInvalid
	}
	var c Cursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > maxCursorBytes {
			return Cursor{}, 0, ErrInvalid
		}
		if json.Unmarshal(raw, &c) != nil || c.At.IsZero() || !uuidPattern.MatchString(c.ID) {
			return Cursor{}, 0, ErrInvalid
		}
	}
	return c, limit, nil
}

func encodeCursor(at time.Time, id string) string {
	b, _ := json.Marshal(Cursor{At: at, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

// ListAccounts 由主管理员分页读取账号；多取一条判断是否还有下一页。
func (s *Service) ListAccounts(ctx context.Context, p Principal, cursor string, limit int) (AccountPage, error) {
	out := AccountPage{Items: []Account{}}
	if err := s.RequireAccountManager(ctx, p); err != nil {
		return out, err
	}
	c, n, err := page(cursor, limit)
	if err != nil {
		return out, err
	}
	rows, err := s.repo.Accounts(ctx, c, n+1)
	if err != nil {
		return out, SafeError(err)
	}
	if len(rows) > n {
		out.NextCursor = encodeCursor(rows[n-1].CreatedAt, rows[n-1].ID)
		rows = rows[:n]
	}
	out.Items = append(out.Items, rows...)
	return out, nil
}
