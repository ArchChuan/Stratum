package application

import (
	"math"
	"sort"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// runVersionAnchor 是 run 的双版本锚点（spec §6.2/§3.5）。PlatformSeq 在平台
// 版本读取器未装配 / 读取失败 / 无已发布版本时为 0（fail-open，unknown 语义
// 与 ObservationService resolvePlatformVersion 一致）。
type runVersionAnchor struct {
	SuiteRevisionID string
	PlatformSeq     int64
	ResourceVersion string
}

// aggregateRunMetrics derives run-level signals from the case results after
// the case loop, before persistence. Existing top-level keys are preserved
// for backward compatibility; spec §6.2 nested structures (overall_pass_rate,
// cost/latency/by_dimension/version) are added alongside.
func aggregateRunMetrics(run domain.EvalRun, version runVersionAnchor) map[string]any {
	metrics := map[string]any{
		"pass_rate":         0.0,
		"overall_pass_rate": 0.0,
		"total_cases":       run.TotalCases,
		"cost":              map[string]any{"total_usd": 0.0, "avg_usd": 0.0},
		"latency":           map[string]any{},
		"by_dimension":      map[string]any{},
		"version": map[string]any{
			"suite_revision_id": version.SuiteRevisionID,
			"platform_seq":      version.PlatformSeq,
			"resource_version":  version.ResourceVersion,
		},
	}
	if run.TotalCases > 0 {
		metrics["pass_rate"] = float64(run.PassedCases) / float64(run.TotalCases)
		metrics["overall_pass_rate"] = metrics["pass_rate"]
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
	metrics["cost"] = map[string]any{
		"total_usd": totalCostUSD,
		"avg_usd":   totalCostUSD / float64(len(run.Results)),
	}
	if len(latencies) > 0 {
		metrics["avg_latency_ms"] = avgInt64(latencies)
		metrics["p95_latency_ms"] = percentileInt64(latencies, 0.95)
		maxLatency := latencies[0]
		for _, l := range latencies[1:] {
			if l > maxLatency {
				maxLatency = l
			}
		}
		metrics["latency"] = map[string]any{
			"p50_ms": percentileInt64(latencies, 0.50),
			"p95_ms": percentileInt64(latencies, 0.95),
			"max_ms": float64(maxLatency),
		}
	}
	metrics["by_dimension"] = aggregateByDimension(run.Results)
	for key, value := range aggregateRAGEvidence(run.Results) {
		metrics[key] = value
	}
	return metrics
}

// aggregateByDimension 按语义维度聚合 judge case 分数（spec §6.2 by_dimension）。
// 只统计带 Dimensions 的 case；未出现的维度不在结果中。samples 为该维度贡献的
// case 数，avg_score 为 Score 均值，pass_rate 为该维度 Passed 比例。
func aggregateByDimension(results []domain.EvalCaseResult) map[string]any {
	type acc struct {
		scoreSum float64
		passed   int
		samples  int
	}
	accum := make(map[string]*acc)
	for _, result := range results {
		for _, d := range result.Dimensions {
			a, ok := accum[d.Name]
			if !ok {
				a = &acc{}
				accum[d.Name] = a
			}
			a.scoreSum += d.Score
			a.samples++
			if d.Passed {
				a.passed++
			}
		}
	}
	out := make(map[string]any, len(accum))
	for name, a := range accum {
		out[name] = map[string]any{
			"avg_score": a.scoreSum / float64(a.samples),
			"pass_rate": float64(a.passed) / float64(a.samples),
			"samples":   a.samples,
		}
	}
	return out
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
