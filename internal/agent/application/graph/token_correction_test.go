package graph

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestUpdateTokenCorrection(t *testing.T) {
	tests := []struct {
		name       string
		correction float64
		estimated  int
		actual     int
		want       float64
	}{
		{
			name:       "no baseline estimate leaves correction unchanged",
			correction: 1.0, estimated: 0, actual: 100, want: 1.0,
		},
		{
			name:       "no reported prompt usage leaves correction unchanged",
			correction: 1.0, estimated: 100, actual: 0, want: 1.0,
		},
		{
			name:       "ratio 1 keeps correction at 1.0",
			correction: 1.0, estimated: 1000, actual: 1000, want: 1.0,
		},
		{
			name:       "under-estimation raises correction (smoothing 0.1)",
			correction: 1.0, estimated: 1000, actual: 2000, want: 1.1,
		},
		{
			name:       "over-estimation lowers correction toward the estimate",
			correction: 1.1, estimated: 1000, actual: 500, want: 1.04,
		},
		{
			name:       "ratio is clamped at TokenCorrectionMax",
			correction: 1.0, estimated: 100, actual: 100000, want: constants.TokenCorrectionMax,
		},
		{
			// single-step EMA moves 10% of the way, so only a correction
			// already near the floor can be clamped by a tiny ratio
			name:       "ratio is clamped at TokenCorrectionMin",
			correction: 0.55, estimated: 100000, actual: 1, want: constants.TokenCorrectionMin,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := updateTokenCorrection(tc.correction, tc.estimated, tc.actual)
			if got != tc.want {
				t.Fatalf("updateTokenCorrection(%v, %d, %d) = %v, want %v",
					tc.correction, tc.estimated, tc.actual, got, tc.want)
			}
		})
	}
}

// correctionMsgs yields an over-budget history whose estimate sits between the
// correction-adjusted thresholds: 1028 tokens total at budget 800 (safety 0.8):
//   - correction 1.0 → threshold 640 → compacts
//   - correction 2.0 → threshold 320 → compacts harder
//   - correction 0.5 → threshold 1280 → lazy no-op
func correctionMsgs() []port.LLMMessage {
	msgs := []port.LLMMessage{sys("s"), usr("u")}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, asst(strings.Repeat("x", 360)))
	}
	return msgs
}

func TestCompactLoopMessagesWithPolicy_Correction(t *testing.T) {
	ctx := context.Background()
	in := correctionMsgs()
	const budget = 800
	estimate := func(msgs []port.LLMMessage) int {
		return tokenutil.EstimateMessages(toEstimate(msgs))
	}

	out05 := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 0.5, 0, nil)
	if len(out05) != len(in) {
		t.Fatalf("correction 0.5 raises threshold above estimate: expected lazy no-op, compacted %d → %d messages", len(in), len(out05))
	}

	out10 := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 1.0, 0, nil)
	if got := estimate(out10); got > int(float64(budget)*constants.LoopCompactionSafetyRatio) {
		t.Fatalf("correction 1.0: estimate = %d, want <= safety threshold %d", got, int(float64(budget)*constants.LoopCompactionSafetyRatio))
	}

	out20 := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 2.0, 0, nil)
	if got := estimate(out20); got > int(float64(budget)*constants.LoopCompactionSafetyRatio/2.0) {
		t.Fatalf("correction 2.0: estimate = %d, want <= halved threshold %d", got, int(float64(budget)*constants.LoopCompactionSafetyRatio/2.0))
	}
	if estimate(out20) >= estimate(out10) {
		t.Fatalf("correction 2.0 must compact harder than 1.0: %d vs %d", estimate(out20), estimate(out10))
	}
	assertNoOrphans(t, out20)

	// correction ≤ 0 must behave exactly like 1.0 (no correction).
	outZero := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 0, 0, nil)
	for i := range out10 {
		if outZero[i].Role != out10[i].Role || outZero[i].Content != out10[i].Content {
			t.Fatalf("correction 0 must be treated as 1.0: msg %d = %+v, want %+v", i, outZero[i], out10[i])
		}
	}
}

