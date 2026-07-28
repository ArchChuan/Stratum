// Package llmgateway provides LLM gateway abstraction.
package infrastructure

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

const (
	llmStatusSuccess = "success"
	llmStatusError   = "error"
)

// LLM IO 类型在 domain 层定义；infra 通过 alias 暴露给内部实现，
// 同时允许跨 ctx 消费者直接 import domain，避免越层依赖。
type (
	Tool               = domain.Tool
	ToolFunction       = domain.ToolFunction
	ToolCall           = domain.ToolCall
	Message            = domain.Message
	CompletionRequest  = domain.CompletionRequest
	TokenUsage         = domain.TokenUsage
	CompletionResponse = domain.CompletionResponse
	EmbeddingRequest   = domain.EmbeddingRequest
	EmbeddingResponse  = domain.EmbeddingResponse
)

// openAICompletionResp is the shared decode type for OpenAI-compatible completion responses.
type openAICompletionResp struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Model string     `json:"model"`
	Usage TokenUsage `json:"usage"`
}

// openAIStreamChunk is the shared decode type for OpenAI-compatible SSE stream chunks.
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                `json:"content"`
			ToolCalls []streamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Model string      `json:"model"`
	Usage *TokenUsage `json:"usage"`
}

// streamToolCallDelta is the per-chunk tool call fragment from an SSE stream.
// Index identifies which tool call slot this delta belongs to.
type streamToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAIEmbedResp is the shared decode type for OpenAI-compatible embedding responses.
type openAIEmbedResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

// Gateway delegates LLM requests to the ModelRegistry, resolving each model
// name to a provider configuration and protocol at call time.
type Gateway struct {
	registry    *ModelRegistry
	chatProtos  map[domain.ProviderKind]ChatProtocol
	embedProtos map[domain.ProviderKind]EmbedProtocol
	metrics     observability.MetricsProvider
	logger      *zap.Logger
}

// NewGateway creates a Gateway with a no-op metrics provider.
// Call WithMetrics to inject a real provider.
func NewGateway(registry *ModelRegistry, chatProtos map[domain.ProviderKind]ChatProtocol, embedProtos map[domain.ProviderKind]EmbedProtocol) *Gateway {
	return &Gateway{
		registry:    registry,
		chatProtos:  chatProtos,
		embedProtos: embedProtos,
		metrics:     observability.NoopMetrics{},
		logger:      zap.NewNop(),
	}
}

// WithMetrics injects a MetricsProvider into the gateway.
func (g *Gateway) WithMetrics(m observability.MetricsProvider) *Gateway {
	g.metrics = m
	return g
}

// WithLogger injects a logger into the gateway.
func (g *Gateway) WithLogger(l *zap.Logger) *Gateway {
	g.logger = l
	return g
}

// Complete resolves the model via the registry and delegates to the resolved
// ChatProtocol.
func (g *Gateway) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	cfg, proto, err := g.registry.Resolve(ctx, tenantID, req.Model)
	if err != nil {
		g.metrics.IncLLMRequest(req.Model, "unknown", llmStatusError)
		return nil, fmt.Errorf("llmgateway: resolve model %q: %w", req.Model, err)
	}

	traceID := reqctx.TraceIDFromContext(ctx)
	fields := []zap.Field{
		zap.String("trace_id", traceID),
		zap.String("tenant_id", tenantID),
		zap.String("model", req.Model),
		zap.String("provider", cfg.Name),
		zap.Int("tool_count", len(req.Tools)),
	}
	if req.ToolChoice != "" {
		fields = append(fields, zap.String("tool_choice", req.ToolChoice))
	}
	g.logger.Info("llm.request", fields...)

	start := time.Now()
	resp, err := proto.Complete(ctx, cfg, req)
	elapsed := time.Since(start).Seconds()

	status := llmStatusSuccess
	if err != nil {
		status = llmStatusError
	}

	g.metrics.IncLLMRequest(req.Model, cfg.Name, status)
	g.metrics.RecordLLMRequestDuration(req.Model, cfg.Name, elapsed)

	if err == nil && resp != nil {
		if resp.Usage.PromptTokens > 0 {
			g.metrics.IncLLMTokenUsage(req.Model, "prompt", int64(resp.Usage.PromptTokens))
			g.metrics.RecordLLMTokenHistogram(req.Model, "prompt", float64(resp.Usage.PromptTokens))
		}
		if resp.Usage.CompletionTokens > 0 {
			g.metrics.IncLLMTokenUsage(req.Model, "completion", int64(resp.Usage.CompletionTokens))
			g.metrics.RecordLLMTokenHistogram(req.Model, "completion", float64(resp.Usage.CompletionTokens))
		}
		g.logger.Info("llm.complete",
			zap.String("trace_id", traceID),
			zap.String("tenant_id", tenantID),
			zap.String("model", req.Model),
			zap.String("provider", cfg.Name),
			zap.Bool("stream", false),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Int("prompt_tokens", resp.Usage.PromptTokens),
			zap.Int("completion_tokens", resp.Usage.CompletionTokens),
		)
	} else if err != nil {
		g.logger.Error("llm.complete",
			zap.String("trace_id", traceID),
			zap.String("tenant_id", tenantID),
			zap.String("model", req.Model),
			zap.String("provider", cfg.Name),
			zap.Bool("stream", false),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Error(err),
		)
	}

	return resp, err
}

