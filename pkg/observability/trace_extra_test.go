package observability

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

func TestWithTraceIDEmptyReturnsOriginalContext(t *testing.T) {
	// 极端情况：空 traceID 必须原样返回 ctx，不注入空值。
	ctx := context.Background()
	if got := WithTraceID(ctx, ""); got != ctx {
		t.Error("empty traceID must return original context")
	}
}

func TestSpanFromContextAbsent(t *testing.T) {
	// 极端情况：ctx 无 SpanContext 时 ok=false 且零值。
	sc, ok := SpanFromContext(context.Background())
	if ok {
		t.Error("expected ok=false for empty context")
	}
	if sc != (SpanContext{}) {
		t.Errorf("expected zero SpanContext, got %+v", sc)
	}
}

func TestWithTraceIDRoundTrip(t *testing.T) {
	ctx := WithTraceID(context.Background(), "trace-abc")
	sc, ok := SpanFromContext(ctx)
	if !ok {
		t.Fatal("expected SpanContext present")
	}
	if sc.TraceID != "trace-abc" {
		t.Errorf("TraceID = %q, want trace-abc", sc.TraceID)
	}
	if sc.SpanID == "" {
		t.Error("SpanID must be populated")
	}
	if sc.Name != "request" {
		t.Errorf("Name = %q, want request", sc.Name)
	}
}

func TestLogTracerStartSpanWithoutParentGeneratesTraceID(t *testing.T) {
	provider := NewTraceProvider(DefaultTraceConfig(), zap.NewNop())
	ctx, span := provider.StartSpan(context.Background(), "child")
	defer span.End()

	sc, ok := SpanFromContext(ctx)
	if !ok {
		t.Fatal("expected SpanContext in new context")
	}
	if sc.TraceID == "" || sc.SpanID == "" {
		t.Error("trace/span IDs must be generated")
	}
	if sc.Name != "child" {
		t.Errorf("Name = %q, want child", sc.Name)
	}
}

func TestLogTracerPreservesParentTraceID(t *testing.T) {
	provider := NewTraceProvider(DefaultTraceConfig(), zap.NewNop())
	ctx := WithTraceID(context.Background(), "trace-root")
	ctx, span := provider.StartSpan(ctx, "child")
	defer span.End()

	sc, _ := SpanFromContext(ctx)
	if sc.TraceID != "trace-root" {
		t.Errorf("TraceID = %q, want trace-root propagated", sc.TraceID)
	}
}

func TestSpanSetAttributeAndRecordError(t *testing.T) {
	provider := NewTraceProvider(DefaultTraceConfig(), zap.NewNop())
	_, span := provider.StartSpan(context.Background(), "op")
	span.SetAttribute("key", "value")
	span.SetAttribute("num", 42)
	span.RecordError(nil) // 极端情况：nil error 不记录
	span.RecordError(errors.New("boom"))
	span.End()
	// 双 End 不 panic（幂等性约束由调用方保证，这里仅冒烟）。
	span.End()
}

func TestNoopTracerProviderKeepsContext(t *testing.T) {
	// none exporter 返回 noopTracer。
	cfg := DefaultTraceConfig()
	cfg.ExporterType = "none"
	provider := NewTraceProvider(cfg, zap.NewNop())
	ctx, span := provider.StartSpan(context.Background(), "x")
	defer span.End()
	span.SetAttribute("k", "v")
	span.RecordError(errors.New("ignored"))
	if _, ok := SpanFromContext(ctx); ok {
		t.Error("noop tracer must not inject SpanContext")
	}
}

func TestNewTracerDelegates(t *testing.T) {
	tr := NewTracer(zap.NewNop())
	ctx, span := tr.StartSpan(context.Background(), "via-tracer")
	defer span.End()
	sc, ok := SpanFromContext(ctx)
	if !ok || sc.Name != "via-tracer" {
		t.Errorf("delegation failed: %+v ok=%v", sc, ok)
	}
}

