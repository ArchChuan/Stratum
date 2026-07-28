// Package llmgateway provides LLM gateway abstraction.
package infrastructure

import (
	"context"
	"fmt"
	"sort"
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

// LLM IO types defined in the domain layer; infrastructure re-exports them
// via aliases so consumers can import a single package.
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

// Gateway dispatches LLM calls through a ModelRegistry, which resolves
// model names to per-tenant provider configurations. Every tenant shares
// the same Gateway instance; per-tenant isolation lives in the registry.
type Gateway struct {
	registry *ModelRegistry
	metrics  observability.MetricsProvider
	logger   *zap.Logger
}

// NewGateway creates a Gateway backed by the given ModelRegistry.
func NewGateway(registry *ModelRegistry) *Gateway {
	return &Gateway{
		registry: registry,
		metrics:  observability.NoopMetrics{},
		logger:   zap.NewNop(),
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

// Complete sends a non-streaming completion request. The tenant is read from
// ctx via reqctx; the model is resolved through ModelRegistry.
func (g *Gateway) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	client, err := g.registry.ResolveChat(ctx, tenantID, req.Model)
	if err != nil {
		g.metrics.IncLLMRequest(req.Model, "unknown", llmStatusError)
		return nil, fmt.Errorf("gateway: resolve chat client: %w", err)
	}

	traceID := reqctx.TraceIDFromContext(ctx)
	fields := []zap.Field{
		zap.String("trace_id", traceID),
		zap.String("tenant_id", tenantID),
		zap.String("model", req.Model),
		zap.Int("tool_count", len(req.Tools)),
	}
	if req.ToolChoice != "" {
		fields = append(fields, zap.String("tool_choice", req.ToolChoice))
	}
	g.logger.Info("llm.request", fields...)

	start := time.Now()
	resp, err := client.Complete(ctx, req)
	elapsed := time.Since(start).Seconds()

	status := llmStatusSuccess
	provider := client.ProviderName()
	if err != nil {
		status = llmStatusError
	}
	g.metrics.IncLLMRequest(req.Model, provider, status)
	g.metrics.RecordLLMRequestDuration(req.Model, provider, elapsed)

	if err == nil && resp != nil {
		g.recordTokenMetrics(req.Model, provider, resp.Usage)
		g.logger.Info("llm.complete",
			zap.String("trace_id", traceID),
			zap.String("tenant_id", tenantID),
			zap.String("model", req.Model),
			zap.String("provider", provider),
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
			zap.String("provider", provider),
			zap.Bool("stream", false),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Error(err),
		)
	}
	return resp, err
}

// CompleteStream streams tokens from the LLM via onToken callback.
func (g *Gateway) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)

	// Start OTel span early so resolve failures are also traced.
	tracer := otel.Tracer("stratum/llmgateway")
	ctx, llmGWSpan := tracer.Start(ctx, "llm.complete",
		oteltrace.WithAttributes(
			attribute.String("llm.model", req.Model),
			attribute.Bool("llm.stream", true),
		),
	)
	defer llmGWSpan.End()

	client, err := g.registry.ResolveChat(ctx, tenantID, req.Model)
	if err != nil {
		llmGWSpan.SetStatus(codes.Error, err.Error())
		llmGWSpan.SetAttributes(attribute.String("llm.provider", "unknown"))
		g.metrics.IncLLMRequest(req.Model, "unknown", llmStatusError)
		return nil, fmt.Errorf("gateway: resolve chat client: %w", err)
	}

	streamTraceID := reqctx.TraceIDFromContext(ctx)
	provider := client.ProviderName()
	llmGWSpan.SetAttributes(attribute.String("llm.provider", provider))
	fields := []zap.Field{
		zap.String("trace_id", streamTraceID),
		zap.String("tenant_id", tenantID),
		zap.String("model", req.Model),
		zap.String("provider", provider),
		zap.Bool("stream", true),
		zap.Int("tool_count", len(req.Tools)),
	}
	if req.ToolChoice != "" {
		fields = append(fields, zap.String("tool_choice", req.ToolChoice))
	}
	g.logger.Info("llm.request", fields...)

	start := time.Now()

	var (
		resp         *CompletionResponse
		ttftRecorded bool
	)
	wrappedOnToken := func(t string) {
		if !ttftRecorded {
			ttftRecorded = true
			g.metrics.RecordLLMFirstTokenLatency(req.Model, provider, time.Since(start).Seconds())
		}
		onToken(t)
	}

	resp, err = client.CompleteStream(ctx, req, wrappedOnToken)
	elapsed := time.Since(start).Seconds()
	status := llmStatusSuccess
	if err != nil {
		status = llmStatusError
	}
	g.metrics.IncLLMRequest(req.Model, provider, status)
	g.metrics.RecordLLMRequestDuration(req.Model, provider, elapsed)

	if err == nil && resp != nil {
		llmGWSpan.SetAttributes(
			attribute.Int("llm.prompt_tokens", resp.Usage.PromptTokens),
			attribute.Int("llm.completion_tokens", resp.Usage.CompletionTokens),
		)
		g.recordTokenMetrics(req.Model, provider, resp.Usage)
		g.logger.Info("llm.complete",
			zap.String("trace_id", streamTraceID),
			zap.String("tenant_id", tenantID),
			zap.String("model", req.Model),
			zap.String("provider", provider),
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
			zap.String("tenant_id", tenantID),
			zap.String("model", req.Model),
			zap.String("provider", provider),
			zap.Bool("stream", true),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Error(err),
		)
	}
	return resp, err
}

