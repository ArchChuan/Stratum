package application

import (
	"math"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPercentileInt64(t *testing.T) {
	tests := []struct {
		name   string
		values []int64
		p      float64
		want   float64
	}{
		{name: "single", values: []int64{42}, p: 0.95, want: 42},
		{name: "sorted ten p95", values: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, p: 0.95, want: 10},
		{name: "unsorted ten p95", values: []int64{10, 9, 8, 7, 6, 5, 4, 3, 2, 1}, p: 0.95, want: 10},
		{name: "ten p50", values: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, p: 0.5, want: 5},
		{name: "three p50 nearest rank", values: []int64{1, 2, 100}, p: 0.5, want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, percentileInt64(tc.values, tc.p))
		})
	}
}

func TestAvgInt64(t *testing.T) {
	require.Equal(t, float64(3), avgInt64([]int64{1, 2, 6}))
}

func TestAggregateRunMetrics(t *testing.T) {
	mkRun := func(results []domain.EvalCaseResult) domain.EvalRun {
		passed := 0
		for _, r := range results {
			if r.Passed {
				passed++
			}
		}
		return domain.EvalRun{
			ID:          uuid.Must(uuid.NewV7()).String(),
			TotalCases:  len(results),
			PassedCases: passed,
			CreatedAt:   time.Now().UTC(),
			Results:     results,
		}
	}
	pass := func(passed bool, tokens int, cost float64, durationMs int) domain.EvalCaseResult {
		return domain.EvalCaseResult{Passed: passed, Tokens: tokens, CostUSD: cost, DurationMs: durationMs}
	}

	t.Run("empty results only structural metrics", func(t *testing.T) {
		run := mkRun(nil)
		metrics := aggregateRunMetrics(run, runVersionAnchor{})
		require.Equal(t, 0.0, metrics["pass_rate"])
		require.Equal(t, 0, metrics["total_cases"])
		require.NotContains(t, metrics, "total_tokens")
		require.NotContains(t, metrics, "avg_latency_ms")
	})

	t.Run("no latency results omit latency metrics", func(t *testing.T) {
		run := mkRun([]domain.EvalCaseResult{pass(true, 100, 0.01, 0), pass(false, 50, 0.005, 0)})
		metrics := aggregateRunMetrics(run, runVersionAnchor{})
		require.Equal(t, 0.5, metrics["pass_rate"])
		require.Equal(t, 150, metrics["total_tokens"])
		require.Equal(t, 0.015, metrics["total_cost_usd"])
		require.Equal(t, float64(75), metrics["avg_tokens_per_case"])
		require.NotContains(t, metrics, "avg_latency_ms")
		require.NotContains(t, metrics, "p95_latency_ms")
	})

	t.Run("mixed pass and latencies", func(t *testing.T) {
		run := mkRun([]domain.EvalCaseResult{
			pass(true, 100, 0.01, 10),
			pass(false, 50, 0.005, 20),
			pass(true, 150, 0.02, 100),
		})
		metrics := aggregateRunMetrics(run, runVersionAnchor{})
		require.InDelta(t, 2.0/3.0, metrics["pass_rate"], 1e-9)
		require.Equal(t, 3, metrics["total_cases"])
		require.Equal(t, 300, metrics["total_tokens"])
		require.Equal(t, float64(100), metrics["avg_tokens_per_case"])
		require.InDelta(t, (10.0+20.0+100.0)/3.0, metrics["avg_latency_ms"], 1e-9)
		require.Equal(t, float64(100), metrics["p95_latency_ms"])
	})

	t.Run("rag evidence aggregated over evidence cases only", func(t *testing.T) {
		evidence := func(retrieved, relevant []string, precision, mrr, ndcg float64) *domain.RAGEvidenceInfo {
			return &domain.RAGEvidenceInfo{
				RetrievedDocumentIDs: retrieved,
				RelevantDocumentIDs:  relevant,
				RecallAtK:            1,
				PrecisionAtK:         precision,
				MRR:                  mrr,
				NDCGAtK:              ndcg,
			}
		}
		withEvidence := pass(true, 10, 0, 0)
		withEvidence.RAGEvidence = evidence([]string{"a", "b", "x"}, []string{"a"}, 1.0/3.0, 1, 1)
		withEvidence2 := pass(true, 10, 0, 0)
		withEvidence2.RAGEvidence = evidence([]string{"x", "a"}, []string{"a"}, 0.5, 0.5, 1.0/math.Log2(3))
		withoutEvidence := pass(false, 5, 0, 0)

		metrics := aggregateRunMetrics(
			mkRun([]domain.EvalCaseResult{withEvidence, withEvidence2, withoutEvidence}), runVersionAnchor{})
		require.InDelta(t, 1.0, metrics["avg_recall_at_5"], 1e-9)
		require.InDelta(t, (1.0/3.0+1.0/2.0)/2.0, metrics["avg_precision_at_5"], 1e-9)
		require.InDelta(t, 0.75, metrics["avg_mrr"], 1e-9)
		require.InDelta(t, (1+1.0/math.Log2(3))/2.0, metrics["avg_ndcg_at_5"], 1e-9)
		require.Equal(t, float64(2), metrics["rag_case_count"])
	})

	t.Run("no rag evidence omits rag metrics", func(t *testing.T) {
		metrics := aggregateRunMetrics(mkRun([]domain.EvalCaseResult{pass(true, 10, 0, 10)}), runVersionAnchor{})
		require.NotContains(t, metrics, "avg_recall_at_5")
		require.NotContains(t, metrics, "rag_case_count")
	})
}

