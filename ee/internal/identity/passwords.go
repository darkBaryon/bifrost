// 本文件实现密码变更的三条路径（本人改密、管理员重置、离线恢复）与密码事件查询；三条路径共用 replacePassword。
package identity

import (
	"context"
	"errors"
)

// ChangePassword 校验旧密码后替换本人密码并撤销全部会话，调用方随后须重新登录。
// 旧密码错误返回 ErrInvalid 且不改变任何状态；比较期间密码被他人重置则返回 ErrConflict。
func (s *PasswordService) ChangePassword(ctx context.Context, p Principal, oldPassword, newPassword string) error {
	if !s.validPassword(newPassword) || newPassword == oldPassword {
		return ErrInvalid
	}
	_, before, err := s.current(ctx, p)
	if err != nil {
		return err
	}
	ok, err := s.compare(before.PasswordHash, oldPassword)
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
		_, c, err := current(tx.AuthByID, p)
		if err != nil {
			return err
		}
		if c.PasswordHash != before.PasswordHash {
			return ErrConflict
		}
		_, err = s.replacePassword(tx, c, hash, false,
			PasswordEvent{ActorID: c.ID, ActorName: c.Username, TargetID: c.ID, TargetName: c.Username, Action: ActionPasswordChange})
		return err
	}))
}

// replacePassword 是三种改密路径共用的事务尾部：写新哈希、升版本、撤销目标全部会话、记录成功事件，返回写入的事件。
func (s *PasswordService) replacePassword(tx Tx, c AccountRecord, hash string, mustChange bool, event PasswordEvent) (PasswordEvent, error) {
	c.PasswordHash = hash
	c.AuthVersion++
	c.MustChangePassword = mustChange
	c.UpdatedAt = now()
	if err := tx.SaveAccount(c); err != nil {
		return PasswordEvent{}, err
	}
	if err := tx.RevokeSessions(c.ID, now()); err != nil {
		return PasswordEvent{}, err
	}
	if event.ID == "" {
		event.ID = randomID()
	}
	if event.OperationID == "" {
		event.OperationID = randomID()
	}
	event.OccurredAt = now()
	event.Result = ResultSuccess
	return event, tx.InsertEvent(event)
}

