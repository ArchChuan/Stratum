package observability

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestDefaultTraceConfig(t *testing.T) {
	cfg := DefaultTraceConfig()
	if cfg == nil {
		t.Fatal("expected config to be non-nil")
	}
	if cfg.ServiceName == "" {
		t.Error("expected non-empty ServiceName")
	}
}

func TestNoopMetricsImplementsProvider(t *testing.T) {
	var _ MetricsProvider = NoopMetrics{}
}

func TestPrometheusMetricsImplementsProvider(t *testing.T) {
	logger := zap.NewNop()
	var _ MetricsProvider = NewPrometheusMetrics(logger)
}

func TestLogTracerStartSpan(t *testing.T) {
	logger := zap.NewNop()
	tracer := NewTracer(logger)

	ctx := context.Background()
	childCtx, span := tracer.StartSpan(ctx, "test.operation")
	if span == nil {
		t.Fatal("expected non-nil span")
	}

	sc, ok := SpanFromContext(childCtx)
	if !ok {
		t.Fatal("expected SpanContext in child context")
	}
	if sc.TraceID == "" {
		t.Error("expected non-empty TraceID")
	}
	if sc.SpanID == "" {
		t.Error("expected non-empty SpanID")
	}
	if sc.Name != "test.operation" {
		t.Errorf("expected span name 'test.operation', got %q", sc.Name)
	}

	span.SetAttribute("key", "value")
	span.End()
}

func TestLogTracerPropagatesTraceID(t *testing.T) {
	logger := zap.NewNop()
	tracer := NewTracer(logger)

	ctx := context.Background()
	ctx1, span1 := tracer.StartSpan(ctx, "parent")
	sc1, _ := SpanFromContext(ctx1)
	span1.End()

	ctx2, span2 := tracer.StartSpan(ctx1, "child")
	sc2, _ := SpanFromContext(ctx2)
	span2.End()

	if sc1.TraceID != sc2.TraceID {
		t.Errorf("child should inherit parent TraceID: parent=%s child=%s", sc1.TraceID, sc2.TraceID)
	}
	if sc1.SpanID == sc2.SpanID {
		t.Error("parent and child should have distinct SpanIDs")
	}
}

func TestNoopTracerProvider(t *testing.T) {
	cfg := &TraceConfig{ExporterType: "none"}
	p := NewTraceProvider(cfg, zap.NewNop())

	ctx, span := p.StartSpan(context.Background(), "op")
	span.End()
	span.SetAttribute("x", 1)
	span.RecordError(nil)

	_, ok := SpanFromContext(ctx)
	if ok {
		t.Error("noopTracer should not set SpanContext")
	}
}

func TestPrometheusMetricsLLM(t *testing.T) {
	logger := zap.NewNop()
	m := NewPrometheusMetrics(logger)

	m.IncLLMRequest("claude-3-sonnet", "claude", "success")
	m.RecordLLMRequestDuration("claude-3-sonnet", "claude", 1.5)
	m.IncLLMTokenUsage("claude-3-sonnet", "prompt", 512)
	m.RecordLLMTokenHistogram("claude-3-sonnet", "completion", 256)
	// RecordLLMFirstTokenLatency is available on *PrometheusMetrics directly (not in interface yet)
	m.RecordLLMFirstTokenLatency("claude-3-sonnet", "claude", 0.3)
}

func TestPrometheusMetricsAgent(t *testing.T) {
	logger := zap.NewNop()
	m := NewPrometheusMetrics(logger)

	m.IncAgentExecution("agent-1", "react", "success")
	m.RecordAgentExecutionDuration("agent-1", "react", 2.0)
	m.RecordAgentStepCount("agent-1", "react", 5)
}
