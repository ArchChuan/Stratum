package domain

import (
	"encoding/json"
	"testing"
)

// TestAssertionResultConfidenceRoundTrip 断言 Confidence 进入序列化契约；
// 缺失 confidence 反序列化为 0（解析层负责回退 1.0，domain 不静默改值）。
func TestAssertionResultConfidenceRoundTrip(t *testing.T) {
	raw := []byte(`{"passed":true,"message":"ok","confidence":0.8}`)
	var got AssertionResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Confidence != 0.8 {
		t.Fatalf("confidence = %v, want 0.8", got.Confidence)
	}
	// 缺失 confidence 原样保留 0，由 parseJudgeResponse 层回退。注意必须用
	// 全新零值结构：json.Unmarshal 对已存在结构只合并不清理，复用 got 会残留
	// 上一次的 0.8，测的是合并语义而非反序列化契约。
	var missing AssertionResult
	if err := json.Unmarshal([]byte(`{"passed":false,"message":"x"}`), &missing); err != nil {
		t.Fatalf("unmarshal missing confidence: %v", err)
	}
	if missing.Confidence != 0 {
		t.Fatalf("confidence = %v, want 0 (unset)", missing.Confidence)
	}
}

// TestEvalCaseResultIDJSON 断言 ID 字段存在且缺省为零值（runCase 生成前）。
func TestEvalCaseResultIDJSON(t *testing.T) {
	raw := []byte(`{"case_id":"c1","passed":true}`)
	var got EvalCaseResult
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "" {
		t.Fatalf("ID = %q, want empty", got.ID)
	}
	got.ID = "r-1"
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(out) {
		t.Fatalf("invalid json: %s", out)
	}
}

func TestTriggersForObservation(t *testing.T) {
	cfg := ReviewConfig{LowConfidenceThreshold: 0.6, JudgePassThreshold: 0.5}
	obs := func() *EvalObservation {
		return &EvalObservation{Resource: ObservationResourceRef{Kind: ResourceKindSkill, ResourceID: "s1"}}
	}

	t.Run("no judge signals yields no triggers", func(t *testing.T) {
		if got := TriggersForObservation(obs(), cfg); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})

	t.Run("low confidence triggers", func(t *testing.T) {
		o := obs()
		o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 1.0, Confidence: 0.4}}
		if got := TriggersForObservation(o, cfg); len(got) != 1 || got[0] != TriggerLowConfidence {
			t.Fatalf("got %v, want [low_confidence]", got)
		}
	})

	t.Run("dimension split triggers", func(t *testing.T) {
		o := obs()
		o.Signals.Judge = []JudgeSignal{
			{Dimension: "faithfulness", Score: 1.0, Confidence: 0.9},
			{Dimension: "relevance", Score: 0.2, Confidence: 0.9},
		}
		if got := TriggersForObservation(o, cfg); !containsReason(got, TriggerDimensionSplit) {
			t.Fatalf("got %v, want dimension_split present", got)
		}
	})

	t.Run("rule conflict triggers only when all judge pass and verdict block", func(t *testing.T) {
		o := obs()
		o.Verdict = VerdictBlock
		o.Signals.Rule = []RuleSignal{{Rule: "r1"}}
		o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 1.0, Confidence: 0.9}}
		got := TriggersForObservation(o, cfg)
		if !containsReason(got, TriggerJudgeRuleConflict) {
			t.Fatalf("got %v, want judge_rule_conflict present", got)
		}
	})

	t.Run("rule conflict suppressed when judge below threshold", func(t *testing.T) {
		o := obs()
		o.Verdict = VerdictBlock
		o.Signals.Rule = []RuleSignal{{Rule: "r1"}}
		o.Signals.Judge = []JudgeSignal{{Dimension: "faithfulness", Score: 0.2, Confidence: 0.9}}
		if got := TriggersForObservation(o, cfg); containsReason(got, TriggerJudgeRuleConflict) {
			t.Fatalf("got %v, want no judge_rule_conflict", got)
		}
	})

	t.Run("nil observation yields no triggers", func(t *testing.T) {
		if got := TriggersForObservation(nil, cfg); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})
}

func TestTriggersForCaseResult(t *testing.T) {
	cfg := ReviewConfig{LowConfidenceThreshold: 0.6}
	passing := AssertionResult{Passed: true, Confidence: 0.9}

	t.Run("passing assertion yields no triggers", func(t *testing.T) {
		if got := TriggersForCaseResult(false, passing, cfg); len(got) != 0 {
			t.Fatalf("got %v, want none", got)
		}
	})

	t.Run("needs review triggers", func(t *testing.T) {
		got := TriggersForCaseResult(true, passing, cfg)
		if !containsReason(got, TriggerNeedsReview) {
			t.Fatalf("got %v, want needs_review present", got)
		}
	})

	t.Run("low confidence triggers", func(t *testing.T) {
		got := TriggersForCaseResult(false, AssertionResult{Passed: true, Confidence: 0.3}, cfg)
		if !containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want low_confidence present", got)
		}
	})

	t.Run("both triggers coexist", func(t *testing.T) {
		got := TriggersForCaseResult(true, AssertionResult{Passed: false, Confidence: 0.2}, cfg)
		if !containsReason(got, TriggerNeedsReview) || !containsReason(got, TriggerLowConfidence) {
			t.Fatalf("got %v, want needs_review + low_confidence", got)
		}
	})
}

func containsReason(got []ReviewTriggerReason, want ReviewTriggerReason) bool {
	for _, g := range got {
		if g == want {
			return true
		}
	}
	return false
}