func TestCompactLoopMessagesWithPolicy_ZeroBudgetSkipsCorrection(t *testing.T) {
	in := correctionMsgs()
	out := compactLoopMessagesWithPolicy(context.Background(), in, Budget{HistoryCap: 0}, 0, 3, 0, 2.0, 0, nil)
	if len(out) != len(in) {
		t.Fatalf("budget 0 disables compaction regardless of correction: %d → %d", len(in), len(out))
	}
}

func TestCompactLoopMessagesWithPolicy_SafetyOverride(t *testing.T) {
	ctx := context.Background()
	// 6 assistant messages total 774 tokens: between the 0.8 threshold (640)
	// and the full-budget threshold (800), so safety 1.0 stays lazy while
	// the default compacts — a deterministic contrast.
	in := []port.LLMMessage{sys("s"), usr("u")}
	for i := 0; i < 6; i++ {
		in = append(in, asst(strings.Repeat("x", 360)))
	}
	const budget = 800

	outDefault := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 1.0, 0, nil)
	if got := tokenutil.EstimateMessages(toEstimate(outDefault)); got > int(float64(budget)*constants.LoopCompactionSafetyRatio) {
		t.Fatalf("default safety: estimate = %d, want <= %d", got, int(float64(budget)*constants.LoopCompactionSafetyRatio))
	}

	outFull := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 1.0, 1.0, nil)
	if len(outFull) != len(in) {
		t.Fatalf("safety 1.0 raises threshold above estimate: expected lazy no-op, compacted %d → %d", len(in), len(outFull))
	}

	// safety ≤ 0 falls back to the constant default, byte-identical to 0.8.
	outExplicit := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 1.0, constants.LoopCompactionSafetyRatio, nil)
	outFallback := compactLoopMessagesWithPolicy(ctx, in, Budget{HistoryCap: budget}, 0, 3, 0, 1.0, 0, nil)
	for i := range outExplicit {
		if outFallback[i].Role != outExplicit[i].Role || outFallback[i].Content != outExplicit[i].Content {
			t.Fatalf("safety 0 must fall back to default: msg %d = %+v, want %+v", i, outFallback[i], outExplicit[i])
		}
	}
}

// capGWTwoStep drives two LLM calls. The first answers with a tool call whose
// reported prompt usage doubles the dispatched estimate (under-estimation →
// correction rises); the second completes. The tool result is huge so the
// second dispatch exceeds the correction-adjusted compaction threshold.
type capGWTwoStep struct {
	llmReqs []port.LLMCapRequest
}

func (s *capGWTwoStep) Route(_ context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	if req.LLM != nil {
		s.llmReqs = append(s.llmReqs, *req.LLM)
		if len(s.llmReqs) == 1 {
			prompt := requestEstimate(*req.LLM)*2 + 1
			return port.CapabilityResponse{
				ToolCalls: []port.ToolCall{{ID: "c1", Name: "calc", Arguments: map[string]any{}}},
				Usage:     port.TokenUsage{Prompt: prompt, Total: prompt + 10},
			}, nil
		}
	}
	return port.CapabilityResponse{Content: "done"}, nil
}

// requestEstimate mirrors makeLLMNode's dispatched-request estimate: messages
// plus the JSON-encoded tools, so the test expectation stays on the same basis
// as LastEstimatedTokens.
func requestEstimate(req port.LLMCapRequest) int {
	toolTokens := 0
	if encoded, err := json.Marshal(req.Tools); err == nil {
		toolTokens = tokenutil.EstimateText(string(encoded))
	}
	return tokenutil.EstimateMessages(toEstimate(req.Messages)) + toolTokens
}