func TestDefaultTraceConfigValues(t *testing.T) {
	cfg := DefaultTraceConfig()
	if cfg.ServiceName != "stratum-ai" {
		t.Errorf("ServiceName = %q", cfg.ServiceName)
	}
	if cfg.ExporterType != "log" {
		t.Errorf("ExporterType = %q, want log", cfg.ExporterType)
	}
	if cfg.SamplingRatio != 1.0 {
		t.Errorf("SamplingRatio = %v, want 1.0", cfg.SamplingRatio)
	}
}

// sampler 四类断言：agent 根恒采、非 agent 根按 ratio、子 span 跟随父决策。

func TestAgentSamplerAgentRootAlwaysSampled(t *testing.T) {
	// ratio=0（其余全部丢弃）时，带 agent 属性的根 span 仍必须 RecordAndSample。
	s := NewAgentSampler(0)
	res := s.ShouldSample(sdktrace.SamplingParameters{
		Name:       "agent.execute",
		Kind:       trace.SpanKindInternal,
		Attributes: []attribute.KeyValue{attribute.Bool(AgentExecuteAttrKey, true)},
	})
	if res.Decision != sdktrace.RecordAndSample {
		t.Errorf("agent root decision = %v, want RecordAndSample", res.Decision)
	}
}

func TestAgentSamplerRootWithoutAgentAttrDelegatesToRatio(t *testing.T) {
	// 非 agent 根 span 不做恒采：ratio=0 必弃、ratio=1 必采，证明决策委派给
	// TraceIDRatioBased（而非恒采恒弃）。ratio 中间值的精确边界由 SDK 自身
	// 单测覆盖，这里验证委派接线。
	params := sdktrace.SamplingParameters{
		TraceID: trace.TraceID([16]byte{0x01}),
		Name:    "other.op",
		Kind:    trace.SpanKindInternal,
	}

	if got := NewAgentSampler(0).ShouldSample(params); got.Decision != sdktrace.Drop {
		t.Errorf("ratio=0 decision = %v, want Drop", got.Decision)
	}
	if got := NewAgentSampler(1).ShouldSample(params); got.Decision != sdktrace.RecordAndSample {
		t.Errorf("ratio=1 decision = %v, want RecordAndSample", got.Decision)
	}
}

func TestParentBasedChildFollowsParent(t *testing.T) {
	// ParentBased(agentSampler)：子 span 不重新决策，跟随父 span 采样位。
	parentCtx := func(sampled bool) context.Context {
		flags := trace.TraceFlags(0)
		if sampled {
			flags = trace.FlagsSampled
		}
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID([16]byte{0xAB, 0x01}),
			SpanID:     trace.SpanID([8]byte{0xCD, 0x02}),
			TraceFlags: flags,
		})
		return trace.ContextWithSpanContext(context.Background(), sc)
	}

	s := sdktrace.ParentBased(NewAgentSampler(0)) // agent 恒采，但子 span 走父决策
	childParams := func(parent context.Context) sdktrace.SamplingParameters {
		return sdktrace.SamplingParameters{
			ParentContext: parent,
			TraceID:       trace.TraceID([16]byte{0xAB, 0x01}),
			Name:          "agent.execute.child",
			Kind:          trace.SpanKindInternal,
		}
	}

	if got := s.ShouldSample(childParams(parentCtx(true))); got.Decision != sdktrace.RecordAndSample {
		t.Errorf("sampled parent child = %v, want RecordAndSample", got.Decision)
	}
	if got := s.ShouldSample(childParams(parentCtx(false))); got.Decision != sdktrace.Drop {
		t.Errorf("dropped parent child = %v, want Drop", got.Decision)
	}
}

func TestNewLogger(t *testing.T) {
	prod, err := NewLogger("production")
	if err != nil {
		t.Fatalf("production logger: %v", err)
	}
	if prod == nil {
		t.Fatal("production logger is nil")
	}
	dev, err := NewLogger("development")
	if err != nil {
		t.Fatalf("dev logger: %v", err)
	}
	if dev == nil {
		t.Fatal("dev logger is nil")
	}
	// 未知 env 走非生产路径，不报错。
	if _, err := NewLogger(""); err != nil {
		t.Errorf("empty env must not error: %v", err)
	}
	// Logger 兼容包装可嵌入使用。
	wrapped := Logger{Logger: dev}
	if wrapped.Sugar() == nil {
		t.Error("wrapped logger broken")
	}
}
