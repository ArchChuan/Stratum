// Package observability provides monitoring and tracing.

package observability

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SpanContext carries trace/span IDs through context.
type SpanContext struct {
	TraceID string
	SpanID  string
	Name    string
	Start   time.Time
}

type spanKey struct{}

// TraceProvider is the pluggable tracing interface.
// LogTracer is the default implementation; swap with OTel exporter when available.
type TraceProvider interface {
	// StartSpan begins a span and returns a context containing SpanContext.
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Span represents an in-flight trace span.
type Span interface {
	// End finalises the span, recording elapsed time.
	End()
	// SetAttribute attaches a key-value pair to the span.
	SetAttribute(key string, value interface{})
	// RecordError marks the span as failed.
	RecordError(err error)
}

// TraceConfig defines tracing configuration.
type TraceConfig struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	ExporterType   string // "log", "otlp", "stdout", "none"
	SamplingRatio  float64
	JaegerEndpoint string
	OTLPEndpoint   string
}

// DefaultTraceConfig returns safe defaults for development.
func DefaultTraceConfig() *TraceConfig {
	return &TraceConfig{
		ServiceName:    "clawhermes-ai",
		ServiceVersion: "1.0.0",
		Environment:    "development",
		ExporterType:   "log",
		SamplingRatio:  1.0,
		JaegerEndpoint: "http://localhost:14268/api/traces",
		OTLPEndpoint:   "localhost:4317",
	}
}

// NewTraceProvider returns a TraceProvider for the given config.
// Currently supports "log" and "none"; extend here for OTel SDK.
func NewTraceProvider(cfg *TraceConfig, logger *zap.Logger) TraceProvider {
	switch cfg.ExporterType {
	case "none":
		return &noopTracer{}
	default:
		return &logTracer{logger: logger}
	}
}

// Tracer is kept for backwards compat with existing call sites.
type Tracer struct {
	provider TraceProvider
}

// NewTracer creates a Tracer backed by a LogTracer.
func NewTracer(logger *zap.Logger) *Tracer {
	return &Tracer{provider: &logTracer{logger: logger}}
}

// StartSpan delegates to the underlying provider.
func (t *Tracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	return t.provider.StartSpan(ctx, name)
}

// SpanFromContext retrieves the current SpanContext, if any.
func SpanFromContext(ctx context.Context) (SpanContext, bool) {
	sc, ok := ctx.Value(spanKey{}).(SpanContext)
	return sc, ok
}

// ---------------------------------------------------------------------------
// logTracer - structured-log based tracer with trace/span ID propagation
// ---------------------------------------------------------------------------

type logTracer struct {
	logger *zap.Logger
}

func (t *logTracer) StartSpan(ctx context.Context, name string) (context.Context, Span) {
	parent, _ := SpanFromContext(ctx)

	traceID := parent.TraceID
	if traceID == "" {
		traceID = uuid.New().String()
	}
	spanID := uuid.New().String()

	sc := SpanContext{
		TraceID: traceID,
		SpanID:  spanID,
		Name:    name,
		Start:   time.Now(),
	}
	newCtx := context.WithValue(ctx, spanKey{}, sc)

	t.logger.Debug("span started",
		zap.String("trace_id", traceID),
		zap.String("span_id", spanID),
		zap.String("span_name", name),
	)

	return newCtx, &logSpan{
		logger: t.logger,
		sc:     sc,
		attrs:  make(map[string]interface{}),
	}
}

type logSpan struct {
	logger *zap.Logger
	sc     SpanContext
	mu     sync.Mutex
	attrs  map[string]interface{}
	errMsg string
}

func (s *logSpan) End() {
	s.mu.Lock()
	elapsed := time.Since(s.sc.Start)
	fields := []zap.Field{
		zap.String("trace_id", s.sc.TraceID),
		zap.String("span_id", s.sc.SpanID),
		zap.String("span_name", s.sc.Name),
		zap.Duration("elapsed", elapsed),
	}
	for k, v := range s.attrs {
		fields = append(fields, zap.Any(k, v))
	}
	errMsg := s.errMsg
	s.mu.Unlock()

	if errMsg != "" {
		fields = append(fields, zap.String("error", errMsg))
		s.logger.Error("span ended with error", fields...)
		return
	}
	s.logger.Debug("span ended", fields...)
}

func (s *logSpan) SetAttribute(key string, value interface{}) {
	s.mu.Lock()
	s.attrs[key] = value
	s.mu.Unlock()
}

func (s *logSpan) RecordError(err error) {
	if err != nil {
		s.mu.Lock()
		s.errMsg = err.Error()
		s.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// noopTracer
// ---------------------------------------------------------------------------

type noopTracer struct{}

func (t *noopTracer) StartSpan(ctx context.Context, _ string) (context.Context, Span) {
	return ctx, &noopSpan{}
}

type noopSpan struct{}

func (*noopSpan) End()                                 {}
func (*noopSpan) SetAttribute(_ string, _ interface{}) {}
func (*noopSpan) RecordError(_ error)                  {}
