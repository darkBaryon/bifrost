// 本文件实现账号生命周期及密码与会话一致性规则。
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
	"unicode/utf8"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.@-]{2,63}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Options 是已由部署配置解析的认证规则，不包含全局Server或Config。
type Options struct {
	InitialPassword, SetupToken string
	SessionTTL                  time.Duration
}
type Service struct {
	repo      Repository
	hasher    PasswordHasher
	policy    AccountPolicy
	options   Options
	hashSlots chan struct{}
	dummyHash string
}

func NewService(repo Repository, hasher PasswordHasher, options Options, policy AccountPolicy) (*Service, error) {
	if repo == nil || hasher == nil {
		return nil, ErrInvalid
	}
	if options.InitialPassword == "" || !utf8.ValidString(options.InitialPassword) || len(options.InitialPassword) > 72 || options.SessionTTL < time.Hour || options.SessionTTL > 168*time.Hour {
		return nil, ErrInvalid
	}
	if policy == nil {
		policy = ChiefPolicy{}
	}
	dummy, err := hasher.Hash("dummy-verification-only")
	if err != nil {
		return nil, SafeError(err)
	}
	return &Service{repo: repo, hasher: hasher, options: options, policy: policy, hashSlots: make(chan struct{}, 4), dummyHash: dummy}, nil
}
func now() time.Time { return time.Now().UTC() }
func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("crypto/rand unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func randomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrUnavailable
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func tokenHash(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }
func (s *Service) hash(password string) (string, error) {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		return "", ErrLimited
	}
	h, err := s.hasher.Hash(password)
	return h, SafeError(err)
}
func (s *Service) compare(hash, password string) (bool, error) {
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		return false, ErrLimited
	}
	ok, err := s.hasher.Compare(hash, password)
	return ok, SafeError(err)
}
func (s *Service) validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= 8 && len(password) <= 72 && password != s.options.InitialPassword
}
func auth(record AuthRecord, at time.Time) (Principal, error) {
	c, v := record.Credential, record.Session
	if c.ID == "" || c.Status != "active" || v.RevokedAt != nil || !v.ExpiresAt.After(at) || c.AuthVersion != v.IssuedAuthVersion {
		return Principal{}, ErrUnauthorized
	}
	return Principal{AccountID: c.ID, SessionID: v.ID, AuthVersion: c.AuthVersion, MustChangePassword: c.MustChangePassword}, nil
}
func principal(tx Tx, p Principal) (Principal, Credential, error) {
	r, err := tx.AuthByID(p.SessionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			err = ErrUnauthorized
		}
		return Principal{}, Credential{}, err
	}
	actual, err := auth(r, now())
	if err == nil && (actual.AccountID != p.AccountID || actual.AuthVersion != p.AuthVersion) {
		err = ErrUnauthorized
	}
	return actual, r.Credential, err
}
func (s *Service) manager(ctx context.Context, tx Tx, p Principal) (Credential, error) {
	actual, c, err := principal(tx, p)
	if err != nil {
		return c, err
	}
	err = s.policy.RequireAccountManager(context.WithValue(ctx, policyStateKey{}, tx.State()), actual)
	return c, err
}
func (s *Service) State(ctx context.Context) (State, error) {
	v, e := s.repo.State(ctx)
	return v, SafeError(e)
}
func (s *Service) Authenticate(ctx context.Context, raw string) (Principal, error) {
	if len(raw) != 43 {
		return Principal{}, ErrUnauthorized
	}
	r, err := s.repo.AuthByHash(ctx, tokenHash(raw))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, SafeError(err)
	}
	return auth(r, now())
}

// ValidateSession 检查连接保存的会话ID，调用者不能持有旧RequestCtx。
func (s *Service) ValidateSession(ctx context.Context, id string) (Principal, error) {
	r, err := s.repo.AuthByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, SafeError(err)
	}
	return auth(r, now())
}
func (s *Service) RequireAccountManager(ctx context.Context, p Principal) error {
	actual, err := s.ValidateSession(ctx, p.SessionID)
	if err != nil {
		return err
	}
	if actual.AccountID != p.AccountID || actual.AuthVersion != p.AuthVersion {
		return ErrUnauthorized
	}
	state, err := s.State(ctx)
	if err != nil {
		return err
	}
	return s.policy.RequireAccountManager(context.WithValue(ctx, policyStateKey{}, state), actual)
}
func (s *Service) Me(ctx context.Context, p Principal) (Account, error) {
	r, err := s.repo.AuthByID(ctx, p.SessionID)
	if err != nil {
		return Account{}, SafeError(err)
	}
	actual, err := auth(r, now())
	if err != nil {
		return Account{}, err
	}
	if actual.AccountID != p.AccountID || actual.AuthVersion != p.AuthVersion {
		return Account{}, ErrUnauthorized
	}
	return r.Credential.Account, nil
}