func TestAggregateRunMetricsMultidimensional(t *testing.T) {
	run := domain.EvalRun{
		ID: "run-1", SuiteRevisionID: "rev-1",
		Resource: domain.ResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "s1", RevisionID: "res-v2"},
		Passed:   false, TotalCases: 2, PassedCases: 1,
		Results: []domain.EvalCaseResult{
			{Passed: false, DurationMs: 100, CostUSD: 0.01,
				Dimensions: []domain.DimensionScore{
					{Name: "faithfulness", Score: 0.4, Passed: false},
					{Name: "relevance", Score: 0.9, Passed: true},
				}},
			{Passed: true, DurationMs: 300, CostUSD: 0.03,
				Dimensions: []domain.DimensionScore{
					{Name: "faithfulness", Score: 0.8, Passed: true},
					{Name: "relevance", Score: 0.7, Passed: true},
					{Name: "completeness", Score: 0.6, Passed: true},
				}},
		},
	}
	metrics := aggregateRunMetrics(run, runVersionAnchor{
		SuiteRevisionID: "rev-1", PlatformSeq: 3, ResourceVersion: "res-v2",
	})

	if metrics["overall_pass_rate"] != 0.5 {
		t.Fatalf("overall_pass_rate = %v, want 0.5", metrics["overall_pass_rate"])
	}
	cost, ok := metrics["cost"].(map[string]any)
	if !ok || cost["total_usd"] != 0.04 || cost["avg_usd"] != 0.02 {
		t.Fatalf("cost = %+v, want {total_usd:0.04 avg_usd:0.02}", metrics["cost"])
	}
	lat, ok := metrics["latency"].(map[string]any)
	// nearest-rank percentile of [100,300]: p50=100 (rank ceil(0.5*2)=1),
	// p95=300 (rank ceil(0.95*2)=2), max=300.
	if !ok || lat["max_ms"] != 300.0 || lat["p50_ms"] != 100.0 || lat["p95_ms"] != 300.0 {
		t.Fatalf("latency = %+v, want p50=100 p95=max=300", metrics["latency"])
	}
	byDim, ok := metrics["by_dimension"].(map[string]any)
	if !ok {
		t.Fatalf("by_dimension missing: %+v", metrics)
	}
	faith := byDim["faithfulness"].(map[string]any)
	// avg_score 用容差比较：(0.4+0.8)/2 在 float64 下为 0.6000000000000001。
	if math.Abs(faith["avg_score"].(float64)-0.6) > 1e-9 || faith["pass_rate"] != 0.5 || faith["samples"] != 2 {
		t.Fatalf("faithfulness = %+v, want {avg_score:0.6 pass_rate:0.5 samples:2}", faith)
	}
	comp := byDim["completeness"].(map[string]any)
	if math.Abs(comp["avg_score"].(float64)-0.6) > 1e-9 || comp["pass_rate"] != 1.0 || comp["samples"] != 1 {
		t.Fatalf("completeness = %+v, want {avg_score:0.6 pass_rate:1 samples:1}", comp)
	}
	ver, ok := metrics["version"].(map[string]any)
	if !ok || ver["suite_revision_id"] != "rev-1" ||
		ver["platform_seq"] != int64(3) || ver["resource_version"] != "res-v2" {
		t.Fatalf("version = %+v, want {rev-1, 3, res-v2}", metrics["version"])
	}
}