// ResetPassword 由策略允许的账号管理者重置目标密码为部署初始密码并强制改密，成功事件与改密同事务提交。
// 权限、目标或自我重置等业务失败会写入失败事件并提交，随后作为错误返回；存储故障与下述预检冲突才回滚。
// 同一 actor/target 用同一 operation_id 重试时原样返回首次结果，不再改密。ErrConflict 有两种成因：
// 同 ID 不同 actor/target（换新 ID 重试）；以及预检拒绝而事务内放行的权限竞态，此时整笔回滚、不写事件，用同一 ID 重试即可。
func (s *PasswordService) ResetPassword(ctx context.Context, p Principal, targetID, operationID string) (PasswordEvent, error) {
	if !uuidPattern.MatchString(operationID) || !uuidPattern.MatchString(targetID) {
		return PasswordEvent{}, ErrInvalid
	}
	// 先做一次廉价的会话与权限预检，通过才计算 bcrypt：未授权的调用不占用哈希槽，
	// 否则任何持有会话的成员都能靠反复调用把槽位占满，拖垮登录与改密。
	// 预检失败不直接返回——仍进入事务，由其中的权限判定写失败事件（方案 4.2）。
	var hash string
	if s.requireAction(ctx, p, ResetAccountPassword, targetID) == nil {
		h, err := s.hash(s.options.InitialPassword)
		if err != nil {
			return PasswordEvent{}, err
		}
		hash = h
	}
	var event PasswordEvent
	var outcome error
	err := s.repo.Transaction(ctx, func(tx Tx) error {
		actual, actor, err := current(tx.AuthByID, p)
		if err != nil {
			return err
		}
		prev, err := tx.Event(operationID)
		switch {
		case err == nil:
			if prev.ActorID != p.AccountID || prev.TargetID != targetID {
				return ErrConflict
			}
			event, outcome = prev, nil
			if prev.Result == ResultFailure {
				outcome = Error(prev.ReasonCode)
			}
			return nil
		case !errors.Is(err, ErrNotFound):
			return err
		}
		event = PasswordEvent{ID: randomID(), OperationID: operationID, ActorID: actor.ID, ActorName: actor.Username,
			TargetID: targetID, Action: ActionPasswordReset, OccurredAt: now()}
		target, err := tx.Account(targetID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		event.TargetName = target.Username
		outcome = s.authorizeTx(ctx, tx, actual, ResetAccountPassword, targetID)
		switch {
		case outcome != nil:
		case targetID == p.AccountID:
			outcome = ErrForbidden // 操作者改自己的密码走本人改密
		case err != nil:
			outcome = ErrNotFound
		}
		if outcome != nil {
			if errors.Is(SafeError(outcome), ErrUnavailable) {
				return outcome
			}
			event.Result, event.ReasonCode = ResultFailure, SafeError(outcome).Error()
			return tx.InsertEvent(event)
		}
		if hash == "" {
			// 预检拒绝、事务内却放行：权限在两次判定之间刚被授予。整笔回滚不写事件，
			// 调用方用同一 operation_id 重试即可（重置本身幂等）。
			return ErrConflict
		}
		event, err = s.replacePassword(tx, target, hash, true, event)
		return err
	})
	if err != nil {
		return PasswordEvent{}, SafeError(err)
	}
	return event, outcome
}

// RecoverAdmin 仅由离线命令调用：重设固定恢复锚点账号密码、启用账号并撤销全部会话，事件的操作者记为 operator。
func (s *PasswordService) RecoverAdmin(ctx context.Context, password string) error {
	if !s.validPassword(password) {
		return ErrInvalid
	}
	hash, err := s.hash(password)
	if err != nil {
		return err
	}
	return SafeError(s.repo.Transaction(ctx, func(tx Tx) error {
		state := tx.State()
		if !state.Initialized {
			return ErrNotFound
		}
		c, err := tx.Account(state.ChiefAccountID)
		if err != nil {
			return err
		}
		c.Status = StatusActive
		_, err = s.replacePassword(tx, c, hash, false,
			PasswordEvent{ActorID: OperatorActor, ActorName: OperatorActor, TargetID: c.ID, TargetName: c.Username, Action: ActionAdminRecovery})
		if err != nil {
			return err
		}
		return s.accessPolicy(tx).AfterRecover(ctx, tx.State())
	}))
}

// ListPasswordEvents 分页读取密码事件：可读全部的调用方按 target 筛选，其他调用方只能查看自己的记录，指定他人返回 ErrForbidden。
func (s *PasswordService) ListPasswordEvents(ctx context.Context, p Principal, target, cursor string, limit int) (EventPage, error) {
	out := EventPage{Items: []PasswordEvent{}}
	actual, _, err := s.current(ctx, p)
	if err != nil {
		return out, err
	}
	if err := fullSession(actual); err != nil {
		return out, err
	}
	state, err := s.State(ctx)
	if err != nil {
		return out, err
	}
	err = authorize(ctx, s.policy, state, actual, ReadAllPasswordEvents, "")
	if err != nil && !errors.Is(err, ErrForbidden) {
		return out, err
	}
	if errors.Is(err, ErrForbidden) {
		if target != "" && target != p.AccountID {
			return out, ErrForbidden
		}
		target = p.AccountID
	}
	c, err := page(cursor, limit)
	if err != nil {
		return out, err
	}
	rows, err := s.repo.Events(ctx, target, c, limit+1)
	if err != nil {
		return out, SafeError(err)
	}
	if len(rows) > limit {
		out.NextCursor = encodeCursor(rows[limit-1].OccurredAt, rows[limit-1].ID)
		rows = rows[:limit]
	}
	out.Items = append(out.Items, rows...)
	return out, nil
}
