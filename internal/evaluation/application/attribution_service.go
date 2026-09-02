package application

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// AttributionReport is the structured output of a §9 parameter-attribution
// pass over a real (failed) evaluation run.
//
// It splits attribution into three layers:
//   - FailureSummaries / Clusters: which cases failed and under which primary
//     attribution key (output reason, process assertion, execution error).
//   - Diagnosis: the rule-based Diagnoser's classification of those failures
//     into tunable categories with root-cause hypotheses and confidence.
//   - TunableDirections: the concrete agent parameters worth adjusting, all of
//     which lie inside the optimizable search-space allowlist.
//   - EvalChainDirections: failures attributable to the evaluation harness
//     itself (judge dimension scoring, process assertions) map to the
//     platform-scoped evaluation.* parameters — declared tunable but NOT in
//     the automatic search space, so a harness-quality problem is never
//     "fixed" by grid-searching the agent under evaluation.
type AttributionReport struct {
	Resource    domain.ResourceRef
	RunID       string
	TotalCases  int
	FailedCases int
	// Advanceable is true when attribution produced at least one direction
	// (tunable or eval-chain) worth acting on. It is the clean signal a
	// caller uses to decide whether to enter the improvement loop.
	Advanceable bool
	// FailureSummaries are the run-bridge summaries consumed by the
	// optimizer pipeline's DIAGNOSE/PROPOSE stages.
	FailureSummaries    []domain.FailureSummary
	Diagnosis           domain.DiagnosisReport
	Clusters            []FailureCluster
	TunableDirections   []TunableDirection
	EvalChainDirections []EvalChainDirection
}

// FailureCluster groups failed cases by their primary attribution key.
type FailureCluster struct {
	Reason string   `json:"reason"`
	Count  int      `json:"count"`
	Cases  []string `json:"cases"`
}

// TunableDirection names an agent parameter worth adjusting and why.
type TunableDirection struct {
	Key       string                 `json:"key"`
	Category  domain.TunableCategory `json:"category,omitempty"`
	Direction string                 `json:"direction"`
}

// EvalChainDirection names a platform-scoped evaluation parameter to review
// when the harness — not the agent — is the suspected failure source.
type EvalChainDirection struct {
	PlatformKey string `json:"platform_key"`
	Reason      string `json:"reason"`
	Direction   string `json:"direction"`
}

// AttributionService converts a completed evaluation run into the structured
// §9 attribution report that drives the improvement loop. Rule-based
// classification always runs (no LLM in the hot path); LLM hypothesis
// enhancement is deliberately out of scope for the minimal landing.
type AttributionService struct {
	diagnoser *Diagnoser
}

func NewAttributionService() *AttributionService {
	return &AttributionService{diagnoser: NewDiagnoser(nil)}
}

// AnalyzeRun attributes the failed cases of run. A run with no failures
// yields an empty report (no clusters, no diagnosis) rather than an error.
func (s *AttributionService) AnalyzeRun(
	ctx context.Context, run domain.EvalRun,
) (AttributionReport, error) {
	summaries := domain.FailedCaseSummaries(run)
	report := AttributionReport{
		Resource:         run.Resource,
		RunID:            run.ID,
		TotalCases:       run.TotalCases,
		FailedCases:      len(summaries),
		FailureSummaries: summaries,
	}
	if len(summaries) == 0 {
		return report, nil
	}

	failed := failedResults(run.Results)
	diagnosis, err := s.diagnoser.Diagnose(ctx, summaries, failed)
	if err != nil {
		return AttributionReport{}, fmt.Errorf("attribute run %s: %w", run.ID, err)
	}
	report.Diagnosis = diagnosis
	report.Clusters = buildFailureClusters(failed)
	report.TunableDirections = buildTunableDirections(diagnosis)
	report.EvalChainDirections = buildEvalChainDirections(report.Clusters)
	report.Advanceable = len(report.TunableDirections) > 0 ||
		len(report.EvalChainDirections) > 0
	return report, nil
}

// failedResults narrows the case results to the failed set so DIAGNOSE
// attribution signals (e.g. security-violation trace counts) match the
// failure summaries being classified.
func failedResults(results []domain.EvalCaseResult) []domain.EvalCaseResult {
	out := make([]domain.EvalCaseResult, 0, len(results))
	for _, cr := range results {
		if !cr.Passed {
			out = append(out, cr)
		}
	}
	return out
}

// primaryFailureReason picks the most actionable attribution key of a case:
// the output failure_reason wins over the process assertion, which wins over
// the bare execution error.
func primaryFailureReason(cr domain.EvalCaseResult) string {
	switch {
	case cr.FailureReason != "":
		return cr.FailureReason
	case cr.ProcessFailure != "":
		return cr.ProcessFailure
	case cr.Error != "":
		return "execution"
	default:
		return "unknown"
	}
}