func (g *Gateway) recordTokenMetrics(model, provider string, usage TokenUsage) {
	if usage.PromptTokens > 0 {
		g.metrics.IncLLMTokenUsage(model, "prompt", int64(usage.PromptTokens))
		g.metrics.RecordLLMTokenHistogram(model, "prompt", float64(usage.PromptTokens))
	}
	if usage.CompletionTokens > 0 {
		g.metrics.IncLLMTokenUsage(model, "completion", int64(usage.CompletionTokens))
		g.metrics.RecordLLMTokenHistogram(model, "completion", float64(usage.CompletionTokens))
	}
}

// CreateEmbeddings generates embeddings via the tenant's configured provider.
func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	client, err := g.registry.ResolveEmbedding(ctx, tenantID, req.Model)
	if err != nil {
		return nil, fmt.Errorf("gateway: resolve embedding client: %w", err)
	}
	return client.CreateEmbeddings(ctx, req)
}

// Health reports whether the gateway has a usable registry.
func (g *Gateway) Health(_ context.Context) error {
	if g.registry == nil {
		return fmt.Errorf("gateway: no model registry")
	}
	return nil
}

// ---- Static model catalogues ------------------------------------------------

// hardcoded chat models across all supported providers.
var allChatModels = func() []string {
	models := []string{
		// Qwen
		"qwen-max", "qwen-max-latest",
		"qwen-plus", "qwen-plus-latest",
		"qwen-turbo", "qwen-turbo-latest",
		"qwen-long",
		// Zhipu
		"glm-5.2",
		"glm-4.7-flashx", "glm-4.7-flash", "glm-4.5-flash",
		"glm-4-plus", "glm-4", "glm-4-air", "glm-4-flash", "glm-4v",
	}
	sort.Strings(models)
	return models
}()

// hardcoded embedding models across all supported providers.
var allEmbeddingModels = func() []string {
	models := []string{
		"text-embedding-v3", "text-embedding-v2",
		"embedding-3",
	}
	sort.Strings(models)
	return models
}()

// ListChatModels returns the static catalogue of all known chat models.
func (g *Gateway) ListChatModels() []string {
	return allChatModels
}

// ListEmbeddingModels returns the static catalogue of all known embedding models.
func (g *Gateway) ListEmbeddingModels() []string {
	return allEmbeddingModels
}

// ListChatModelsByTenant delegates to ModelRegistry for the tenant's chat models.
func (g *Gateway) ListChatModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return g.registry.ListChatModelsByTenant(ctx, tenantID)
}

// ListEmbeddingModelsByTenant delegates to ModelRegistry for the tenant's embedding models.
func (g *Gateway) ListEmbeddingModelsByTenant(ctx context.Context, tenantID string) ([]string, error) {
	return g.registry.ListEmbeddingModelsByTenant(ctx, tenantID)
}

// ---- Context helpers ---------------------------------------------------------

// WithGateway returns a new context carrying gw as the LLM gateway override.
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
