package application

import (
	"context"
	"errors"
	"testing"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"go.uber.org/zap"
)

type stubSufficiencyJudge struct {
	verdict          knowledgeport.SufficiencyVerdict
	err              error
	lastInstructions string
}

func (s *stubSufficiencyJudge) JudgeSufficiency(_ context.Context, _, _, instructions string) (knowledgeport.SufficiencyVerdict, error) {
	s.lastInstructions = instructions
	return s.verdict, s.err
}

func gateResult() *RAGQueryResult {
	return &RAGQueryResult{
		Sources:        []Source{{DocumentID: "d1", Content: "c1", Score: 0.8}},
		BestScore:      0.8,
		CandidateCount: 3,
	}
}

func TestJudgeSufficiencyGate(t *testing.T) {
	judge := func(j knowledgeport.SufficiencyJudge) *RAGService {
		rs := NewRAGService(nil, nil, zap.NewNop())
		rs.SetSufficiencyJudgeResolver(func(_ context.Context, _ string) (knowledgeport.SufficiencyJudge, error) { return j, nil })
		return rs
	}

	cases := []struct {
		name       string
		rs         *RAGService
		result     *RAGQueryResult
		wantAnswer bool
		wantReason NoAnswerReason
	}{
		{
			name:       "nil judge 未装配 fail-closed 放行",
			rs:         NewRAGService(nil, nil, zap.NewNop()),
			wantAnswer: true,
		},
		{
			name:       "sufficient 放行",
			rs:         judge(&stubSufficiencyJudge{verdict: knowledgeport.SufficiencySufficient}),
			wantAnswer: true,
		},
		{
			name:       "insufficient 升级为 insufficient_evidence 且清空 sources",
			rs:         judge(&stubSufficiencyJudge{verdict: knowledgeport.SufficiencyInsufficient}),
			wantAnswer: false,
			wantReason: NoAnswerInsufficientEvidence,
		},
		{
			name:       "judge 失败降级放行",
			rs:         judge(&stubSufficiencyJudge{err: errors.New("timeout")}),
			wantAnswer: true,
		},
		{
			name: "空 sources 不判（no_sources 已是更强信号）",
			rs:   judge(&stubSufficiencyJudge{verdict: knowledgeport.SufficiencyInsufficient}),
			result: &RAGQueryResult{
				NoAnswer:       buildNoAnswer(NoAnswerNoSources, 0, 0, 0),
				BestScore:      0,
				CandidateCount: 0,
			},
			wantAnswer: false,
			wantReason: NoAnswerNoSources,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.result == nil {
				tc.result = gateResult()
			}
			got := tc.rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "qwen-turbo", "测试指令", tc.result)
			if tc.wantAnswer {
				if got.NoAnswer != nil || len(got.Sources) == 0 {
					t.Errorf("expected pass-through (sources kept), got NoAnswer=%v sources=%d", got.NoAnswer, len(got.Sources))
				}
				return
			}
			if len(got.Sources) != 0 {
				t.Errorf("expected sources cleared, got %d", len(got.Sources))
			}
			if got.NoAnswer == nil {
				t.Fatal("expected NoAnswer signal")
			}
			if got.NoAnswer.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", got.NoAnswer.Reason, tc.wantReason)
			}
			if formatSources(got.Sources) == "" && got.NoAnswer == nil {
				t.Error("invariant broken: empty content without NoAnswer")
			}
		})
	}
}

func TestJudgeSufficiencyGatePreservesStats(t *testing.T) {
	rs := NewRAGService(nil, nil, zap.NewNop())
	rs.SetSufficiencyJudgeResolver(func(_ context.Context, _ string) (knowledgeport.SufficiencyJudge, error) {
		return &stubSufficiencyJudge{verdict: knowledgeport.SufficiencyInsufficient}, nil
	})
	got := rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "qwen-turbo", "", gateResult())
	if got.BestScore != 0.8 || got.CandidateCount != 3 {
		t.Errorf("stats lost: BestScore=%v CandidateCount=%d, want 0.8/3", got.BestScore, got.CandidateCount)
	}
	if got.NoAnswer.BestScore != 0.8 || got.NoAnswer.RetrievedCount != 3 {
		t.Errorf("NoAnswer stats wrong: BestScore=%v RetrievedCount=%d", got.NoAnswer.BestScore, got.NoAnswer.RetrievedCount)
	}
}

func TestJudgeSufficiencyGateModelAndResolverPaths(t *testing.T) {
	rs := NewRAGService(nil, nil, zap.NewNop())
	rs.SetSufficiencyJudgeResolver(func(_ context.Context, model string) (knowledgeport.SufficiencyJudge, error) {
		if model != "qwen-turbo" {
			return nil, errors.New("model not in chat catalogue")
		}
		return &stubSufficiencyJudge{verdict: knowledgeport.SufficiencyInsufficient}, nil
	})

	t.Run("空 model 短路放行（judge 门关闭）", func(t *testing.T) {
		got := rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "", "", gateResult())
		if len(got.Sources) == 0 || got.NoAnswer != nil {
			t.Fatalf("empty model must pass through, got NoAnswer=%v", got.NoAnswer)
		}
	})
	t.Run("resolver 失败 fail-closed 放行", func(t *testing.T) {
		got := rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "qwen-max", "", gateResult())
		if len(got.Sources) == 0 || got.NoAnswer != nil {
			t.Fatalf("resolver failure must pass through, got NoAnswer=%v", got.NoAnswer)
		}
	})
	t.Run("insufficient 升级 insufficient_evidence", func(t *testing.T) {
		got := rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "qwen-turbo", "", gateResult())
		if len(got.Sources) != 0 || got.NoAnswer == nil || got.NoAnswer.Reason != NoAnswerInsufficientEvidence {
			t.Fatalf("want insufficient_evidence, got sources=%d NoAnswer=%+v", len(got.Sources), got.NoAnswer)
		}
	})
}
