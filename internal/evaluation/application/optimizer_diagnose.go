package application

import (
	"context"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// Diagnoser analyses evaluation failures and produces a DiagnosisReport
// classifying them by tunable category with root cause hypotheses.
type Diagnoser struct {
	// llmEnhancer is an optional LLM-backed enhancer for ambiguous cases.
	// When nil, only rule-based classification is used.
	llmEnhancer LLMDiagnosisEnhancer
}

// LLMDiagnosisEnhancer optionally augments rule-based diagnosis with
// LLM-driven root cause analysis for ambiguous failure patterns.
type LLMDiagnosisEnhancer interface {
	EnhanceDiagnosis(ctx context.Context, failures []domain.FailureSummary,
		rules DiagnosisReport) (DiagnosisReport, error)
}

func NewDiagnoser(enhancer LLMDiagnosisEnhancer) *Diagnoser {
	return &Diagnoser{llmEnhancer: enhancer}
}

// Diagnose classifies evaluation failures into a DiagnosisReport.
// Rule-based classification always runs; LLM enhancement is best-effort
// and only invoked when an enhancer is configured.
func (d *Diagnoser) Diagnose(
	ctx context.Context,
	failures []domain.FailureSummary,
	caseResults []domain.EvalCaseResult,
) (DiagnosisReport, error) {
	if len(failures) == 0 {
		return DiagnosisReport{}, nil
	}

	report := classifyFailures(failures)
	report = deriveAffectedTunables(report)
	collectTraceSignals(caseResults, &report)

	if d.llmEnhancer != nil {
		enhanced, err := d.llmEnhancer.EnhanceDiagnosis(ctx, failures, report)
		if err != nil {
			return report, nil // Best-effort: return rule-based result.
		}
		mergeLLMHypotheses(enhanced, &report)
	}

	return report, nil
}

// classifyFailures maps each failure to a tunable category via keyword heuristics
// and produces hypotheses sorted by confidence with per-category deduplication.
func classifyFailures(failures []domain.FailureSummary) DiagnosisReport {
	report := DiagnosisReport{
		TotalFailures:     len(failures),
		CategoryBreakdown: make(map[domain.TunableCategory]int),
	}

	for _, f := range failures {
		category, confidence, desc := classifyFailure(f)
		report.CategoryBreakdown[category]++

		if confidence >= 0.5 {
			report.RootCauseHypotheses = append(report.RootCauseHypotheses,
				domain.RootCauseHypothesis{
					Category:    category,
					Description: desc,
					Confidence:  confidence,
					Evidence:    []string{f.CaseName},
					Source:      "rule",
				})
		}
	}

	sort.Slice(report.RootCauseHypotheses, func(i, j int) bool {
		return report.RootCauseHypotheses[i].Confidence >
			report.RootCauseHypotheses[j].Confidence
	})

	report.RootCauseHypotheses = mergeHypotheses(report.RootCauseHypotheses)
	return report
}

// deriveAffectedTunables maps top hypotheses to concrete tunable keys.
func deriveAffectedTunables(report DiagnosisReport) DiagnosisReport {
	for _, h := range report.RootCauseHypotheses {
		report.AffectedTunables = append(report.AffectedTunables,
			categoryToTunables(h.Category)...)
	}
	report.AffectedTunables = dedupeStrings(report.AffectedTunables)
	return report
}

// collectTraceSignals counts security violations from case trace evidence.
func collectTraceSignals(caseResults []domain.EvalCaseResult, report *DiagnosisReport) {
	for _, cr := range caseResults {
		if cr.TraceEvidence != nil && cr.TraceEvidence.SecurityViolation {
			report.CategoryBreakdown[domain.CatPrompt]++
		}
	}
}

// mergeLLMHypotheses adds LLM-sourced hypotheses from the enhanced report.
func mergeLLMHypotheses(enhanced DiagnosisReport, report *DiagnosisReport) {
	for _, h := range enhanced.RootCauseHypotheses {
		if h.Source == "llm" {
			report.RootCauseHypotheses = append(report.RootCauseHypotheses, h)
		}
	}
	report.AffectedTunables = dedupeStrings(
		append(report.AffectedTunables, enhanced.AffectedTunables...))
}

// classifyFailure maps a single failure to a tunable category using keyword
// and pattern heuristics. Returns category, confidence (0-1), and description.
func classifyFailure(f domain.FailureSummary) (domain.TunableCategory, float64, string) {
	text := strings.ToLower(f.CaseName + " " + f.Description + " " + f.Actual + " " + f.Expected)

	// Security violations take highest priority.
	if containsAny(text, securityKeywords) {
		return domain.CatPrompt, 0.9, "security constraint violation detected in output"
	}

	// Timeout / retry exhaustion → model config.
	if containsAny(text, timeoutKeywords) {
		return domain.CatModelConfig, 0.85, "timeout or retry exhaustion suggests model/config tuning"
	}

	// Tool execution errors.
	if containsAny(text, toolExecKeywords) {
		return domain.CatToolExec, 0.8, "tool call or structured output failure"
	}

	// RAG / retrieval.
	if containsAny(text, ragKeywords) {
		return domain.CatRAG, 0.8, "retrieval or knowledge lookup failure"
	}

	// Planning / multi-step.
	if containsAny(text, planningKeywords) {
		return domain.CatPlanning, 0.75, "multi-step reasoning or planning failure"
	}

	// Compaction / summary.
	if containsAny(text, compactionKeywords) {
		return domain.CatCompaction, 0.75, "context compaction or summary quality issue"
	}

	// Context / memory.
	if containsAny(text, contextMemoryKeywords) {
		return domain.CatContextMemory, 0.7, "context window or memory management issue"
	}

	// Prompt quality (default for assertion failures).
	if containsAny(text, promptKeywords) {
		return domain.CatPrompt, 0.6, "output quality suggests prompt improvement"
	}

	// Fallback: low-confidence prompt classification.
	return domain.CatPrompt, 0.3, "unclassified failure, defaulting to prompt review"
}

var securityKeywords = []string{
	"security", "injection", "xss", "prompt injection", "jailbreak",
	"unsafe", "dangerous", "exploit", "bypass", "披露", "泄露",
}

var timeoutKeywords = []string{
	"timeout", "timed out", "deadline exceeded", "context deadline",
	"retry exhaust", "too many retries", "超时", "重试耗尽",
}

var toolExecKeywords = []string{
	"tool call", "tool_call", "function call", "function_call",
	"invalid json", "parse error", "schema", "structured output",
	"argument", "parameter mismatch", "tool not found",
}

var ragKeywords = []string{
	"retrieval", "retrieve", "knowledge", "rag", "vector",
	"embedding", "search result", "relevant document", "milvus",
	"查询", "检索", "知识库",
}

var planningKeywords = []string{
	"plan", "planning", "multi-step", "reasoning", "chain",
	"thought", "cot", "react", "loop", "iteration",
}

var compactionKeywords = []string{
	"compaction", "compact", "summary", "summarize", "truncat",
	"context limit", "token limit", "message limit",
}

var contextMemoryKeywords = []string{
	"memory", "context", "forget", "forgot", "remember",
	"session", "history", "conversation",
}

var promptKeywords = []string{
	"prompt", "instruction", "system_prompt", "output quality",
	"hallucination", "incorrect", "wrong answer", "不准确",
	"格式", "回复", "回答",
}

func containsAny(text string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

// categoryToTunables maps a tunable category to the concrete tunable keys
// that should be optimized.
func categoryToTunables(cat domain.TunableCategory) []string {
	switch cat {
	case domain.CatModelConfig:
		return []string{"temperature", "max_tokens"}
	case domain.CatContextMemory:
		return []string{domain.TunableMemoryExtractionPrompt,
			domain.TunableMemorySummaryPrompt,
			domain.TunableMemoryEnrichmentPrompt}
	case domain.CatToolExec:
		return []string{"max_retries", "timeout_ms"}
	case domain.CatRAG:
		return []string{"top_k", "score_threshold", "query_rewrite"}
	case domain.CatPlanning:
		return []string{"max_iterations", "max_context_tokens"}
	case domain.CatCompaction:
		// 压缩提示词已迁平台参数，不在 agent 调优搜索空间内。
		return nil
	case domain.CatPrompt:
		return []string{domain.TunableSystemPrompt}
	default:
		return nil
	}
}

// mergeHypotheses consolidates hypotheses of the same category, keeping the
// highest-confidence entry and merging evidence lists.
func mergeHypotheses(hs []domain.RootCauseHypothesis) []domain.RootCauseHypothesis {
	if len(hs) <= 1 {
		return hs
	}
	byCategory := map[domain.TunableCategory]domain.RootCauseHypothesis{}
	order := make([]domain.TunableCategory, 0, len(hs))
	for _, h := range hs {
		existing, ok := byCategory[h.Category]
		if !ok {
			byCategory[h.Category] = h
			order = append(order, h.Category)
			continue
		}
		if h.Confidence > existing.Confidence {
			existing.Confidence = h.Confidence
			existing.Description = h.Description
		}
		existing.Evidence = dedupeStrings(append(existing.Evidence, h.Evidence...))
		byCategory[h.Category] = existing
	}
	merged := make([]domain.RootCauseHypothesis, 0, len(order))
	for _, cat := range order {
		merged = append(merged, byCategory[cat])
	}
	return merged
}

func dedupeStrings(ss []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// DiagnosisReport is an application-level alias for the domain type, used
// within the application layer to avoid import cycles.
type DiagnosisReport = domain.DiagnosisReport