// buildFailureClusters groups the failed cases by their primary attribution
// key (output reason → process assertion → execution error), with stable
// ordering and case-name evidence.
func buildFailureClusters(results []domain.EvalCaseResult) []FailureCluster {
	byReason := map[string]*FailureCluster{}
	order := make([]string, 0, len(results))
	for _, cr := range results {
		reason := primaryFailureReason(cr)
		cluster, ok := byReason[reason]
		if !ok {
			cluster = &FailureCluster{Reason: reason}
			byReason[reason] = cluster
			order = append(order, reason)
		}
		cluster.Count++
		cluster.Cases = append(cluster.Cases, cr.CaseID)
	}
	clusters := make([]FailureCluster, 0, len(order))
	for _, reason := range order {
		clusters = append(clusters, *byReason[reason])
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Reason < clusters[j].Reason })
	return clusters
}

// buildTunableDirections maps the diagnosis' affected tunables to actionable
// adjustment directions. Only keys the Diagnoser surfaced (all inside the
// allowlist) are suggested; every key carries a concrete human direction.
func buildTunableDirections(diagnosis domain.DiagnosisReport) []TunableDirection {
	seen := map[string]bool{}
	out := make([]TunableDirection, 0, len(diagnosis.AffectedTunables))
	for _, key := range diagnosis.AffectedTunables {
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, TunableDirection{
			Key:       key,
			Category:  tunableCategory(key),
			Direction: tunableDirectionText(key),
		})
	}
	return out
}

// tunableCategory looks up a tunable's category from the domain registry
// (empty when the key is not a registered grid-searchable tunable).
func tunableCategory(key string) domain.TunableCategory {
	t := domain.NewTunableRegistry().Get(key)
	if t == nil {
		return ""
	}
	return t.Category()
}

// buildEvalChainDirections detects harness-attributable failure clusters and
// maps them onto the platform-scoped evaluation.* parameters. These are the
// §9.2 tunables declared optimizable at the platform page but deliberately
// excluded from the automatic agent search space.
func buildEvalChainDirections(clusters []FailureCluster) []EvalChainDirection {
	var out []EvalChainDirection
	for _, c := range clusters {
		switch {
		case strings.HasPrefix(c.Reason, "dimension:"):
			out = append(out, EvalChainDirection{
				PlatformKey: "evaluation.judge.temperature",
				Reason:      c.Reason,
				Direction: "维度失败可能是 LLM judge 自身波动：先对 golden 做一致性校准（≥90%），" +
					"再调低 judge 采样温度以降低判定方差；在 judge 可信前勿把维度失败当 agent 缺陷",
			})
		case strings.HasPrefix(c.Reason, "process:must_not_call:"):
			out = append(out, EvalChainDirection{
				PlatformKey: "evaluation.ruleguard.enabled",
				Reason:      c.Reason,
				Direction: "禁用工具被实际调用：仅过程断言事后告警不够，建议开启规则护栏 denylist，" +
					"在执行期即时硬拦截而非仅标记失败",
			})
		}
	}
	return out
}

// tunableDirectionText returns the human-facing adjustment direction for a
// tunable key. Keys the Diagnoser surfaces are all allowlist members; the
// fallback keeps unknown keys readable instead of silently dropping them.
func tunableDirectionText(key string) string {
	if text, ok := directionText[key]; ok {
		return text
	}
	return fmt.Sprintf("调整 %s 以针对诊断出的失败模式", key)
}

var directionText = map[string]string{
	"temperature":              "降低温度以获得更确定性的输出",
	"max_tokens":               "增大 max_tokens 预算以容纳被截断的长输出",
	"maxTokens":                "增大 maxTokens 预算以容纳被截断的长输出",
	"max_context_tokens":       "增大上下文窗口预算以缓解长上下文截断",
	"max_iterations":           "增加迭代上限以容纳多步规划的收敛",
	"max_retries":              "增加重试次数以容忍瞬时工具/网关失败",
	"timeout_ms":               "增大超时预算以覆盖慢模型/慢工具响应",
	"top_k":                    "调整 top_k 检索窗口以平衡召回与精确",
	"score_threshold":          "调整 score_threshold 过滤阈值以改善检索相关性",
	"query_rewrite":            "启用查询改写以提升检索命中质量",
	"reranking":                "启用重排序以提升检索结果相关性",
	"reasoning_effort":         "调整 reasoning_effort 以平衡推理深度与成本",
	"model":                    "评估是否更换更适配该失败模式的模型",
	"system_prompt":            "优化系统提示词以约束输出格式与安全边界",
	"instructions":             "完善 agent 指令以约束行为边界",
	"memory_extraction_prompt": "优化记忆抽取提示词以减少误抽取",
	"memory_summary_prompt":    "优化记忆摘要提示词以保留关键事实",
	"memory_enrichment_prompt": "优化记忆富化提示词以提升上下文质量",
}
