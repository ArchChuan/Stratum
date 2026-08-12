package pipeline

import (
	"context"
	"errors"
	"testing"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"go.uber.org/zap"
)

// TestEnricherResolveEffectiveOverridesBaselineKeys 验证机制基线逐消息解析：
// 非空键覆盖构造时 env/默认值；空键与解析失败维持构造值（现状行为）。
func TestEnricherResolveEffectiveOverridesBaselineKeys(t *testing.T) {
	w := NewEnricherWorker(nil, nil, nil, zap.NewNop(), Config{})
	if eff := w.resolveEffective(context.Background(), "tenant-1"); eff.model != "qwen-turbo" {
		t.Fatalf("constructor default model = %q, want qwen-turbo", eff.model)
	}

	w.WithMechanismBaseline(func(_ context.Context, tenantID string) (memport.MechanismBaseline, error) {
		if tenantID != "tenant-1" {
			t.Fatalf("baseline resolved for tenant %q, want tenant-1", tenantID)
		}
		return memport.MechanismBaseline{
			MemoryEnrichment: "富化模板",
			MemorySummary:    "总结模板",
			EnrichModel:      "qwen-max",
			SummaryModel:     "qwen-plus",
		}, nil
	})
	eff := w.resolveEffective(context.Background(), "tenant-1")
	if eff.enrichmentTmpl != "富化模板" || eff.summaryTmpl != "总结模板" ||
		eff.model != "qwen-max" || eff.summaryModel != "qwen-plus" {
		t.Fatalf("baseline keys not applied: %+v", eff)
	}

	// 空键：不覆盖，维持构造时默认（每次 resolve 从构造字段起效）。
	w.WithMechanismBaseline(func(context.Context, string) (memport.MechanismBaseline, error) {
		return memport.MechanismBaseline{}, nil
	})
	if eff = w.resolveEffective(context.Background(), "tenant-1"); eff.model != "qwen-turbo" {
		t.Fatalf("empty baseline must keep constructor values, got %+v", eff)
	}

	// 解析失败：Warn + 维持构造值（配置源失败不阻断消息处理）。
	w.WithMechanismBaseline(func(context.Context, string) (memport.MechanismBaseline, error) {
		return memport.MechanismBaseline{}, errors.New("db down")
	})
	if eff = w.resolveEffective(context.Background(), "tenant-1"); eff.model != "qwen-turbo" {
		t.Fatalf("resolver error must keep constructor values, got %+v", eff)
	}
}
