package application

import (
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
		metrics := aggregateRunMetrics(run)
		require.Equal(t, 0.0, metrics["pass_rate"])
		require.Equal(t, 0, metrics["total_cases"])
		require.NotContains(t, metrics, "total_tokens")
		require.NotContains(t, metrics, "avg_latency_ms")
	})

	t.Run("no latency results omit latency metrics", func(t *testing.T) {
		run := mkRun([]domain.EvalCaseResult{pass(true, 100, 0.01, 0), pass(false, 50, 0.005, 0)})
		metrics := aggregateRunMetrics(run)
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
		metrics := aggregateRunMetrics(run)
		require.InDelta(t, 2.0/3.0, metrics["pass_rate"], 1e-9)
		require.Equal(t, 3, metrics["total_cases"])
		require.Equal(t, 300, metrics["total_tokens"])
		require.Equal(t, float64(100), metrics["avg_tokens_per_case"])
		require.InDelta(t, (10.0+20.0+100.0)/3.0, metrics["avg_latency_ms"], 1e-9)
		require.Equal(t, float64(100), metrics["p95_latency_ms"])
	})
}
