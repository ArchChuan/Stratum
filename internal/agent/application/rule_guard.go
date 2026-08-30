package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

// RuleBlockedError 是规则护栏即时拦截信号：命中即失败返回，禁止默认放行（§4.1 fail closed）。
type RuleBlockedError struct {
	Rule    string
	Tool    string
	Message string
}

func (e *RuleBlockedError) Error() string {
	return fmt.Sprintf("rule_blocked:%s:tool=%s:%s", e.Rule, e.Tool, e.Message)
}

// ruleBlockCollectorKey 是 context 累积器 key：AgentService 在执行上下文中注入
// *[]domain.RuleBlock，RuleGuard 命中时追加，emitObservation 读取进观测事件。
type ruleBlockCollectorKey struct{}

// RuleGuardDeps 是规则护栏依赖。Enabled/Denylist 由 wiring 包装平台参数读取
// （evaluation.ruleguard.*，仅注册不播种；enabled 默认 false 时静默放行）。
type RuleGuardDeps struct {
	Enabled  func(ctx context.Context) bool
	Denylist func(ctx context.Context) []string
	Metrics  observability.MetricsProvider
	Logger   *zap.Logger
}

// RuleGuard 是内联规则护栏（§4.1 快路径）：denylist 命中即时拦截，零 LLM、零额外延迟。
type RuleGuard struct {
	deps RuleGuardDeps
}

func NewRuleGuard(deps RuleGuardDeps) *RuleGuard {
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	if deps.Metrics == nil {
		deps.Metrics = observability.NoopMetrics{}
	}
	return &RuleGuard{deps: deps}
}

// Check 对单个工具名执行规则护栏：denylist 命中返回 RuleBlockedError（fail closed）
// 并计数 T1（eval_rule_hit_total + eval_gate_action_total），同时把拦截记录追加到
// ctx 累积器供观测事件携带；未命中或规则未启用返回 nil（放行）。
func (g *RuleGuard) Check(ctx context.Context, toolID string) (*RuleBlockedError, bool) {
	if g == nil || g.deps.Enabled == nil || !g.deps.Enabled(ctx) {
		return nil, false
	}
	if g.deps.Denylist == nil {
		return nil, false
	}
	for _, denied := range g.deps.Denylist(ctx) {
		denied = strings.TrimSpace(denied)
		if denied == "" || !strings.EqualFold(denied, toolID) {
			continue
		}
		block := &RuleBlockedError{
			Rule:    "tool_denylist",
			Tool:    toolID,
			Message: fmt.Sprintf("tool %q blocked by platform rule", toolID),
		}
		g.deps.Metrics.IncEvalRuleHit("tool_denylist", "agent", "block")
		g.deps.Metrics.IncEvalGateAction("rule_guard", "block")
		if collector, ok := ctx.Value(ruleBlockCollectorKey{}).(*[]domain.RuleBlock); ok {
			*collector = append(*collector, domain.RuleBlock{Rule: block.Rule, Tool: toolID, Message: block.Message})
		}
		return block, true
	}
	return nil, false
}
