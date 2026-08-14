package pipeline

import (
	"testing"

	"go.uber.org/zap"
)

// TestEnricherConstructorDefaults 验证构造时模型默认（cfg 空时 qwen-turbo）。
// mechanism 基线移除后为唯一权威，行为等价 pre-refactor（金丝雀回归）。
func TestEnricherConstructorDefaults(t *testing.T) {
	w := NewEnricherWorker(nil, nil, nil, zap.NewNop(), Config{})
	if w.model != "qwen-turbo" {
		t.Fatalf("constructor default model = %q, want qwen-turbo", w.model)
	}
	if w.summaryModel != "qwen-turbo" {
		t.Fatalf("constructor default summary model = %q, want qwen-turbo", w.summaryModel)
	}
}
