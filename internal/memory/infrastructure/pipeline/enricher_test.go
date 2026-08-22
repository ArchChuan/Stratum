package pipeline

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// stubPlatformResolver 返回固定 key→value 映射；缺失 key 返回 present=false，
// 模拟平台存储未配置该 key。
type stubPlatformResolver struct {
	values map[string]any
}

func (s stubPlatformResolver) ResolvePlatform(_ context.Context, key string) (any, bool, error) {
	v, ok := s.values[key]
	return v, ok, nil
}

// TestEnricherResolveDefaults 验证解析期默认：resolver 缺失（nil）时
// enrich/summary 模型默认必须为空——代码内不写死兜底模型，空模型交由
// llmgateway 从模型目录解析默认；温度/阈值仍取 pkg/constants 默认。
func TestEnricherResolveDefaults(t *testing.T) {
	w := NewEnricherWorker(nil, nil, nil, zap.NewNop(), Config{})
	ctx := context.Background()

	enrich := w.resolveEnrichSettings(ctx)
	if enrich.model != "" {
		t.Fatalf("enrich default model = %q, want empty (gateway resolves from catalog)", enrich.model)
	}
	if enrich.temperature != constants.MemoryEnrichLLMTemperature {
		t.Fatalf("enrich default temperature = %v, want %v", enrich.temperature, constants.MemoryEnrichLLMTemperature)
	}

	summary := w.resolveSummarySettings(ctx)
	if summary.model != "" {
		t.Fatalf("summary default model = %q, want empty (gateway resolves from catalog)", summary.model)
	}
	if summary.temperature != constants.TaskSummarizeTemperature {
		t.Fatalf("summary default temperature = %v, want %v", summary.temperature, constants.TaskSummarizeTemperature)
	}
	if summary.threshold != constants.EnricherSummaryTokenThreshold {
		t.Fatalf("summary default threshold = %d, want %d", summary.threshold, constants.EnricherSummaryTokenThreshold)
	}
}

// TestEnricherResolvePlatformValues 验证平台解析值生效：resolver 返回的平台
// 模型/温度/阈值覆盖空默认（运行态热改的解析期断言）。
func TestEnricherResolvePlatformValues(t *testing.T) {
	w := NewEnricherWorker(nil, nil, nil, zap.NewNop(), Config{})
	w.paramResolver = stubPlatformResolver{values: map[string]any{
		"memory.enrich_model":            "glm-4.5-air",
		"memory.enrich_temperature":      float64(0.3),
		"memory.summary_model":           "qwen-max",
		"memory.summary_temperature":     float64(0.5),
		"memory.summary_token_threshold": int64(2500),
	}}
	ctx := context.Background()

	enrich := w.resolveEnrichSettings(ctx)
	if enrich.model != "glm-4.5-air" {
		t.Fatalf("enrich platform model = %q, want glm-4.5-air", enrich.model)
	}
	if enrich.temperature != 0.3 {
		t.Fatalf("enrich platform temperature = %v, want 0.3", enrich.temperature)
	}

	summary := w.resolveSummarySettings(ctx)
	if summary.model != "qwen-max" {
		t.Fatalf("summary platform model = %q, want qwen-max", summary.model)
	}
	if summary.temperature != 0.5 {
		t.Fatalf("summary platform temperature = %v, want 0.5", summary.temperature)
	}
	if summary.threshold != 2500 {
		t.Fatalf("summary platform threshold = %d, want 2500", summary.threshold)
	}
}

// TestNewSummaryLLMRequestRoundsPlatformTemperature 回归 PR #441 漏网覆盖点：
// 会话摘要请求的平台温度必须经 PlatformTemperaturePtr 舍入 2 位小数，
// float64(float32(0.2)) 直转会变成 0.20000000298023224 触发智谱 400；
// 平台温度 0 保持 unset（nil，走网关默认）。
func TestNewSummaryLLMRequestRoundsPlatformTemperature(t *testing.T) {
	req := newSummaryLLMRequest(summarySettings{model: "qwen-max", temperature: 0.2}, "prompt")
	if req.Temperature == nil || *req.Temperature != 0.2 {
		t.Fatalf("platform summary temperature = %v, want 0.2 (2 位小数)", req.Temperature)
	}
	zero := newSummaryLLMRequest(summarySettings{model: "qwen-max", temperature: 0}, "prompt")
	if zero.Temperature != nil {
		t.Fatalf("zero platform temperature must keep unset (nil), got %v", zero.Temperature)
	}
}
