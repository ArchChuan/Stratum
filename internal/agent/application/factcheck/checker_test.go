package factcheck

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

// P3 幻觉校验编排 fail-closed 与降级语义：开关关 / 空 viewerID 不触发任何
// RAG 检索（SystemActor 旁路防护，不依赖 rag fn 内部 D2 条件）；检索/judge
// 失败降级「不校验」（nil），绝不阻塞 agent 执行。
func TestChecker_FailClosed(t *testing.T) {
	evidenceCalls := 0
	evidenceFn := func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
		evidenceCalls++
		return port.RAGSearchEvidence{Content: "evidence"}, nil
	}

	t.Run("disabled returns nil and never queries", func(t *testing.T) {
		c := New(Settings{Enabled: false, Judge: stubJudge{}, EvidenceFn: evidenceFn, TopK: 4, MaxClaims: 10})
		report := c.Check(context.Background(), domain.FactCheckInput{Output: "Claim one. Claim two.", ViewerID: "u1"})
		require.Nil(t, report)
		require.Zero(t, evidenceCalls)
	})

	t.Run("empty viewerID returns nil and never queries", func(t *testing.T) {
		c := New(Settings{Enabled: true, Judge: stubJudge{}, EvidenceFn: evidenceFn, TopK: 4, MaxClaims: 10})
		report := c.Check(context.Background(), domain.FactCheckInput{Output: "Claim one.", ViewerID: ""})
		require.Nil(t, report)
		require.Zero(t, evidenceCalls)
	})

	t.Run("missing judge returns nil", func(t *testing.T) {
		c := New(Settings{Enabled: true, EvidenceFn: evidenceFn, TopK: 4, MaxClaims: 10})
		report := c.Check(context.Background(), domain.FactCheckInput{Output: "Claim one.", ViewerID: "u1"})
		require.Nil(t, report)
	})
}

