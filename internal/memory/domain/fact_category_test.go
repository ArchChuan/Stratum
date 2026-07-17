package domain_test

import (
	"math"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// TestFactTypeToCategory_KnownTypes 验证已知 fact_type 到 category 的映射
func TestFactTypeToCategory_KnownTypes(t *testing.T) {
	cases := []struct {
		factType string
		want     string
	}{
		{"preference", "preference"},
		{"skill", "skill"},
		{"event", "event"},
		{"state", "state"},
		{"relationship", "relationship"},
		{"other", "other"},
	}
	for _, c := range cases {
		got := domain.FactTypeToCategory(c.factType)
		if got != c.want {
			t.Errorf("FactTypeToCategory(%q) = %q, want %q", c.factType, got, c.want)
		}
	}
}

// TestFactTypeToCategory_UnknownFallback 未知 fact_type 应安全回退为 "other"
func TestFactTypeToCategory_UnknownFallback(t *testing.T) {
	got := domain.FactTypeToCategory("completely_unknown")
	if got != "other" {
		t.Errorf("FactTypeToCategory(unknown) = %q, want %q", got, "other")
	}
}

// TestNewFactWithMeta_ValidCategory 合法 category 构造成功
func TestNewFactWithMeta_ValidCategory(t *testing.T) {
	f, err := domain.NewFactWithMeta(
		"t1", "u1", "", "", "user", "User prefers Vim", 0.8, 0.7,
		"preference", domain.FactSourceLLMExtraction, []string{"vim"},
	)
	if err != nil {
		t.Fatalf("NewFactWithMeta valid category failed: %v", err)
	}
	if f.Category != "preference" {
		t.Errorf("Category = %q, want %q", f.Category, "preference")
	}
	if f.Confidence != 0.7 {
		t.Errorf("Confidence = %f, want 0.7", f.Confidence)
	}
	if f.Source != domain.FactSourceLLMExtraction {
		t.Errorf("Source = %q, want %q", f.Source, domain.FactSourceLLMExtraction)
	}
}

// TestNewFactWithMeta_InvalidCategory 非法 category 返回错误
func TestNewFactWithMeta_InvalidCategory(t *testing.T) {
	_, err := domain.NewFactWithMeta(
		"t1", "u1", "", "", "user", "content", 0.5, 0.5,
		"invalid_cat", domain.FactSourceLLMExtraction, nil,
	)
	if err == nil {
		t.Error("expected error for invalid category, got nil")
	}
}

// TestNewFactWithMeta_ConfidenceBounds confidence 超出 [0,1] 应返回错误
func TestNewFactWithMeta_ConfidenceBounds(t *testing.T) {
	_, err := domain.NewFactWithMeta(
		"t1", "u1", "", "", "user", "content", 0.5, 1.1,
		"other", domain.FactSourceLLMExtraction, nil,
	)
	if err == nil {
		t.Error("expected error for confidence=1.1")
	}

	_, err = domain.NewFactWithMeta(
		"t1", "u1", "", "", "user", "content", 0.5, -0.1,
		"other", domain.FactSourceLLMExtraction, nil,
	)
	if err == nil {
		t.Error("expected error for confidence=-0.1")
	}

	_, err = domain.NewFactWithMeta(
		"t1", "u1", "", "", "user", "content", 0.5, math.NaN(),
		"other", domain.FactSourceLLMExtraction, nil,
	)
	if err == nil {
		t.Error("expected error for confidence=NaN")
	}
}

func TestNewFactWithMeta_InvalidSource(t *testing.T) {
	_, err := domain.NewFactWithMeta(
		"t1", "u1", "", "", "user", "content", 0.5, 0.5,
		"other", "untrusted_source", nil,
	)
	if err == nil {
		t.Fatal("expected error for source outside the allowlist")
	}
}

// TestNewFact_BackwardCompat 旧 NewFact 调用不改变签名，Category 默认 "other"，Source 默认 "llm_extraction"
func TestNewFact_BackwardCompat(t *testing.T) {
	f, err := domain.NewFact("t1", "u1", "", "", "user", "content", 0.5, nil)
	if err != nil {
		t.Fatalf("NewFact backward compat failed: %v", err)
	}
	if f.Category != "other" {
		t.Errorf("default Category = %q, want %q", f.Category, "other")
	}
	if f.Source != domain.FactSourceLLMExtraction {
		t.Errorf("default Source = %q, want %q", f.Source, domain.FactSourceLLMExtraction)
	}
	if f.Confidence != f.Importance {
		t.Errorf("default Confidence = %f, want Importance = %f", f.Confidence, f.Importance)
	}
}

// TestFactSourceConstants 来源常量可访问
func TestFactSourceConstants(t *testing.T) {
	sources := []string{
		domain.FactSourceLLMExtraction,
		domain.FactSourceExplicitUser,
		domain.FactSourceManualAPI,
	}
	for _, s := range sources {
		if s == "" {
			t.Errorf("source constant is empty")
		}
	}
}
