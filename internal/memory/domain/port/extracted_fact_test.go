package port_test

import (
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// TestExtractedFact_JSON_OmittedConfidence JSON 省略 confidence 字段 → Confidence 为 nil
func TestExtractedFact_JSON_OmittedConfidence(t *testing.T) {
	raw := `{"content":"test","importance":0.8,"fact_type":"preference","entities":[]}`
	var ef port.ExtractedFact
	if err := json.Unmarshal([]byte(raw), &ef); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if ef.Confidence != nil {
		t.Errorf("omitted confidence should be nil, got %v", ef.Confidence)
	}
}

// TestExtractedFact_JSON_ExplicitZero JSON 显式设置 confidence:0.0 → Confidence 指针非 nil，值为 0.0
func TestExtractedFact_JSON_ExplicitZero(t *testing.T) {
	raw := `{"content":"test","importance":0.8,"fact_type":"preference","entities":[],"confidence":0.0}`
	var ef port.ExtractedFact
	if err := json.Unmarshal([]byte(raw), &ef); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if ef.Confidence == nil {
		t.Fatal("explicit 0.0 confidence should not be nil")
	}
	if *ef.Confidence != 0.0 {
		t.Errorf("explicit confidence = %f, want 0.0", *ef.Confidence)
	}
}

// TestExtractedFact_JSON_ExplicitNonZero JSON 设置 confidence:0.7 → Confidence = &0.7
func TestExtractedFact_JSON_ExplicitNonZero(t *testing.T) {
	raw := `{"content":"test","importance":0.8,"fact_type":"skill","entities":["Go"],"confidence":0.7}`
	var ef port.ExtractedFact
	if err := json.Unmarshal([]byte(raw), &ef); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if ef.Confidence == nil {
		t.Fatal("confidence should not be nil")
	}
	if *ef.Confidence != 0.7 {
		t.Errorf("confidence = %f, want 0.7", *ef.Confidence)
	}
}
