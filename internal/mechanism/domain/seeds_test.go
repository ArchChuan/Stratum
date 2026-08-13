package domain

import "testing"

// TestDefaultBaselineCoversAllConsumptionPoints 守卫七键完整性：机制基线
// 的每个 prompt 键都对应一个消费路径，漏键会让该路径静默回退硬编码，
// 档案编辑失去效力。
func TestDefaultBaselineCoversAllConsumptionPoints(t *testing.T) {
	b := DefaultBaseline()
	cases := map[string]string{
		"llm_extractor extraction":     b.Prompts.MemoryExtraction,
		"enricher summary (中文)":        b.Prompts.MemorySummary,
		"enricher enrichment":          b.Prompts.MemoryEnrichment,
		"history_summarizer summarize": b.Prompts.MemorySummarize,
		"llm_superseder judge":         b.Prompts.MemorySupersede,
		"history_compactor compaction": b.Prompts.Compaction,
		"factcheck judge":              b.Prompts.AgentFactCheck,
	}
	for name, got := range cases {
		if got == "" {
			t.Errorf("%s: seed prompt missing in DefaultBaseline", name)
		}
	}
	models := map[string]string{
		"enrich model":     b.Models.EnrichModel,
		"summary model":    b.Models.SummaryModel,
		"extraction model": b.Models.ExtractionModel,
		"judge model":      b.Models.JudgeModel,
	}
	for name, got := range models {
		if got == "" {
			t.Errorf("%s: seed model missing in DefaultBaseline", name)
		}
	}
}
