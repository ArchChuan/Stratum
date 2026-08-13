package factcheck

import (
	"context"
	"errors"
	"testing"

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
	var queries []string
	c := New(Settings{
		Enabled: true,
		Judge:   stubJudge{verdicts: []domain.ClaimVerdict{}},
		EvidenceFn: func(_ context.Context, _ []string, query string, _ int, _ string) (port.RAGSearchEvidence, error) {
			queries = append(queries, query)
			return port.RAGSearchEvidence{Content: "evidence"}, nil
		},
		TopK: 4, MaxClaims: 10,
	})
	report := c.Check(context.Background(), domain.FactCheckInput{Output: "Hello world. Next sentence!", ViewerID: "u1"})
	require.NotNil(t, report)
	require.True(t, report.Checked)
	require.Equal(t, []string{"Hello world.", "Next sentence!"}, queries)
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
