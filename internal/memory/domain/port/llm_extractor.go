package port

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// factTypeAllowSet 是 LLM 可返回的 fact_type 合法枚举（镜像 domain 层
// factTypeToCategory 的 6 个 key）。严格校验而非宽松 fallback：0 条通过时
// 走带错重试 + typed error（保留 MarkFailed/DLQ），禁止把坏枚举静默当 other。
var factTypeAllowSet = map[string]bool{
	"preference":   true,
	"skill":        true,
	"event":        true,
	"state":        true,
	"relationship": true,
	"other":        true,
}

// maxSupersedeReasonRunes 限制 supersede judgment reason 的最大 rune 数，
// 防止模型输出超长理由（memory_entries 行宽约束 + 注入预算保护）。
const maxSupersedeReasonRunes = 500

// inUnitInterval 判断 v 是否在 [0,1] 闭区间。port 包内部共享 helper。
func inUnitInterval(v float64) bool {
	return v >= 0 && v <= 1
}

// Validate 校验单条抽取事实：content 非空、importance/confidence ∈ [0,1]、
// fact_type 属于 6 枚举。返回 *ValidationError 或 nil（通过）。
// 声明返回 error 而非 *ValidationError：方法内 return nil 会得到真 nil 接口，
// 避免调用方把 typed-nil 误判为失败（闭包转 error 接口后 != nil 恒真）。
func (f *ExtractedFact) Validate() error {
	if f == nil {
		return &ValidationError{Location: "fact", FieldName: "fact", Reason: "fact is nil"}
	}
	if strings.TrimSpace(f.Content) == "" {
		return &ValidationError{Location: "fact", FieldName: "content", Reason: "content must not be empty"}
	}
	if !inUnitInterval(f.Importance) {
		return &ValidationError{Location: "fact", FieldName: "importance",
			Value: strconv.FormatFloat(f.Importance, 'g', -1, 64), Reason: "importance must be in [0,1]"}
	}
	if !factTypeAllowSet[f.FactType] {
		return &ValidationError{Location: "fact", FieldName: "fact_type",
			Value: f.FactType, Reason: "fact_type must be one of preference|skill|event|state|relationship|other"}
	}
	if f.Confidence != nil && !inUnitInterval(*f.Confidence) {
		return &ValidationError{Location: "fact", FieldName: "confidence",
			Value: strconv.FormatFloat(*f.Confidence, 'g', -1, 64), Reason: "confidence must be in [0,1]"}
	}
	return nil
}

// Validate 校验 supersede 判定：reason 长度 ≤ 上限（结构完整性）。
func (j *SupersedeJudgment) Validate() error {
	if j == nil {
		return &ValidationError{Location: "judgment", FieldName: "judgment", Reason: "judgment is nil"}
	}
	if utf8.RuneCountInString(j.Reason) > maxSupersedeReasonRunes {
		return &ValidationError{Location: "judgment", FieldName: "reason",
			Reason: fmt.Sprintf("reason exceeds %d runes", maxSupersedeReasonRunes)}
	}
	return nil
}

// ExtractedFact represents a fact extracted from conversation.
type ExtractedFact struct {
	Content    string   `json:"content"`
	Importance float64  `json:"importance"`
	Entities   []string `json:"entities"`
	// FactType classifies the fact: preference|skill|event|state|relationship|other
	FactType string `json:"fact_type"`
	// Confidence is the LLM-reported confidence in [0,1].
	// Pointer to distinguish omitted (nil → default to Importance) from explicit 0.0
	// (filtered by low-confidence gate in Phase 0).
	Confidence *float64 `json:"confidence,omitempty"`
}

// LLMExtractor extracts structured facts from conversation messages.
type LLMExtractor interface {
	ExtractFacts(ctx context.Context, userID, agentID, message string) ([]*ExtractedFact, error)
}

// SupersedeJudgment contains LLM's decision on whether new fact supersedes old.
type SupersedeJudgment struct {
	Supersedes bool
	Reason     string
}

// LLMSuperseder judges whether new fact supersedes old fact.
type LLMSuperseder interface {
	JudgeSupersede(ctx context.Context, oldFact, newFact string) (*SupersedeJudgment, error)
}
