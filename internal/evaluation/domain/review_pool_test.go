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
