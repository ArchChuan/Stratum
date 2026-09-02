package application

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// TestEvalSpanAttrs 验证 evalSpanAttrs 纯函数：rule_hits 恒挂（值=信号数）、
// emitted 恒挂（true）、行为三属性仅在 behaviorFromResult 非空时挂载。
func TestEvalSpanAttrs(t *testing.T) {
	base := &AgentResult{TokensUsed: 100, CostUSD: 0.01, Duration: 500}
	cases := []struct {
		name        string
		result      *AgentResult
		signals     []port.RuleSignalPayload
		wantHits    int64
		wantRetry   bool
		wantAbandon bool
		wantAttrs   int
	}{
		{name: "no signals no behavior", result: base, signals: nil, wantHits: 0, wantAttrs: 2},
		{name: "two rule hits retry", result: &AgentResult{NoAnswer: &domain.NoAnswerInfo{Retried: true}},
			signals: []port.RuleSignalPayload{{Rule: "r1"}, {Rule: "r2"}}, wantHits: 2, wantRetry: true, wantAttrs: 5},
		{name: "degraded abandonment", result: &AgentResult{Degraded: true},
			signals: nil, wantHits: 0, wantAbandon: true, wantAttrs: 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			attrs := evalSpanAttrs(tc.result, tc.signals)
			require.Len(t, attrs, tc.wantAttrs)
			got := make(map[string]attribute.Value, len(attrs))
			for _, a := range attrs {
				got[string(a.Key)] = a.Value
			}
			require.Equal(t, attribute.Int64Value(tc.wantHits), got["opik.metadata.stratum.eval_rule_hits"])
			require.Equal(t, attribute.BoolValue(true), got["opik.metadata.stratum.eval_emitted"])
			// 行为属性仅在 behaviorFromResult 非空时挂载；无行为时键不得出现。
			retryVal, hasRetry := got["opik.metadata.stratum.eval_behavior_retry"]
			abandonVal, hasAbandon := got["opik.metadata.stratum.eval_behavior_abandonment"]
			wantBehavior := tc.wantRetry || tc.wantAbandon
			require.Equal(t, wantBehavior, hasRetry, "retry attr presence")
			require.Equal(t, wantBehavior, hasAbandon, "abandonment attr presence")
			if wantBehavior {
				require.Equal(t, attribute.BoolValue(tc.wantRetry), retryVal)
				require.Equal(t, attribute.BoolValue(tc.wantAbandon), abandonVal)
			}
		})
	}
}
