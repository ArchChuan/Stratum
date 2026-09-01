package domain

import "time"

// HumanVerdict 人工评审结论 4 分类（spec §6.6 回写动作）。
// 常量以 HumanVerdict 前缀命名：同包已有 ObservationVerdict.VerdictPass，
// 裸名 VerdictPass 已被占用，故按 Go 惯例用类型前缀消除歧义。
type HumanVerdict string

const (
	HumanVerdictPass             HumanVerdict = "pass"
	HumanVerdictFail             HumanVerdict = "fail"
	HumanVerdictJudgeMisjudgment HumanVerdict = "judge_misjudgment"
	HumanVerdictCaseRevision     HumanVerdict = "case_revision"
)

// Valid 校验人工结论枚举。
func (v HumanVerdict) Valid() bool {
	switch v {
	case HumanVerdictPass, HumanVerdictFail, HumanVerdictJudgeMisjudgment, HumanVerdictCaseRevision:
		return true
	default:
		return false
	}
}

// ReviewTriggerReason 入池原因（硬编码规则，AI 不做控制决策）。
type ReviewTriggerReason string

const (
	TriggerLowConfidence         ReviewTriggerReason = "low_confidence"
	TriggerDimensionSplit        ReviewTriggerReason = "dimension_split"
	TriggerJudgeRuleConflict     ReviewTriggerReason = "judge_rule_conflict"
	TriggerNeedsReview           ReviewTriggerReason = "needs_review"
	TriggerProcessOutputConflict ReviewTriggerReason = "process_output_conflict"
)

// Valid 校验入池原因枚举。
func (r ReviewTriggerReason) Valid() bool {
	switch r {
	case TriggerLowConfidence, TriggerDimensionSplit, TriggerJudgeRuleConflict, TriggerNeedsReview,
		TriggerProcessOutputConflict:
		return true
	default:
		return false
	}
}

// ReviewSourceType 评审条目来源。
type ReviewSourceType string

const (
	ReviewSourceObservation ReviewSourceType = "observation"
	ReviewSourceCaseResult  ReviewSourceType = "case_result"
)

// ReviewItemStatus 评审条目状态。
type ReviewItemStatus string

const (
	ReviewStatusPending  ReviewItemStatus = "pending"
	ReviewStatusReviewed ReviewItemStatus = "reviewed"
)

// ReviewConfig 评审池触发配置。默认值在 pkg/constants（Task 1），wiring 组装。
type ReviewConfig struct {
	// LowConfidenceThreshold 是 low_confidence 触发阈值（默认 constants.ReviewLowConfidenceThreshold）。
	LowConfidenceThreshold float64
	// JudgePassThreshold 是维度通过/跌阈分界（沿用 constants.JudgeBelowThreshold）。
	JudgePassThreshold float64
}

// ReviewItem 评审池条目（对应 eval_review_items）。
type ReviewItem struct {
	ID            string              `json:"id"`
	SourceType    ReviewSourceType    `json:"source_type"`
	SourceID      string              `json:"source_id"`
	RunID         string              `json:"run_id,omitempty"`
	TraceID       string              `json:"trace_id,omitempty"`
	ResourceKind  ResourceKind        `json:"resource_kind"`
	ResourceID    string              `json:"resource_id"`
	TriggerReason ReviewTriggerReason `json:"trigger_reason"`
	Snapshot      any                 `json:"snapshot"`
	Status        ReviewItemStatus    `json:"status"`
	HumanVerdict  HumanVerdict        `json:"human_verdict,omitempty"`
	Reviewer      string              `json:"reviewer,omitempty"`
	ReviewReason  string              `json:"review_reason,omitempty"`
	CreatedAt     time.Time           `json:"created_at"`
	ReviewedAt    *time.Time          `json:"reviewed_at,omitempty"`
}

