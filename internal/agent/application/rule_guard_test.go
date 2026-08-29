package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestRuleGuardCheck(t *testing.T) {
	t.Run("blocked when denylist matches", func(t *testing.T) {
		g := NewRuleGuard(RuleGuardDeps{
			Enabled:  func(context.Context) bool { return true },
			Denylist: func(context.Context) []string { return []string{"danger_tool"} },
			Metrics:  observability.NoopMetrics{},
		})
		blocks := &[]domain.RuleBlock{}
		ctx := context.WithValue(context.Background(), ruleBlockCollectorKey{}, blocks)
		block, blocked := g.Check(ctx, "danger_tool")
		if !blocked || block == nil || block.Rule != "tool_denylist" {
			t.Fatalf("expected blocked, got blocked=%v block=%v", blocked, block)
		}
		if len(*blocks) != 1 {
			t.Fatalf("expected 1 accumulated block, got %d", len(*blocks))
		}
	})
	t.Run("allow when not listed", func(t *testing.T) {
		g := NewRuleGuard(RuleGuardDeps{
			Enabled:  func(context.Context) bool { return true },
			Denylist: func(context.Context) []string { return []string{"danger_tool"} },
			Metrics:  observability.NoopMetrics{},
		})
		if block, blocked := g.Check(context.Background(), "safe_tool"); blocked || block != nil {
			t.Fatalf("expected allowed, got blocked=%v block=%v", blocked, block)
		}
	})
	t.Run("disabled guard allows", func(t *testing.T) {
		g := NewRuleGuard(RuleGuardDeps{Enabled: func(context.Context) bool { return false }})
		if block, blocked := g.Check(context.Background(), "danger_tool"); blocked || block != nil {
			t.Fatalf("expected allowed when disabled, got blocked=%v", blocked)
		}
	})
	t.Run("case-insensitive denylist match", func(t *testing.T) {
		g := NewRuleGuard(RuleGuardDeps{
			Enabled:  func(context.Context) bool { return true },
			Denylist: func(context.Context) []string { return []string{"DANGER_Tool", "  spaced_tool  "} },
			Metrics:  observability.NoopMetrics{},
		})
		if block, blocked := g.Check(context.Background(), "danger_tool"); !blocked || block == nil {
			t.Fatalf("expected case-insensitive block, got blocked=%v block=%v", blocked, block)
		}
	})
	t.Run("nil guard allows", func(t *testing.T) {
		var g *RuleGuard
		if block, blocked := g.Check(context.Background(), "danger_tool"); blocked || block != nil {
			t.Fatalf("expected allowed when nil guard, got blocked=%v", blocked)
		}
	})
}
