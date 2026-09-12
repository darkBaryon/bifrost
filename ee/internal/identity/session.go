// 本文件是会话线：登录签发、Cookie 验证、WebSocket 重验、登出与一次性票据。
package identity

import (
	"context"
	"errors"
)

// Authenticate 用 Cookie 中的原始 token 换取身份；形状不对的 token 不查库。
func (s *SessionService) Authenticate(ctx context.Context, raw string) (Principal, error) {
	if len(raw) != tokenLength {
		return Principal{}, ErrUnauthorized
	}
	p, _, err := resolve(s.repo.AuthByHash(ctx, digest(raw)))
	return p, err
}

// ValidateSession 按会话 ID 重新验证，供 WebSocket 连接在每次写入前调用；调用方不能持有旧的 RequestCtx。
func (s *SessionService) ValidateSession(ctx context.Context, id string) (Principal, error) {
	p, _, err := resolve(s.repo.AuthByID(ctx, id))
	return p, err
}

// Me 返回调用方自己的账号信息，受限会话同样可用。
func (s *SessionService) Me(ctx context.Context, p Principal) (Account, error) {
	_, c, err := s.current(ctx, p)
	return c.Account, err
}

// Login 先限流、再比较密码，最后在事务内重读账号后签发会话；错误用户名、密码和停用账号统一返回 ErrUnauthorized。
// 超长输入不可能是有效凭据，直接拒绝且不占用限流桶，避免用巨大请求耗尽哈希槽。
func (s *SessionService) Login(ctx context.Context, username, password, peerIP string) (IssuedSession, error) {
	if username == "" || len(username) > maxLoginUsernameBytes || len(password) > maxLoginPasswordBytes {
		return IssuedSession{}, ErrUnauthorized
	}
	limits := []LoginLimit{
		{Key: "ip:" + digest(peerIP), Max: LoginAttemptsPerIP, Window: LoginRateWindow},
		{Key: "name:" + digest(username), Max: LoginAttemptsPerUsername, Window: LoginRateWindow},
	}
	if err := s.repo.ReserveLogin(ctx, limits, now()); err != nil {
		return IssuedSession{}, SafeError(err)
	}
	c, err := s.repo.RecordByName(ctx, username)
	missing := errors.Is(err, ErrNotFound)
	if err != nil && !missing {
		return IssuedSession{}, SafeError(err)
	}
	hash := c.PasswordHash
	if missing {
		hash = s.dummyHash
	}
	ok, err := s.compare(hash, password)
	if err != nil {
		return IssuedSession{}, err
	}
	if missing || !ok || c.Status != StatusActive {
		return IssuedSession{}, ErrUnauthorized
	}
	raw := randomToken()
	var out IssuedSession
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		// 比较密码期间可能发生重置或停用：签发前重读，版本或哈希变化则拒绝。
		latest, err := tx.Account(c.ID)
		if err != nil {
			return err
		}
		if latest.AuthVersion != c.AuthVersion || latest.PasswordHash != c.PasswordHash || latest.Status != StatusActive {
			return ErrUnauthorized
		}
		v := Session{ID: randomID(), TokenHash: digest(raw), AccountID: c.ID, IssuedAuthVersion: latest.AuthVersion,
			CreatedAt: now(), ExpiresAt: now().Add(s.options.SessionTTL)}
		if err = tx.InsertSession(v); err != nil {
			return err
		}
		p, err := verify(AuthRecord{Session: v, Account: latest}, now())
		if err != nil {
			return err
		}
		out = IssuedSession{Principal: p, Account: latest.Account, Token: raw, ExpiresAt: v.ExpiresAt}
		return nil
	})
	return out, SafeError(err)
}

// Logout 撤销 token 对应的会话；空、形状不对或已失效的 token 同样视为成功，浏览器随后清除 Cookie。
func (s *SessionService) Logout(ctx context.Context, raw string) error {
	if len(raw) != tokenLength {
		return nil
	}
	r, err := s.repo.AuthByHash(ctx, digest(raw))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return SafeError(err)
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error { return tx.RevokeSession(r.Session.ID, now()) }))
}

// IssueTicket 为正常的主管理员会话签发一次性 WebSocket 票据，原文只返回一次。
func (s *SessionService) IssueTicket(ctx context.Context, p Principal) (string, error) {
	raw := randomToken()
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		if _, err := s.manager(tx, p); err != nil {
			return err
		}
		return tx.InsertTicket(Ticket{Hash: digest(raw), SessionID: p.SessionID, ExpiresAt: now().Add(WSTicketTTL)})
	})
	return raw, SafeError(err)
}

// ConsumeTicket 消费票据并返回其会话身份；票据只能用一次，且会话仍须是正常的主管理员会话。
func (s *SessionService) ConsumeTicket(ctx context.Context, raw string) (Principal, error) {
	if len(raw) != tokenLength {
		return Principal{}, ErrUnauthorized
	}
	var out Principal
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		id, err := tx.ConsumeTicket(digest(raw), now())
		if err != nil {
			return err
		}
		p, _, err := resolve(tx.AuthByID(id))
		if err != nil {
			return err
		}
		if err := s.authorize(tx.State(), p); err != nil {
			return err
		}
		out = p
		return nil
	})
	return out, SafeError(err)
}
