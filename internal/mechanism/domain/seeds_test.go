package domain

import "testing"

// TestDefaultBaselineCoversAllConsumptionPoints 守卫六键完整性：机制基线
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
	}
	for name, got := range cases {
		if got == "" {
			t.Errorf("%s: seed prompt missing in DefaultBaseline", name)
		}
	}
}