// TestReActLoop_UsageFeedbackClosesCorrectionLoop 验证 usage 反馈闭环接线：
// 第一步派发后 LastEstimatedTokens 记录实际请求的估计，LLM 成功后将
// actual/estimated 折叠进 TokenCorrection，且第二步压缩使用新 correction。
func TestReActLoop_UsageFeedbackClosesCorrectionLoop(t *testing.T) {
	stub := &capGWTwoStep{}
	cg, err := BuildReActGraph(stub, NoopTokenRecorder{}, zap.NewNop())
	require.NoError(t, err)

	// 2000-token window keeps the tool schemas (ToolsCap 80) but forces the
	// second dispatch — a huge tool result — past the correction-adjusted
	// HistoryCap threshold (≈192/correction).
	state := ReActState{
		Model:            "qwen",
		MaxContextTokens: 2000,
		Budget:           ComputeBudget(2000, 0, 0),
		Messages:         []port.LLMMessage{{Role: "user", Content: "hi"}},
		AvailableTools: []port.ToolDefinition{
			{Name: "calc", ProviderType: "mcp", ServerID: "test", Metadata: map[string]any{"risk_level": "read"}},
		},
		ToolExecutionFn: func(context.Context, port.ToolExecutionRequest) (any, error) {
			return strings.Repeat("z", 4500), nil
		},
	}
	out, err := cg.Invoke(context.Background(), state, RunConfig[ReActState]{MaxSteps: 4})
	require.NoError(t, err)
	require.Len(t, stub.llmReqs, 2)

	// 第一步：usage 报告 = 2×估计 + 1 > 派发估计 → ratio > 1 → correction
	// 上调且 > 1，与 updateTokenCorrection 对同一输入的结果一致。
	est1 := requestEstimate(stub.llmReqs[0])
	prompt1 := est1*2 + 1
	if out.TokenCorrection != updateTokenCorrection(1.0, est1, prompt1) {
		t.Fatalf("TokenCorrection = %v, want EMA(1.0, est=%d, actual=%d)", out.TokenCorrection, est1, prompt1)
	}
	if out.TokenCorrection <= 1 {
		t.Fatalf("under-estimated prompt must raise correction above 1: %v", out.TokenCorrection)
	}

	// 第二步：请求必须按新 correction 压缩（阈值 = HistoryCap·0.8/correction）。
	second := stub.llmReqs[1]
	secondEst := requestEstimate(second)
	historyCap := ComputeBudget(2000, 0, 0).HistoryCap
	if secondEst > int(float64(historyCap)*constants.LoopCompactionSafetyRatio/out.TokenCorrection) {
		t.Fatalf("second dispatch estimate = %d, want <= correction-adjusted threshold %d",
			secondEst, int(float64(historyCap)*constants.LoopCompactionSafetyRatio/out.TokenCorrection))
	}
	if out.LastEstimatedTokens != secondEst {
		t.Fatalf("LastEstimatedTokens = %d, want the dispatched request estimate %d", out.LastEstimatedTokens, secondEst)
	}
}

func TestCompactLoopMessagesWithReserve_LowersThreshold(t *testing.T) {
	ctx := context.Background()
	in := correctionMsgs()
	const budget = 800

	outNoReserve := compactLoopMessagesWithReserve(ctx, in, budget, 0, 3, nil)
	outReserved := compactLoopMessagesWithReserve(ctx, in, budget, 400, 3, nil)

	noReserve := tokenutil.EstimateMessages(toEstimate(outNoReserve))
	reserved := tokenutil.EstimateMessages(toEstimate(outReserved))
	if reserved >= noReserve {
		t.Fatalf("reserved tokens must lower the effective threshold: reserved %d >= no-reserve %d", reserved, noReserve)
	}
	// threshold = 640 − 400 = 240 → only the anchor pair survives.
	if reserved > 240 {
		t.Fatalf("reserved-400 estimate = %d, want <= 240", reserved)
	}
	assertNoOrphans(t, outReserved)
}