func TestAggregateRunMetricsNoJudgeDimensions(t *testing.T) {
	run := domain.EvalRun{Passed: true, TotalCases: 1, PassedCases: 1,
		Results: []domain.EvalCaseResult{{Passed: true, DurationMs: 10}}}
	metrics := aggregateRunMetrics(run, runVersionAnchor{})
	if _, ok := metrics["by_dimension"].(map[string]any); ok && len(metrics["by_dimension"].(map[string]any)) != 0 {
		t.Fatalf("by_dimension should be empty for no-dimension runs: %+v", metrics["by_dimension"])
	}
	if _, ok := metrics["version"].(map[string]any); !ok {
		t.Fatalf("version must always be present: %+v", metrics)
	}
	if metrics["version"].(map[string]any)["platform_seq"] != int64(0) {
		t.Fatalf("platform_seq must be 0 (unknown) when anchor absent")
	}
}

// TestAggregateRunMetricsProcessPassRate covers the §6.5 run-level
// process_pass_rate metric: it counts evaluated results by ProcessPass with
// denominator = all evaluated results, and confirms step_judge dimensions
// (tool_pass / step_reasoning) surface in by_dimension.
func TestAggregateRunMetricsProcessPassRate(t *testing.T) {
	mkRun := func(results []domain.EvalCaseResult) domain.EvalRun {
		passed := 0
		for _, r := range results {
			if r.Passed {
				passed++
			}
		}
		return domain.EvalRun{
			ID:          uuid.Must(uuid.NewV7()).String(),
			TotalCases:  len(results),
			PassedCases: passed,
			CreatedAt:   time.Now().UTC(),
			Results:     results,
		}
	}

	t.Run("two results one process pass yields 0.5", func(t *testing.T) {
		run := mkRun([]domain.EvalCaseResult{
			{Passed: true, ProcessPass: true},
			{Passed: false, ProcessPass: false},
		})
		metrics := aggregateRunMetrics(run, runVersionAnchor{})
		require.Equal(t, 0.5, metrics["process_pass_rate"])
	})

	t.Run("process_pass_rate zero for empty results", func(t *testing.T) {
		metrics := aggregateRunMetrics(mkRun(nil), runVersionAnchor{})
		require.Equal(t, 0.0, metrics["process_pass_rate"])
	})

	t.Run("step judge dimensions surface in by_dimension", func(t *testing.T) {
		run := mkRun([]domain.EvalCaseResult{
			{Passed: true, ProcessPass: true,
				Dimensions: []domain.DimensionScore{
					{Name: "tool_pass", Score: 1.0, Passed: true},
					{Name: "step_reasoning", Score: 0.8, Passed: true},
				}},
			{Passed: false, ProcessPass: false,
				Dimensions: []domain.DimensionScore{
					{Name: "tool_pass", Score: 0.0, Passed: false},
					{Name: "step_reasoning", Score: 0.5, Passed: false},
				}},
		})
		metrics := aggregateRunMetrics(run, runVersionAnchor{})
		byDim, ok := metrics["by_dimension"].(map[string]any)
		require.True(t, ok, "by_dimension must be present")
		toolPass, ok := byDim["tool_pass"].(map[string]any)
		require.True(t, ok, "tool_pass dimension must surface")
		require.Equal(t, 2, toolPass["samples"])
		require.Equal(t, 0.5, toolPass["pass_rate"])
		require.InDelta(t, 0.5, toolPass["avg_score"], 1e-9)
		stepReasoning, ok := byDim["step_reasoning"].(map[string]any)
		require.True(t, ok, "step_reasoning dimension must surface")
		require.Equal(t, 2, stepReasoning["samples"])
		require.Equal(t, 0.5, stepReasoning["pass_rate"])
		require.InDelta(t, 0.65, stepReasoning["avg_score"], 1e-9)
	})
}