// CalibrationSample judge 误判校准样本（对应 eval_calibration_samples）。
type CalibrationSample struct {
	ID           string           `json:"id"`
	ReviewItemID string           `json:"review_item_id"`
	SourceType   ReviewSourceType `json:"source_type"`
	SourceID     string           `json:"source_id"`
	JudgeModel   string           `json:"judge_model,omitempty"`
	Signals      any              `json:"signals"`
	HumanVerdict HumanVerdict     `json:"human_verdict"`
	Reviewer     string           `json:"reviewer"`
	Reason       string           `json:"reason,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
}

// AttributionEntry 产品缺陷归因条目（对应 eval_attribution_entries，轻量记录）。
type AttributionEntry struct {
	ID           string           `json:"id"`
	ReviewItemID string           `json:"review_item_id"`
	SourceType   ReviewSourceType `json:"source_type"`
	SourceID     string           `json:"source_id"`
	ResourceKind ResourceKind     `json:"resource_kind"`
	ResourceID   string           `json:"resource_id"`
	Dimension    string           `json:"dimension,omitempty"`
	Snapshot     any              `json:"snapshot"`
	Status       string           `json:"status"`
	Reviewer     string           `json:"reviewer"`
	Reason       string           `json:"reason,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
}

// TriggersForObservation 计算观测应入池的触发原因（空 = 不进池）。纯函数，硬编码规则。
// 规则（spec §6.6）：
//  1. low_confidence：任一 judge 维度 Confidence < cfg.LowConfidenceThreshold；
//  2. dimension_split：存在 Score >= JudgePassThreshold 且存在 Score < JudgePassThreshold；
//  3. judge_rule_conflict：规则命中（Signals.Rule 非空）+ Verdict == block + 全部维度 pass。
func TriggersForObservation(obs *EvalObservation, cfg ReviewConfig) []ReviewTriggerReason {
	if obs == nil || len(obs.Signals.Judge) == 0 {
		return nil
	}
	var triggers []ReviewTriggerReason
	if hasLowConfidence(obs.Signals.Judge, cfg.LowConfidenceThreshold) {
		triggers = append(triggers, TriggerLowConfidence)
	}
	below, above := splitExists(obs.Signals.Judge, cfg.JudgePassThreshold)
	if below && above {
		triggers = append(triggers, TriggerDimensionSplit)
	}
	if len(obs.Signals.Rule) > 0 && obs.Verdict == VerdictBlock && !below {
		triggers = append(triggers, TriggerJudgeRuleConflict)
	}
	return triggers
}

// hasLowConfidence 返回任一 judge 维度 Confidence < threshold。
func hasLowConfidence(judge []JudgeSignal, threshold float64) bool {
	for _, j := range judge {
		if j.Confidence < threshold {
			return true
		}
	}
	return false
}

// splitExists 返回是否存在 Score 低于 threshold（below）与不低于 threshold（above）。
func splitExists(judge []JudgeSignal, threshold float64) (below, above bool) {
	for _, j := range judge {
		if j.Score < threshold {
			below = true
		} else {
			above = true
		}
	}
	return below, above
}

// TriggersForProcessConflict 计算输出断言与过程断言不一致的入池原因（§6.5 §6.6）：
// 仅输出通过但过程失败（outputPass=true, processPass=false）时触发
// process_output_conflict；其余组合（一致或输出已失败）不构成冲突，不进池。
// 纯函数，硬编码规则。规则断言 case 也复用本函数（规则 case 无 judge 信号，
// 不能走完整 TriggersForCaseResult 以免 low_confidence 误触发）。
func TriggersForProcessConflict(outputPass, processPass bool) []ReviewTriggerReason {
	if outputPass && !processPass {
		return []ReviewTriggerReason{TriggerProcessOutputConflict}
	}
	return nil
}

// TriggersForCaseResult 计算评测集 judge 判定的入池原因（空 = 不进池）。
// 规则（spec §6.6）：
//  1. needs_review：EvalCase.NeedsReview == true（assertion_mode 分支由调用方强制，本函数不检查）；
//  2. low_confidence：assertion.Confidence < cfg.LowConfidenceThreshold；
//  3. process_output_conflict：输出断言通过但过程断言失败（§6.5）。
func TriggersForCaseResult(
	needsReview bool, outputPass, processPass bool, assertion AssertionResult, cfg ReviewConfig,
) []ReviewTriggerReason {
	var triggers []ReviewTriggerReason
	if needsReview {
		triggers = append(triggers, TriggerNeedsReview)
	}
	if assertion.Confidence < cfg.LowConfidenceThreshold {
		triggers = append(triggers, TriggerLowConfidence)
	}
	return append(triggers, TriggersForProcessConflict(outputPass, processPass)...)
}
