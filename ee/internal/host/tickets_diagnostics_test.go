// 本文件用真实 SQLite 更新故障验证票据消费回滚、关联诊断和敏感信息脱敏。
package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/darkBaryon/bifrost/ee/internal/auth"
	"github.com/darkBaryon/bifrost/ee/internal/database"
	"github.com/darkBaryon/bifrost/ee/internal/session"
)

type ticketFailureLog struct{ messages []string }

func (l *ticketFailureLog) Error(message string, args ...any) {
	l.messages = append(l.messages, fmt.Sprintf(message, args...))
}

func TestTicketSQLFailureLogsSafelyAndRollsBack(t *testing.T) {
	_, admin, fixture := testAdapter(t)
	ctx := database.WithOperation(context.Background(), "identity.ws-ticket")
	_, incidentID := database.Operation(ctx)
	if incidentID == "" {
		t.Fatal("操作上下文缺少关联编号")
	}
	ticket, err := fixture.Tickets.IssueTicket(ctx, admin.Principal)
	if err != nil {
		t.Fatal(err)
	}
	log := &ticketFailureLog{}
	fixture.Tickets.log = log
	// AFTER UPDATE + FAIL 可保留语句已完成的修改，必须由最外层事务回滚消费标记。
	if err := fixture.DB.Exec(`CREATE TRIGGER fail_ticket_consumption
AFTER UPDATE OF consumed_at ON ee_identity_ws_tickets
WHEN NEW.consumed_at IS NOT NULL
BEGIN
  SELECT RAISE(FAIL, 'driversecret');
END`).Error; err != nil {
		t.Fatal(err)
	}
	principal, err := fixture.Tickets.ConsumeTicket(ctx, ticket)
	if !errors.Is(err, auth.ErrUnavailable) || principal != (session.Principal{}) {
		t.Fatalf("SQL 故障返回不安全：principal=%+v err=%v", principal, err)
	}
	if ticketConsumedAt(t, fixture, ticket) != nil {
		t.Fatal("更新失败后票据消费标记未随事务回滚")
	}
	if len(log.messages) != 1 {
		t.Fatalf("存储故障诊断条数 = %d，期望 1", len(log.messages))
	}
	message := log.messages[0]
	for _, required := range []string{"EE console database failure", "operation=identity.ws-ticket", "incident_id=" + incidentID, "stage=ticket.consume", "kind=sqlite"} {
		if !strings.Contains(message, required) {
			t.Errorf("故障诊断缺少 %q：%s", required, message)
		}
	}
	for _, forbidden := range []string{"driversecret", ticket, admin.Token, session.Digest(ticket), "token", "ee_identity_ws_tickets", "update ", "select ", "raise(", "consumed_at"} {
		if strings.Contains(strings.ToLower(message), strings.ToLower(forbidden)) {
			t.Errorf("故障诊断包含凭据、驱动原文或 SQL：%s", message)
		}
	}
	if err := fixture.DB.Exec("DROP TRIGGER fail_ticket_consumption").Error; err != nil {
		t.Fatal(err)
	}
	principal, err = fixture.Tickets.ConsumeTicket(ctx, ticket)
	if err != nil || principal != admin.Principal || ticketConsumedAt(t, fixture, ticket) == nil {
		t.Fatalf("移除故障后同票据重试失败：principal=%+v err=%v", principal, err)
	}
	if len(log.messages) != 1 {
		t.Fatal("成功消费产生了额外故障日志")
	}
	// 已消费票据属于业务拒绝，不能再作为数据库故障记录。
	principal, err = fixture.Tickets.ConsumeTicket(ctx, ticket)
	if (!errors.Is(err, auth.ErrUnauthorized) && !errors.Is(err, session.ErrUnauthorized)) || principal != (session.Principal{}) {
		t.Fatalf("重复消费未按业务拒绝返回：principal=%+v err=%v", principal, err)
	}
	if len(log.messages) != 1 {
		t.Fatal("重复消费的业务拒绝产生了数据库故障日志")
	}
}
