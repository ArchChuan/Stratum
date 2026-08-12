package port

import "context"

// MechanismBaseline 是机制面基线在 memory 消费方的视图（薄 ACL）。
// 字段与 mechanism/domain.Baseline 对应；跨 context 禁止 import 兄弟 domain，
// wiring 负责适配转换。空字符串 = 该键未建档，消费方回退自身硬编码兜底
// （现状行为）。
type MechanismBaseline struct {
	MemoryExtraction string // llm_extractor 抽取模板（%s/%s/%d）
	MemorySummary    string // enricher 中文总结模板（%s）
	MemoryEnrichment string // enricher 富化模板（%s/%s）
	MemorySummarize  string // history_summarizer 周期总结（无占位）
	MemorySupersede  string // llm_superseder 判断模板（%s/%s）
	EnrichModel      string // 管线富化模型（基线优先，env 兜底）
	SummaryModel     string // 管线总结模型（基线优先，env 兜底）
}

// MechanismBaselineResolver 按租户解析机制基线。err 非 nil 时调用方应保持
// 自身兜底配置并告警（基线是配置源而非授权，回退现状不构成安全降级）。
type MechanismBaselineResolver func(ctx context.Context, tenantID string) (MechanismBaseline, error)
