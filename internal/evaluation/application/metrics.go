package application

import (
	"math"
	"sort"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// aggregateRunMetrics derives run-level signals from the case results after
// the case loop, before persistence. Tool call counts require trace-level
// data that case results do not carry today; they are added when the trace
// reader exposes them (Phase 3).
func aggregateRunMetrics(run domain.EvalRun) map[string]any {
	metrics := map[string]any{
		"pass_rate":   0.0,
		"total_cases": run.TotalCases,
	}
	if run.TotalCases > 0 {
		metrics["pass_rate"] = float64(run.PassedCases) / float64(run.TotalCases)
	}
	if len(run.Results) == 0 {
		return metrics
	}

	var totalTokens int
	var totalCostUSD float64
	latencies := make([]int64, 0, len(run.Results))
	for _, result := range run.Results {
		totalTokens += result.Tokens
		totalCostUSD += result.CostUSD
		if result.DurationMs > 0 {
			latencies = append(latencies, int64(result.DurationMs))
		}
	}
	metrics["total_tokens"] = totalTokens
	metrics["total_cost_usd"] = totalCostUSD
	metrics["avg_tokens_per_case"] = float64(totalTokens) / float64(len(run.Results))
	if len(latencies) > 0 {
		metrics["avg_latency_ms"] = avgInt64(latencies)
		metrics["p95_latency_ms"] = percentileInt64(latencies, 0.95)
	}
	for key, value := range aggregateRAGEvidence(run.Results) {
		metrics[key] = value
	}
	return metrics
}

// aggregateRAGEvidence averages the per-case retrieval metrics of a run over
// the cases that carry evidence; runs without knowledge evidence return nil.
// The at_5 rank window mirrors knowledge/application.RetrievalK.
func aggregateRAGEvidence(results []domain.EvalCaseResult) map[string]float64 {
	var recall, precision, mrr, ndcg float64
	n := 0
	for _, result := range results {
		if result.RAGEvidence == nil {
			continue
		}
		recall += result.RAGEvidence.RecallAtK
		precision += result.RAGEvidence.PrecisionAtK
		mrr += result.RAGEvidence.MRR
		ndcg += result.RAGEvidence.NDCGAtK
		n++
	}
	if n == 0 {
		return nil
	}
	count := float64(n)
	return map[string]float64{
		"avg_recall_at_5":    recall / count,
		"avg_precision_at_5": precision / count,
		"avg_mrr":            mrr / count,
		"avg_ndcg_at_5":      ndcg / count,
		"rag_case_count":     float64(n),
	}
}

func avgInt64(values []int64) float64 {
	var sum int64
	for _, v := range values {
		sum += v
	}
	return float64(sum) / float64(len(values))
}

// percentileInt64 returns the nearest-rank percentile: sorted ascending,
// rank = ceil(p*len), index = rank-1. Callers must pass a non-empty slice.
func percentileInt64(values []int64, p float64) float64 {
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	index := int(math.Ceil(p*float64(len(sorted)))) - 1
	if index < 0 {
		index = 0
	}
	return float64(sorted[index])
}
