// Package observability provides monitoring and tracing.

package observability

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// AgentExecuteAttrKey marks an agent execution root span that must always be
// sampled, overriding both head-sampling ratio and the collector tail_sampling
// default policy (which would otherwise drop agent executions at 10%).
const AgentExecuteAttrKey = "stratum.agent.execute"

// agentSampler always RecordAndSample's a root span carrying the agent
// execution attribute, so every agent.execute root span reaches the collector
// regardless of entry path (HTTP, workflow, scheduler, NATS, MCP). All other
// root spans delegate to a ratio-based sampler; children are decided by
// sdktrace.ParentBased (follow the parent's decision).
//
// ParentBased only consults this sampler for parent-less root spans, so the
// caller must create agent.execute with WithNewRoot(): otherwise an HTTP entry
// would make it a child of the otelgin root span and ParentBased would short-
// circuit on the parent's sampling bit without ever calling ShouldSample here.
type agentSampler struct {
	ratio sdktrace.Sampler
}

// NewAgentSampler returns the agent-preserving head sampler: a root span
// carrying AgentExecuteAttrKey is always RecordAndSample'd; everything else
// delegates to a TraceIDRatioBased sampler at the given ratio. Wire it as
// sdktrace.ParentBased(NewAgentSampler(ratio)) so children follow the root's
// decision. Exported so tests can reproduce the HTTP-entry scenario where
// agent.execute must survive a dropped parent root.
func NewAgentSampler(ratio float64) sdktrace.Sampler {
	return &agentSampler{ratio: sdktrace.TraceIDRatioBased(ratio)}
}

// Description implements sdktrace.Sampler.
func (s *agentSampler) Description() string {
	return "agentSampler"
}

// ShouldSample implements sdktrace.Sampler. ParentBased only consults this
// sampler for root spans; the parent check is defensive.
func (s *agentSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if trace.SpanContextFromContext(p.ParentContext).IsValid() {
		return s.ratio.ShouldSample(p)
	}
	for _, kv := range p.Attributes {
		if kv.Key == attribute.Key(AgentExecuteAttrKey) && kv.Value.AsBool() {
			return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
		}
	}
	return s.ratio.ShouldSample(p)
}

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
		ServiceName:    "stratum-ai",
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

// WithTraceID seeds ctx with a SpanContext carrying the given traceID so that
// any subsequent StartSpan call in the same request propagates the same ID.
// Call this once per request in the HTTP middleware after the trace ID is known.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	if traceID == "" {
		return ctx
	}
	sc := SpanContext{
		TraceID: traceID,
		SpanID:  uuid.Must(uuid.NewV7()).String(),
		Name:    "request",
		Start:   time.Now(),
	}
	return context.WithValue(ctx, spanKey{}, sc)
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
		traceID = uuid.Must(uuid.NewV7()).String()
	}
	spanID := uuid.Must(uuid.NewV7()).String()

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

// ---------------------------------------------------------------------------
// OTel SDK provider — used when ExporterType == "otlp"
// ---------------------------------------------------------------------------

// probeOTLPEndpoint verifies the OTLP endpoint accepts TCP connections.
// Short explicit budget: DNS hangs (WSL2 lookup can stall ~30s) and refused
// connections must both fail here, bounded by ctx (caller passes a 5s init
// ctx) and the dialer timeout.
func probeOTLPEndpoint(ctx context.Context, endpoint string) error {
	d := net.Dialer{Timeout: 2 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// InitOTelProvider creates an OTel TracerProvider that exports spans to the
// OTLP gRPC endpoint in cfg.OTLPEndpoint, registers it as the global provider,
// and returns a shutdown function the caller must invoke on exit.
// The endpoint must be host:port without a scheme (e.g. "otel-collector:4317").
func InitOTelProvider(ctx context.Context, cfg *TraceConfig) (func(context.Context) error, error) {
	endpoint := strings.TrimPrefix(cfg.OTLPEndpoint, "http://")
	endpoint = strings.TrimPrefix(endpoint, "https://")

	// otlptracegrpc.New is a lazy dial: an unreachable collector still
	// constructs successfully, so the BatchSpanProcessor starts and then
	// retries exports forever ("traces export: exporter export timeout"
	// every ~15s). Probe TCP reachability first and fail fast so callers
	// can degrade to the log tracer instead of spinning forever.
	if err := probeOTLPEndpoint(ctx, endpoint); err != nil {
		return nil, fmt.Errorf("otlp endpoint %s unreachable: %w", endpoint, err)
	}

	// WithRetry enables gRPC export-level retry with bounded backoff. Without
	// it, a transient collector outage drops the whole batch silently: the
	// BatchSpanProcessor does not retry failed exports by itself, so spans of
	// in-flight traces (long agent executions included) are lost.
	exporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(endpoint),
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithRetry(otlptracegrpc.RetryConfig{
			Enabled:         true,
			InitialInterval: 500 * time.Millisecond,
			MaxInterval:     30 * time.Second,
			MaxElapsedTime:  5 * time.Minute,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("otlp exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			attribute.String("environment", cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// agent.execute 根 span 恒采样（见 NewAgentSampler），其余按 ratio；子 span 跟随父。
		sdktrace.WithSampler(sdktrace.ParentBased(NewAgentSampler(cfg.SamplingRatio))),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return tp.Shutdown, nil
}
