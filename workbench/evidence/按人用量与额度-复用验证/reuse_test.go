// 本文件实测既有治理计数器的复用边界；不向运行中的网关安装任何业务代码。
package reuse_test

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/governance"
)

type quietLog struct{}

func (quietLog) Debug(string, ...any)                   {}
func (quietLog) Info(string, ...any)                    {}
func (quietLog) Warn(string, ...any)                    {}
func (quietLog) Error(string, ...any)                   {}
func (quietLog) Fatal(string, ...any)                   { panic("unexpected fatal") }
func (quietLog) SetLevel(schemas.LogLevel)              {}
func (quietLog) SetOutputType(schemas.LoggerOutputType) {}
func (quietLog) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func store(t *testing.T) *governance.LocalGovernanceStore {
	t.Helper()
	s, err := governance.NewLocalGovernanceStore(context.Background(), quietLog{}, nil, &configstore.GovernanceConfig{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func monthStart() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func limit(id string) []schemas.Limit {
	return []schemas.Limit{{ID: id, HolderKind: "user", HolderID: "employee-a"}}
}

func charge(t *testing.T, s *governance.LocalGovernanceStore, limits []schemas.Limit, amount float64) {
	t.Helper()
	if err := s.ChargeBudgets(context.Background(), limits, amount); err != nil {
		t.Fatal(err)
	}
}

func TestSharedCounterAndConfigReplacement(t *testing.T) {
	s, ctx := store(t), context.Background()
	s.UpsertBudgetConfig(ctx, "employee-a", &tables.TableBudget{ID: "employee-a", MaxLimit: 100, ResetDuration: "1M", LastReset: monthStart(), IsCalendarAligned: true})
	// 两个调用方只要引用同一个预算ID，就累计到同一个计数器；这里没有验证VK识别和HTTP装配。
	charge(t, s, limit("employee-a"), 10)
	charge(t, s, limit("employee-a"), 15)
	replacement := &tables.TableBudget{ID: "employee-a", MaxLimit: 30, ResetDuration: "1M", LastReset: monthStart(), IsCalendarAligned: true}
	s.UpsertBudgetConfig(ctx, "employee-a", replacement)
	if got := s.LoadBudget(ctx, "employee-a").CurrentUsage; got != 25 {
		t.Fatalf("config replacement lost usage: %v", got)
	}
	if got, err := s.CheckBudgets(ctx, limit("employee-a"), nil); got != governance.DecisionAllow || err != nil {
		t.Fatalf("below limit: %v %v", got, err)
	}
	charge(t, s, limit("employee-a"), 5)
	if got, _ := s.CheckBudgets(ctx, limit("employee-a"), nil); got != governance.DecisionBudgetExceeded {
		t.Fatalf("exhausted: %v", got)
	}
}

func TestLateChargeUsesCurrentMonth(t *testing.T) {
	s, ctx := store(t), context.Background()
	previous := monthStart().AddDate(0, -1, 0)
	s.UpsertBudgetConfig(ctx, "employee-a", &tables.TableBudget{ID: "employee-a", MaxLimit: 100, ResetDuration: "1M", LastReset: previous, CurrentUsage: 90, IsCalendarAligned: true})
	admittedBeforeReset := limit("employee-a")
	// 准入时捕获的Limit只有预算ID，没有周期ID。模拟月初重置，然后结算上月已放行的请求。
	if _, ok := s.ResetBudgetAt(ctx, "employee-a", monthStart()); !ok {
		t.Fatal("reset not applied")
	}
	charge(t, s, admittedBeforeReset, 5)
	got := s.LoadBudget(ctx, "employee-a")
	if !got.LastReset.Equal(monthStart()) || got.CurrentUsage != 5 {
		t.Fatalf("unexpected current-month state: %+v", got)
	}
	t.Log("旧请求的费用进入当前月：仅复用稳定预算ID不满足按开始周期归属")
}

func TestMissingCounterIsNotAnAdmissionFailure(t *testing.T) {
	s, ctx := store(t), context.Background()
	got, err := s.CheckBudgets(ctx, limit("not-loaded"), nil)
	if got != governance.DecisionAllow || err != nil {
		t.Fatalf("baseline behavior changed: %v %v", got, err)
	}
	charge(t, s, limit("not-loaded"), 5)
	if s.LoadBudget(ctx, "not-loaded") != nil {
		t.Fatal("unexpected implicit creation")
	}
	t.Log("不存在的预算被忽略：受管用户装配/查库失败需由EE显式拒绝")
}