func TestChecker_DegradesOnDependencyFailure(t *testing.T) {
	t.Run("evidence error degrades to nil", func(t *testing.T) {
		c := New(Settings{
			Enabled: true,
			Judge:   stubJudge{},
			EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
				return port.RAGSearchEvidence{}, errors.New("rag down")
			},
			TopK: 4, MaxClaims: 10,
		})
		report := c.Check(context.Background(), domain.FactCheckInput{Output: "Claim one.", ViewerID: "u1"})
		require.Nil(t, report)
	})

	t.Run("empty evidence degrades to nil", func(t *testing.T) {
		c := New(Settings{
			Enabled: true,
			Judge:   stubJudge{},
			EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
				return port.RAGSearchEvidence{Content: "  "}, nil
			},
			TopK: 4, MaxClaims: 10,
		})
		report := c.Check(context.Background(), domain.FactCheckInput{Output: "Claim one.", ViewerID: "u1"})
		require.Nil(t, report)
	})

	t.Run("judge error degrades to nil", func(t *testing.T) {
		c := New(Settings{
			Enabled: true,
			Judge:   stubJudge{err: errors.New("judge down")},
			EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
				return port.RAGSearchEvidence{Content: "evidence"}, nil
			},
			TopK: 4, MaxClaims: 10,
		})
		report := c.Check(context.Background(), domain.FactCheckInput{Output: "Claim one.", ViewerID: "u1"})
		require.Nil(t, report)
	})

	t.Run("context cancelled degrades to nil", func(t *testing.T) {
		c := New(Settings{
			Enabled: true,
			Judge:   stubJudge{},
			EvidenceFn: func(ctx context.Context, _ []string, _ string, _ int, _ string) (port.RAGSearchEvidence, error) {
				<-ctx.Done()
				return port.RAGSearchEvidence{}, ctx.Err()
			},
			TopK: 4, MaxClaims: 10,
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		report := c.Check(ctx, domain.FactCheckInput{Output: "Claim one.", ViewerID: "u1"})
		require.Nil(t, report)
	})
}

func TestChecker_SummarizeVerdicts(t *testing.T) {
	c := New(Settings{
		Enabled: true,
		Judge: stubJudge{verdicts: []domain.ClaimVerdict{
			{Text: "supported", Verdict: VerdictSupported, Risk: 0},
			{Text: "contradicted", Verdict: VerdictContradicted, Risk: 4},
		}},
		EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		TopK: 4, MaxClaims: 10,
	})
	report := c.Check(context.Background(), domain.FactCheckInput{Output: "Claim one.", ViewerID: "u1"})
	require.NotNil(t, report)
	require.True(t, report.Checked)
	require.False(t, report.IsValid) // 任一 CONTRADICTED → 整体无效
	require.Equal(t, 1, report.RiskPoints)
}

func TestChecker_AllSupportedIsValid(t *testing.T) {
	c := New(Settings{
		Enabled: true,
		Judge: stubJudge{verdicts: []domain.ClaimVerdict{
			{Text: "a", Verdict: VerdictSupported, Risk: 0},
			{Text: "b", Verdict: VerdictSupported, Risk: 1},
		}},
		EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		TopK: 4, MaxClaims: 10,
	})
	report := c.Check(context.Background(), domain.FactCheckInput{Output: "A. B.", ViewerID: "u1"})
	require.NotNil(t, report)
	require.True(t, report.IsValid)
	require.Zero(t, report.RiskPoints)
}

// 英文输出按句拆分（Latin 边界 bug 回归）："Hello world. Next sentence." 应
// 拆为 2 个 claim，EvidenceFn 逐句调用，per-claim RAG 才有意义。
func TestChecker_SplitsEnglishClaims(t *testing.T) {
	// 证据检索已并行化（任务 #3）：EvidenceFn 调用顺序无保证，按集合断言。
	var mu sync.Mutex
	var queries []string
	c := New(Settings{
		Enabled: true,
		Judge:   stubJudge{verdicts: []domain.ClaimVerdict{}},
		EvidenceFn: func(_ context.Context, _ []string, query string, _ int, _ string) (port.RAGSearchEvidence, error) {
			mu.Lock()
			queries = append(queries, query)
			mu.Unlock()
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		TopK: 4, MaxClaims: 10,
	})
	report := c.Check(context.Background(), domain.FactCheckInput{Output: "Hello world. Next sentence!", ViewerID: "u1"})
	require.NotNil(t, report)
	require.True(t, report.Checked)
	require.ElementsMatch(t, []string{"Hello world.", "Next sentence!"}, queries)
}

// extractClaims 清洗：模型常把工具调用写成 markdown code fence 块而非真正
// 执行；这些噪声既非事实断言，又会挤占 MaxClaims 配额把后续真实断言截掉。
func TestExtractClaims_StripsCodeFenceBlocks(t *testing.T) {
	output := "我需要检索信息。\n\n```json\n{\n  \"tool_code\": \"stratum_search_knowledge\",\n  \"parameters\": {\"query\": \"总线\"}\n}\n```\n\n根据结果，消息总线基于 Kafka。"
	got := extractClaims(output, 10)
	require.Equal(t, []string{"我需要检索信息。", "根据结果，消息总线基于 Kafka。"}, got)
}

func TestExtractClaims_SingleLineToolCallFiltered(t *testing.T) {
	got := extractClaims(`Stratum 基于 NATS。stratum_search_knowledge("总线")`, 10)
	require.Equal(t, []string{"Stratum 基于 NATS。"}, got)
}

func TestExtractClaims_MaxClaimsTruncates(t *testing.T) {
	got := extractClaims("一。二。三。四。五。", 3)
	require.Equal(t, []string{"一。", "二。", "三。"}, got)
}

// 输出仅含非事实内容（code fence 工具块）时 claims 全被剥：返回空校验报告
// （Checked=true 表示已执行校验）且不触发任何检索。
func TestChecker_AllClaimsFiltered(t *testing.T) {
	evidenceCalls := 0
	c := New(Settings{
		Enabled: true,
		Judge:   stubJudge{},
		EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
			evidenceCalls++
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		TopK: 4, MaxClaims: 10,
	})
	report := c.Check(context.Background(), domain.FactCheckInput{
		Output:   "```json\n{\"tool_code\": \"stratum_search_knowledge\"}\n```",
		ViewerID: "u1",
	})
	require.NotNil(t, report)
	require.True(t, report.Checked)
	require.True(t, report.IsValid)
	require.Zero(t, evidenceCalls)
}

// gatherEvidence 并行化（任务 #3）：检索有界并发且结果按 claim 索引保序。
// EvidenceFn 完成顺序被打乱（延迟与 query 长度相关，慢的排在前面）时，
// 拼接顺序仍与 claims 输入一致。
func TestChecker_GatherEvidenceParallelPreservesOrder(t *testing.T) {
	claims := []string{"五。", "四。", "三。", "二。", "一。"}
	c := &checker{settings: Settings{
		Enabled: true,
		Judge:   stubJudge{verdicts: []domain.ClaimVerdict{}},
		EvidenceFn: func(_ context.Context, _ []string, query string, _ int, _ string) (port.RAGSearchEvidence, error) {
			// 让前面的 claim 更慢，保证完成顺序 ≠ 输入顺序。
			time.Sleep(time.Duration(len(query)) * 2 * time.Millisecond)
			return port.RAGSearchEvidence{Content: "evidence-for:" + query}, nil
		},
		TopK: 4, MaxClaims: 10,
	}}
	got, err := c.gatherEvidence(context.Background(), domain.FactCheckInput{ViewerID: "u1"}, claims)
	require.NoError(t, err)
	segments := strings.Split(got, "\n\n")
	require.Len(t, segments, len(claims))
	for i, seg := range segments {
		require.True(t, strings.HasPrefix(seg, fmt.Sprintf("[claim %d]", i)), "段顺序错乱: %q", seg)
	}
}

// gatherEvidence 并发上界：活跃检索数不得超过信号量
// AgentFactCheckEvidenceConcurrency（=3），且确实发生并发（peak≥2）。
func TestChecker_GatherEvidenceBoundsConcurrency(t *testing.T) {
	claims := make([]string, 10)
	for i := range claims {
		claims[i] = fmt.Sprintf("claim-%d。", i)
	}
	var mu sync.Mutex
	active, peak := 0, 0
	c := &checker{settings: Settings{
		Enabled: true,
		Judge:   stubJudge{},
		EvidenceFn: func(_ context.Context, _ []string, _ string, _ int, _ string) (port.RAGSearchEvidence, error) {
			mu.Lock()
			active++
			if active > peak {
				peak = active
			}
			mu.Unlock()
			time.Sleep(5 * time.Millisecond) // 拉长活跃窗口，让并发有机会堆起来
			mu.Lock()
			active--
			mu.Unlock()
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		TopK: 4, MaxClaims: 10,
	}}
	_, err := c.gatherEvidence(context.Background(), domain.FactCheckInput{ViewerID: "u1"}, claims)
	require.NoError(t, err)
	require.GreaterOrEqual(t, peak, 2, "期望观察到并发执行")
	require.LessOrEqual(t, peak, 3, "并发不得超过信号量上界")
}

// 对账轨（任务 #4）：CitationVerify 单独开启时无需 Judge/EvidenceFn/viewerID，
// 纯代码级核验最终输出中的引用声称。
func TestChecker_CitationOnlyRunsReconciliation(t *testing.T) {
	observations := []domain.ToolObservation{obs("call-1", domain.ToolTraceStatusSuccess, "")}
	c := New(Settings{CitationVerify: true}) // Judge/EvidenceFn 均缺省
	report := c.Check(context.Background(), domain.FactCheckInput{
		Output:           "已删除订单 <tool_ref:call-1>。",
		ViewerID:         "u1",
		ToolObservations: observations,
	})
	require.NotNil(t, report)
	require.True(t, report.Checked)
	require.True(t, report.IsValid)
	require.Len(t, report.ToolReferences, 1)
	require.Equal(t, ClassVerified, report.ToolReferences[0].Classification)
	require.Zero(t, report.RiskPoints)
}

// 对账判定影响整体 IsValid：invalid_reference 引用 → 整体无效 + RiskPoints 累计。
func TestChecker_CitationFailureInvalidatesReport(t *testing.T) {
	c := New(Settings{CitationVerify: true})
	report := c.Check(context.Background(), domain.FactCheckInput{
		Output:           "已删除订单 <tool_ref:call-ghost>。",
		ViewerID:         "u1",
		ToolObservations: []domain.ToolObservation{obs("call-1", domain.ToolTraceStatusSuccess, "")},
	})
	require.NotNil(t, report)
	require.False(t, report.IsValid)
	require.Equal(t, 1, report.RiskPoints)
	require.Len(t, report.ToolReferences, 1)
	require.Equal(t, ClassInvalidReference, report.ToolReferences[0].Classification)
}

// 双轨叠加：judge 判定与对账结果合并到同一报告，RiskPoints 各自累计。
func TestChecker_DualTrackMergesIntoOneReport(t *testing.T) {
	observations := []domain.ToolObservation{obs("call-1", domain.ToolTraceStatusSuccess, "")}
	c := New(Settings{
		Enabled: true,
		Judge: stubJudge{verdicts: []domain.ClaimVerdict{
			{Text: "A", Verdict: VerdictContradicted, Risk: 4},
		}},
		EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		CitationVerify: true,
		TopK:           4, MaxClaims: 10,
	})
	report := c.Check(context.Background(), domain.FactCheckInput{
		Output:           "A。已删除订单 <tool_ref:call-1>。",
		ViewerID:         "u1",
		ToolObservations: observations,
	})
	require.NotNil(t, report)
	require.False(t, report.IsValid) // judge 判 CONTRADICTED → 整体无效
	require.Len(t, report.Claims, 1)
	require.Len(t, report.ToolReferences, 1)
	require.Equal(t, ClassVerified, report.ToolReferences[0].Classification)
	require.Equal(t, 1, report.RiskPoints) // judge 1 条风险，对账 verified 无风险
}

// judge 轨依赖失败时对账结果必须保留（对账是独立事实核验，不被 judge 吞掉）。
func TestChecker_JudgeFailureKeepsReconciliation(t *testing.T) {
	observations := []domain.ToolObservation{obs("call-1", domain.ToolTraceStatusSuccess, "")}
	c := New(Settings{
		Enabled: true,
		Judge:   stubJudge{err: errors.New("judge down")},
		EvidenceFn: func(context.Context, []string, string, int, string) (port.RAGSearchEvidence, error) {
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		CitationVerify: true,
		TopK:           4, MaxClaims: 10,
	})
	report := c.Check(context.Background(), domain.FactCheckInput{
		Output:           "已删除订单 <tool_ref:call-1>。",
		ViewerID:         "u1",
		ToolObservations: observations,
	})
	require.NotNil(t, report, "judge 失败时对账结果必须保留")
	require.Len(t, report.ToolReferences, 1)
	require.Equal(t, ClassVerified, report.ToolReferences[0].Classification)
}

type stubJudge struct {
	verdicts []domain.ClaimVerdict
	err      error
}

func (j stubJudge) JudgeClaims(context.Context, []string, string) ([]domain.ClaimVerdict, error) {
	if j.err != nil {
		return nil, j.err
	}
	return j.verdicts, nil
}