// BootstrapLegacy 一次性接受有效的旧凭据；已初始化后不再读取旧密码语义。
func (s *Service) BootstrapLegacy(ctx context.Context, legacy *Credential) error {
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		state := tx.State()
		if state.Initialized {
			return nil
		}
		if legacy == nil {
			return nil
		}
		if legacy.Username == "" || !s.hasher.ValidHash(legacy.PasswordHash) {
			return ErrInvalid
		}
		c := *legacy
		c.ID = randomID()
		c.Status = "active"
		c.AuthVersion = 1
		c.MustChangePassword = false
		c.CreatedAt = now()
		c.UpdatedAt = c.CreatedAt
		if err := tx.InsertAccount(c); err != nil {
			return err
		}
		return tx.SaveState(State{Initialized: true, ChiefAccountID: c.ID})
	}))
}
func (s *Service) Initialize(ctx context.Context, setupToken, username, password string) (Account, error) {
	var out Account
	state, err := s.State(ctx)
	if err != nil {
		return out, err
	}
	if state.Initialized {
		return out, ErrConflict
	}
	a, b := sha256.Sum256([]byte(setupToken)), sha256.Sum256([]byte(s.options.SetupToken))
	if s.options.SetupToken == "" || subtle.ConstantTimeCompare(a[:], b[:]) != 1 {
		return out, ErrForbidden
	}
	if !usernamePattern.MatchString(username) || !s.validPassword(password) {
		return out, ErrInvalid
	}
	hash, err := s.hash(password)
	if err != nil {
		return out, err
	}
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		if tx.State().Initialized {
			return ErrConflict
		}
		c := Credential{Account: Account{ID: randomID(), Username: username, Status: "active"}, PasswordHash: hash, AuthVersion: 1, CreatedAt: now(), UpdatedAt: now()}
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
func (s *Service) Login(ctx context.Context, username, password, peerIP string) (IssuedSession, error) {
	var out IssuedSession
	if len(username) > 512 || len(password) > 1024 || username == "" {
		return out, ErrUnauthorized
	}
	if err := s.repo.ReserveLogin(ctx, tokenHash(username), tokenHash(peerIP), now()); err != nil {
		return out, SafeError(err)
	}
	c, err := s.repo.CredentialByName(ctx, username)
	missing := errors.Is(err, ErrNotFound)
	if err != nil && !missing {
		return out, SafeError(err)
	}
	hash := c.PasswordHash
	if missing {
		hash = s.dummyHash
	}
	ok, err := s.compare(hash, password)
	if err != nil {
		return out, err
	}
	if missing || !ok || c.Status != "active" {
		return out, ErrUnauthorized
	}
	raw, err := randomToken()
	if err != nil {
		return out, err
	}
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		latest, e := tx.Account(c.ID)
		if e != nil {
			return e
		}
		if latest.AuthVersion != c.AuthVersion || latest.PasswordHash != c.PasswordHash || latest.Status != "active" {
			return ErrUnauthorized
		}
		v := Session{ID: randomID(), TokenHash: tokenHash(raw), AccountID: c.ID, IssuedAuthVersion: c.AuthVersion, CreatedAt: now(), ExpiresAt: now().Add(s.options.SessionTTL)}
		if e = tx.InsertSession(v); e != nil {
			return e
		}
		p, e := auth(AuthRecord{v, latest}, now())
		if e != nil {
			return e
		}
		out = IssuedSession{Principal: p, Token: raw, ExpiresAt: v.ExpiresAt}
		return nil
	})
	return out, SafeError(err)
}
func (s *Service) Logout(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	r, err := s.repo.AuthByHash(ctx, tokenHash(raw))
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return SafeError(err)
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error { return tx.RevokeSession(r.Session.ID, now()) }))
}
func (s *Service) CreateAccount(ctx context.Context, p Principal, username, displayName string) (Account, error) {
	var out Account
	if err := s.RequireAccountManager(ctx, p); err != nil {
		return out, err
	}
	if !usernamePattern.MatchString(username) || !utf8.ValidString(displayName) || utf8.RuneCountInString(displayName) > 128 {
		return out, ErrInvalid
	}
	hash, err := s.hash(s.options.InitialPassword)
	if err != nil {
		return out, err
	}
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		if _, e := s.manager(ctx, tx, p); e != nil {
			return e
		}
		if _, e := tx.AccountByName(username); e == nil {
			return ErrConflict
		} else if !errors.Is(e, ErrNotFound) {
			return e
		}
		c := Credential{Account: Account{ID: randomID(), Username: username, DisplayName: displayName, Status: "active", MustChangePassword: true}, PasswordHash: hash, AuthVersion: 1, CreatedAt: now(), UpdatedAt: now()}
		if e := tx.InsertAccount(c); e != nil {
			return e
		}
		out = c.Account
		return nil
	})
	return out, SafeError(err)
}
func (s *Service) SetAccountStatus(ctx context.Context, p Principal, id, status string) error {
	if status != "active" && status != "disabled" {
		return ErrInvalid
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		if _, err := s.manager(ctx, tx, p); err != nil {
			return err
		}
		if id == p.AccountID && status == "disabled" {
			return ErrForbidden
		}
		c, err := tx.Account(id)
		if err != nil {
			return err
		}
		if c.Status == status {
			return nil
		}
		c.Status = status
		c.AuthVersion++
		c.UpdatedAt = now()
		if err = tx.SaveAccount(c); err != nil {
			return err
		}
		return tx.RevokeSessions(c.ID, now())
	}))
}
func (s *Service) ChangePassword(ctx context.Context, p Principal, oldPassword, newPassword string) error {
	if !s.validPassword(newPassword) || newPassword == oldPassword {
		return ErrInvalid
	}
	record, err := s.repo.AuthByID(ctx, p.SessionID)
	if err != nil {
		return SafeError(err)
	}
	actual, err := auth(record, now())
	if err != nil {
		return err
	}
	if actual.AccountID != p.AccountID || actual.AuthVersion != p.AuthVersion {
		return ErrUnauthorized
	}
	ok, err := s.compare(record.Credential.PasswordHash, oldPassword)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalid
	}
	hash, err := s.hash(newPassword)
	if err != nil {
		return err
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		_, c, e := principal(tx, p)
		if e != nil {
			return e
		}
		if c.PasswordHash != record.Credential.PasswordHash {
			return ErrConflict
		}
		return s.replacePassword(tx, c, hash, false, PasswordEvent{ActorID: c.ID, ActorName: c.Username, TargetID: c.ID, TargetName: c.Username, Action: "password_change"})
	}))
}
func (s *Service) replacePassword(tx Tx, c Credential, hash string, mustChange bool, event PasswordEvent) error {
	c.PasswordHash = hash
	c.AuthVersion++
	c.MustChangePassword = mustChange
	c.UpdatedAt = now()
	if err := tx.SaveAccount(c); err != nil {
		return err
	}
	if err := tx.RevokeSessions(c.ID, now()); err != nil {
		return err
	}
	if event.ID == "" {
		event.ID = randomID()
	}
	if event.OperationID == "" {
		event.OperationID = randomID()
	}
	event.OccurredAt = now()
	event.Result = "success"
	return tx.InsertEvent(event)
}
func (s *Service) ResetPassword(ctx context.Context, p Principal, id, operationID string) (PasswordEvent, error) {
	var event PasswordEvent
	if !uuidPattern.MatchString(operationID) || !uuidPattern.MatchString(id) {
		return event, ErrInvalid
	}
	// 重置的失败事件也要事务提交，因此业务失败与存储失败分别返回。
	var result error
	hash, err := s.hash(s.options.InitialPassword)
	if err != nil {
		return event, err
	}
	err = s.repo.Transaction(ctx, func(tx Tx) error {
		result = nil
		_, actor, e := principal(tx, p)
		if e != nil {
			return e
		}
		prev, e := tx.Event(operationID)
		if e == nil {
			if prev.ActorID != p.AccountID || prev.TargetID != id {
				return ErrConflict
			}
			event = prev
			if prev.Result != "success" {
				result = Error(prev.ReasonCode)
			}
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		event = PasswordEvent{ID: randomID(), OperationID: operationID, ActorID: actor.ID, ActorName: actor.Username, TargetID: id, Action: "password_reset", OccurredAt: now()}
		_, result = s.manager(ctx, tx, p)
		if result == nil && id == p.AccountID {
			result = ErrForbidden
		}
		target, e := tx.Account(id)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		if result == nil && e != nil {
			result = ErrNotFound
		}
		event.TargetName = target.Username
		if result != nil {
			event.Result = "failure"
			event.ReasonCode = SafeError(result).Error()
			return tx.InsertEvent(event)
		}
		event.Result = "success"
		return s.replacePassword(tx, target, hash, true, event)
	})
	if err != nil {
		return PasswordEvent{}, SafeError(err)
	}
	return event, result
}

// RecoverAdmin 仅由离线命令调用，不接入HTTP路由。
func (s *Service) RecoverAdmin(ctx context.Context, password string) error {
	if !s.validPassword(password) {
		return ErrInvalid
	}
	hash, err := s.hash(password)
	if err != nil {
		return err
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		st := tx.State()
		if !st.Initialized {
			return ErrNotFound
		}
		c, e := tx.Account(st.ChiefAccountID)
		if e != nil {
			return e
		}
		c.Status = "active"
		return s.replacePassword(tx, c, hash, false, PasswordEvent{ActorID: "operator", ActorName: "operator", TargetID: c.ID, TargetName: c.Username, Action: "admin_recovery"})
	}))
}
func page(cursor string, limit int) (Cursor, int, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return Cursor{}, 0, ErrInvalid
	}
	var c Cursor
	if cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || len(raw) > 256 {
			return c, 0, ErrInvalid
		}
		if json.Unmarshal(raw, &c) != nil || c.At.IsZero() || !uuidPattern.MatchString(c.ID) {
			return c, 0, ErrInvalid
		}
	}
	return c, limit, nil
}
func nextCursor(at time.Time, id string) *string {
	b, _ := json.Marshal(Cursor{At: at, ID: id})
	v := base64.RawURLEncoding.EncodeToString(b)
	return &v
}
func (s *Service) ListAccounts(ctx context.Context, p Principal, cursor string, limit int) (AccountPage, error) {
	out := AccountPage{Items: []Account{}}
	if e := s.RequireAccountManager(ctx, p); e != nil {
		return out, e
	}
	c, n, e := page(cursor, limit)
	if e != nil {
		return out, e
	}
	rows, e := s.repo.Accounts(ctx, c, n+1)
	if e != nil {
		return out, SafeError(e)
	}
	if len(rows) > n {
		out.NextCursor = nextCursor(rows[n-1].CreatedAt, rows[n-1].ID)
		rows = rows[:n]
	}
	for _, r := range rows {
		out.Items = append(out.Items, r.Account)
	}
	return out, nil
}
func (s *Service) ListPasswordEvents(ctx context.Context, p Principal, target, cursor string, limit int) (EventPage, error) {
	out := EventPage{Items: []PasswordEvent{}}
	actual, e := s.ValidateSession(ctx, p.SessionID)
	if e != nil {
		return out, e
	}
	if actual.AccountID != p.AccountID || actual.AuthVersion != p.AuthVersion {
		return out, ErrUnauthorized
	}
	if actual.MustChangePassword {
		return out, ErrForbidden
	}
	st, e := s.State(ctx)
	if e != nil {
		return out, e
	}
	all, e := s.policy.CanReadAllPasswordEvents(context.WithValue(ctx, policyStateKey{}, st), actual)
	if e != nil {
		return out, e
	}
	if !all {
		if target != "" && target != p.AccountID {
			return out, ErrForbidden
		}
		target = p.AccountID
	}
	c, n, e := page(cursor, limit)
	if e != nil {
		return out, e
	}
	rows, e := s.repo.Events(ctx, target, c, n+1)
	if e != nil {
		return out, SafeError(e)
	}
	if len(rows) > n {
		out.NextCursor = nextCursor(rows[n-1].OccurredAt, rows[n-1].ID)
		rows = rows[:n]
	}
	out.Items = rows
	return out, nil
}
func (s *Service) IssueTicket(ctx context.Context, p Principal) (string, error) {
	raw, e := randomToken()
	if e != nil {
		return "", e
	}
	e = s.repo.Transaction(ctx, func(tx Tx) error {
		if _, e := s.manager(ctx, tx, p); e != nil {
			return e
		}
		return tx.InsertTicket(Ticket{Hash: tokenHash(raw), SessionID: p.SessionID, ExpiresAt: now().Add(WSTicketTTL)})
	})
	return raw, SafeError(e)
}
func (s *Service) ConsumeTicket(ctx context.Context, raw string) (Principal, error) {
	var p Principal
	if len(raw) != 43 {
		return p, ErrUnauthorized
	}
	e := s.repo.Transaction(ctx, func(tx Tx) error {
		id, e := tx.ConsumeTicket(tokenHash(raw), now())
		if e != nil {
			return e
		}
		r, e := tx.AuthByID(id)
		if e != nil {
			return e
		}
		p, e = auth(r, now())
		if e != nil {
			return e
		}
		_, e = s.manager(ctx, tx, p)
		return e
	})
	return p, SafeError(e)
}

// Account 在事务内重验管理员身份，再读取目标账号的公开信息。
func (s *Service) Account(ctx context.Context, p Principal, id string) (Account, error) {
	var out Account
	e := s.repo.Transaction(ctx, func(tx Tx) error {
		if _, e := s.manager(ctx, tx, p); e != nil {
			return e
		}
		c, e := tx.Account(id)
		out = c.Account
		return e
	})
	return out, SafeError(e)
}
