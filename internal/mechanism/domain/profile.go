// Package mechanism 承载机制面（平台固定、租户零感知）的可调参数存储化：
// model_profiles 档案（prompt 四键/管线模型引用/召回参数）按模型族差异化，
// 版本化 + 回退 + 评测迭代。语义见 docs/agent/mechanism-baseline.md。
package domain

import (
	"errors"
	"fmt"
	"time"
)

// ProfileStatus 是档案生命周期状态：draft（评测中）→ active（生效）。
const (
	ProfileStatusActive = "active"
	ProfileStatusDraft  = "draft"
)

// 家族前缀规模上限：与 proto binding（max=32,dive,max=100）双保险，
// 限制全局基线的 JSONB 体积与消费路径前缀扫描成本。
const (
	maxFamilyPrefixes  = 32
	maxFamilyPrefixLen = 100
)

// ModelMatcher 把模型名映射到档案：模型名以任一 family_prefix 开头即命中。
type ModelMatcher struct {
	FamilyPrefixes []string `json:"family_prefixes"`
}

// BaselinePrompts 是机制面 prompt 键集：对应原 memory 管线/agent 压缩链路的
// 全部硬编码模板（6 个消费点），DB 缺省时回退 embedded 种子（现状值）。
type BaselinePrompts struct {
	MemoryExtraction string `json:"memory_extraction,omitempty"` // llm_extractor 抽取模板（%s/%s/%d）
	MemorySummary    string `json:"memory_summary,omitempty"`    // enricher 中文总结模板（%s）
	MemoryEnrichment string `json:"memory_enrichment,omitempty"` // enricher 富化模板（%s/%s）
	MemorySummarize  string `json:"memory_summarize,omitempty"`  // history_summarizer 周期总结（无占位）
	MemorySupersede  string `json:"memory_supersede,omitempty"`  // llm_superseder 判断模板（%s/%s）
	Compaction       string `json:"compaction,omitempty"`        // history_compactor 压缩指令（无占位）
	AgentFactCheck   string `json:"agent_factcheck,omitempty"`   // factcheck judge 判定模板（%s/%s）
}

// BaselineModels 是机制面管线模型引用（原 env EnrichModel/SummaryModel）。
type BaselineModels struct {
	EnrichModel     string `json:"enrich_model,omitempty"`
	SummaryModel    string `json:"summary_model,omitempty"`
	ExtractionModel string `json:"extraction_model,omitempty"` // llm_extractor 抽取模型
	JudgeModel      string `json:"judge_model,omitempty"`      // factcheck LLM-as-Judge 判定模型
}

// BaselineRecall 是召回参数（注册表 legacy 假参数，接线语义确认前保持 nil）。
type BaselineRecall struct {
	RecallTopK        *int `json:"recall_top_k,omitempty"`
	FactInjectionTopN *int `json:"fact_injection_top_n,omitempty"`
	LongTermTopK      *int `json:"long_term_top_k,omitempty"`
}

// Baseline 是一个档案档位的全部机制基线。
type Baseline struct {
	Prompts BaselinePrompts `json:"prompts"`
	Models  BaselineModels  `json:"models"`
	Recall  BaselineRecall  `json:"recall"`
}

// Profile 是一个模型族档案档位（global 共享引用，public schema）。
type Profile struct {
	ID          string
	FamilyKey   string
	DisplayName string
	Matcher     ModelMatcher
	Baseline    Baseline
	Fingerprint string
	Version     int
	Status      string
	CreatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ErrInvalidProfile 标记校验失败的档案输入。
var ErrInvalidProfile = errors.New("mechanism: invalid profile")

// Validate 校验档案不变量：族键非空、matcher 至少一个前缀、状态合法。
// 前缀数量/长度上限与 proto binding（max=32 / dive,max=100）保持双保险。
func (p *Profile) Validate() error {
	if p.FamilyKey == "" {
		return fmt.Errorf("%w: family_key required", ErrInvalidProfile)
	}
	if len(p.Matcher.FamilyPrefixes) == 0 {
		return fmt.Errorf("%w: model_matcher.family_prefixes must not be empty", ErrInvalidProfile)
	}
	if len(p.Matcher.FamilyPrefixes) > maxFamilyPrefixes {
		return fmt.Errorf("%w: too many family_prefixes (%d > %d)", ErrInvalidProfile, len(p.Matcher.FamilyPrefixes), maxFamilyPrefixes)
	}
	for _, prefix := range p.Matcher.FamilyPrefixes {
		if prefix == "" {
			return fmt.Errorf("%w: model_matcher.family_prefixes contains empty prefix", ErrInvalidProfile)
		}
		if len(prefix) > maxFamilyPrefixLen {
			return fmt.Errorf("%w: family_prefix exceeds %d chars", ErrInvalidProfile, maxFamilyPrefixLen)
		}
	}
	if p.Status != "" && p.Status != ProfileStatusActive && p.Status != ProfileStatusDraft {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidProfile, p.Status)
	}
	return nil
}

// Matches 报告模型名是否命中本档案（前缀匹配，familyPrefixes 语义）。
func (p *Profile) Matches(model string) bool {
	for _, prefix := range p.Matcher.FamilyPrefixes {
		if len(model) >= len(prefix) && model[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
