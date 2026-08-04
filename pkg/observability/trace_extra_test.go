package observability

import (
	"context"
	"errors"
	"testing"

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