// CompleteStream resolves the model via the registry and delegates to the
// resolved ChatProtocol's streaming method.
func (g *Gateway) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	cfg, proto, err := g.registry.Resolve(ctx, tenantID, req.Model)
	if err != nil {
		g.metrics.IncLLMRequest(req.Model, "unknown", llmStatusError)
		return nil, fmt.Errorf("llmgateway: resolve model %q: %w", req.Model, err)
	}

	streamTraceID := reqctx.TraceIDFromContext(ctx)
	streamTenantID := tenantID
	fields := []zap.Field{
		zap.String("trace_id", streamTraceID),
		zap.String("tenant_id", streamTenantID),
		zap.String("model", req.Model),
		zap.String("provider", cfg.Name),
		zap.Bool("stream", true),
		zap.Int("tool_count", len(req.Tools)),
	}
	if req.ToolChoice != "" {
		fields = append(fields, zap.String("tool_choice", req.ToolChoice))
	}
	g.logger.Info("llm.request", fields...)

	start := time.Now()
	tracer := otel.Tracer("stratum/llmgateway")
	ctx, llmGWSpan := tracer.Start(ctx, "llm.complete",
		oteltrace.WithAttributes(
			attribute.String("llm.model", req.Model),
			attribute.String("llm.provider", cfg.Name),
			attribute.Bool("llm.stream", true),
		),
	)
	defer llmGWSpan.End()

	var (
		resp         *CompletionResponse
		ttftRecorded bool
	)
	wrappedOnToken := func(t string) {
		if !ttftRecorded {
			ttftRecorded = true
			g.metrics.RecordLLMFirstTokenLatency(req.Model, cfg.Name, time.Since(start).Seconds())
		}
		onToken(t)
	}

	resp, err = proto.CompleteStream(ctx, cfg, req, wrappedOnToken)
	elapsed := time.Since(start).Seconds()

	status := llmStatusSuccess
	if err != nil {
		status = llmStatusError
	}

	g.metrics.IncLLMRequest(req.Model, cfg.Name, status)
	g.metrics.RecordLLMRequestDuration(req.Model, cfg.Name, elapsed)

	if err == nil && resp != nil {
		llmGWSpan.SetAttributes(
			attribute.Int("llm.prompt_tokens", resp.Usage.PromptTokens),
			attribute.Int("llm.completion_tokens", resp.Usage.CompletionTokens),
		)
		if resp.Usage.PromptTokens > 0 {
			g.metrics.IncLLMTokenUsage(req.Model, "prompt", int64(resp.Usage.PromptTokens))
		}
		if resp.Usage.CompletionTokens > 0 {
			g.metrics.IncLLMTokenUsage(req.Model, "completion", int64(resp.Usage.CompletionTokens))
		}
		g.logger.Info("llm.complete",
			zap.String("trace_id", streamTraceID),
			zap.String("tenant_id", streamTenantID),
			zap.String("model", req.Model),
			zap.String("provider", cfg.Name),
			zap.Bool("stream", true),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Int("prompt_tokens", resp.Usage.PromptTokens),
			zap.Int("completion_tokens", resp.Usage.CompletionTokens),
		)
	} else if err != nil {
		llmGWSpan.RecordError(err)
		llmGWSpan.SetStatus(codes.Error, "llm provider call failed")
		g.logger.Error("llm.complete",
			zap.String("trace_id", streamTraceID),
			zap.String("tenant_id", streamTenantID),
			zap.String("model", req.Model),
			zap.String("provider", cfg.Name),
			zap.Bool("stream", true),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Error(err),
		)
	}

	return resp, err
}

// CreateEmbeddings resolves the embedding model via the registry and delegates
// to the resolved EmbedProtocol.
func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	cfg, proto, err := g.registry.ResolveEmbedding(ctx, tenantID, req.Model)
	if err != nil {
		return nil, fmt.Errorf("llmgateway: resolve embedding model %q: %w", req.Model, err)
	}
	return proto.CreateEmbeddings(ctx, cfg, req)
}

// Health returns nil. Per-tenant health checks are delegated to the
// ModelRegistry and are not performed at the global Gateway level.
func (g *Gateway) Health(context.Context) error {
	return nil
}

// ListEmbeddingModels returns an empty slice. Tenant-scoped model lists are
// available via ListEmbeddingModelsByTenant.
func (g *Gateway) ListEmbeddingModels() []string {
	return []string{}
}

// ListChatModels returns an empty slice. Tenant-scoped model lists are
// available via ListChatModelsByTenant.
func (g *Gateway) ListChatModels() []string {
	return []string{}
}

// ListChatModelsByTenant returns sorted enabled chat model names for the
// given tenant, delegating to the registry.
func (g *Gateway) ListChatModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return g.registry.ListChatModels(ctx, tenantID)
}

// ListEmbeddingModelsByTenant returns sorted enabled embedding model names
// for the given tenant, delegating to the registry.
func (g *Gateway) ListEmbeddingModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return g.registry.ListEmbeddingModels(ctx, tenantID)
}

// WithGateway returns a new context carrying gw as the LLM gateway override.
// 内部委派给 domain.WithCompleter，使消费方可仅依赖 domain 接口。
func WithGateway(ctx context.Context, gw *Gateway) context.Context {
	return domain.WithCompleter(ctx, gw)
}

// GatewayFromContext returns the gateway stored in ctx (from WithGateway), or (nil, false).
func GatewayFromContext(ctx context.Context) (*Gateway, bool) {
	c, ok := domain.CompleterFromContext(ctx)
	if !ok {
		return nil, false
	}
	gw, ok := c.(*Gateway)
	return gw, ok
}
