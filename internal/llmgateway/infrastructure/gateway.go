// Package llmgateway provides LLM gateway abstraction.
package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
)

type ModelProvider string

const (
	ProviderQwen  ModelProvider = "qwen"
	ProviderZhipu ModelProvider = "zhipu"
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

type LLMClient interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	Health(ctx context.Context) error
	// Models returns the chat model names supported by this provider.
	Models() []string
}

// StreamingLLMClient is an optional extension of LLMClient for providers that support token streaming.
type StreamingLLMClient interface {
	CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error)
}

type EmbeddingClient interface {
	CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}

type Gateway struct {
	clients          map[ModelProvider]LLMClient
	embeddingClients map[ModelProvider]EmbeddingClient
	defaultProvider  ModelProvider
	metrics          observability.MetricsProvider
	logger           *zap.Logger
}

// NewGateway creates a Gateway with a no-op metrics provider.
// Call WithMetrics to inject a real provider.
func NewGateway() *Gateway {
	return &Gateway{
		clients:          make(map[ModelProvider]LLMClient),
		embeddingClients: make(map[ModelProvider]EmbeddingClient),
		metrics:          observability.NoopMetrics{},
		logger:           zap.NewNop(),
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

func (g *Gateway) RegisterClient(provider ModelProvider, client LLMClient) {
	g.clients[provider] = client
}

func (g *Gateway) RegisterEmbeddingClient(provider ModelProvider, client EmbeddingClient) {
	g.embeddingClients[provider] = client
}

func (g *Gateway) HasEmbeddingClient() bool {
	return len(g.embeddingClients) > 0
}

// DefaultEmbeddingModel returns the appropriate embedding model name for the default provider.
func (g *Gateway) DefaultEmbeddingModel() string {
	switch g.defaultProvider {
	case ProviderQwen:
		return "text-embedding-v3"
	case ProviderZhipu:
		return "embedding-3"
	default:
		return "text-embedding-3-small"
	}
}

func (g *Gateway) SetDefault(provider ModelProvider) {
	g.defaultProvider = provider
}

func (g *Gateway) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
	provider := g.defaultProvider
	if req.Model != "" {
		provider = g.parseProvider(req.Model)
	}

	client, ok := g.clients[provider]
	if !ok {
		g.metrics.IncLLMRequest(req.Model, string(provider), "error")
		return nil, fmt.Errorf("provider not found: %s", provider)
	}

	traceID := reqctx.TraceIDFromContext(ctx)
	tenantID := reqctx.TenantIDFromContext(ctx)
	if raw, merr := json.Marshal(req.Messages); merr == nil {
		fields := []zap.Field{
			zap.String("trace_id", traceID),
			zap.String("tenant_id", tenantID),
			zap.String("model", req.Model),
			zap.String("provider", string(provider)),
			zap.ByteString("messages", raw),
			zap.Int("tool_count", len(req.Tools)),
		}
		if len(req.Tools) > 0 {
			if toolsRaw, terr := json.Marshal(req.Tools); terr == nil {
				fields = append(fields, zap.ByteString("tools", toolsRaw))
			}
			if req.ToolChoice != "" {
				fields = append(fields, zap.String("tool_choice", req.ToolChoice))
			}
		}
		g.logger.Info("llm.request", fields...)
	}

	start := time.Now()
	resp, err := client.Complete(ctx, req)
	elapsed := time.Since(start).Seconds()

	status := "success"
	if err != nil {
		status = "error"
	}

	g.metrics.IncLLMRequest(req.Model, string(provider), status)
	g.metrics.RecordLLMRequestDuration(req.Model, string(provider), elapsed)

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
			zap.String("provider", string(provider)),
			zap.Bool("stream", false),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Int("prompt_tokens", resp.Usage.PromptTokens),
			zap.Int("completion_tokens", resp.Usage.CompletionTokens),
		)
		if g.logger.Core().Enabled(zap.DebugLevel) {
			if raw, merr := json.Marshal(resp); merr == nil {
				g.logger.Debug("llm.response",
					zap.String("trace_id", traceID),
					zap.String("model", req.Model),
					zap.ByteString("output", raw),
				)
			}
		}
	} else if err != nil {
		g.logger.Error("llm.complete",
			zap.String("trace_id", traceID),
			zap.String("tenant_id", tenantID),
			zap.String("model", req.Model),
			zap.String("provider", string(provider)),
			zap.Bool("stream", false),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Error(err),
		)
	}

	return resp, err
}

// CompleteStream streams tokens from the LLM via onToken callback.
// Falls back to non-streaming Complete if the provider does not implement StreamingLLMClient.
func (g *Gateway) CompleteStream(ctx context.Context, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
	provider := g.parseProvider(req.Model)
	client, ok := g.clients[provider]
	if !ok {
		g.metrics.IncLLMRequest(req.Model, string(provider), "error")
		return nil, fmt.Errorf("llmgateway: no client for provider %q", provider)
	}
	if g.logger.Core().Enabled(zap.DebugLevel) {
		if raw, merr := json.Marshal(req.Messages); merr == nil {
			g.logger.Debug("llm.request", zap.String("model", req.Model), zap.ByteString("messages", raw))
		}
	}
	streamTraceID := reqctx.TraceIDFromContext(ctx)
	streamTenantID := reqctx.TenantIDFromContext(ctx)
	if raw, merr := json.Marshal(req.Messages); merr == nil {
		fields := []zap.Field{
			zap.String("trace_id", streamTraceID),
			zap.String("tenant_id", streamTenantID),
			zap.String("model", req.Model),
			zap.String("provider", string(provider)),
			zap.ByteString("messages", raw),
			zap.Int("tool_count", len(req.Tools)),
		}
		if len(req.Tools) > 0 {
			if toolsRaw, terr := json.Marshal(req.Tools); terr == nil {
				fields = append(fields, zap.ByteString("tools", toolsRaw))
			}
			if req.ToolChoice != "" {
				fields = append(fields, zap.String("tool_choice", req.ToolChoice))
			}
		}
		g.logger.Info("llm.request", fields...)
	}
	start := time.Now()
	var (
		resp *CompletionResponse
		err  error
	)
	if sc, ok := client.(StreamingLLMClient); ok {
		resp, err = sc.CompleteStream(ctx, req, onToken)
	} else {
		resp, err = client.Complete(ctx, req)
		if err == nil && resp != nil {
			onToken(resp.Content)
		}
	}
	elapsed := time.Since(start).Seconds()
	status := "success"
	if err != nil {
		status = "error"
	}
	g.metrics.IncLLMRequest(req.Model, string(provider), status)
	g.metrics.RecordLLMRequestDuration(req.Model, string(provider), elapsed)
	if err == nil && resp != nil {
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
			zap.String("provider", string(provider)),
			zap.Bool("stream", true),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Int("prompt_tokens", resp.Usage.PromptTokens),
			zap.Int("completion_tokens", resp.Usage.CompletionTokens),
		)
		if g.logger.Core().Enabled(zap.DebugLevel) {
			if raw, merr := json.Marshal(resp); merr == nil {
				g.logger.Debug("llm.response",
					zap.String("trace_id", streamTraceID),
					zap.String("model", req.Model),
					zap.ByteString("output", raw),
				)
			}
		}
	} else if err != nil {
		g.logger.Error("llm.complete",
			zap.String("trace_id", streamTraceID),
			zap.String("tenant_id", streamTenantID),
			zap.String("model", req.Model),
			zap.String("provider", string(provider)),
			zap.Bool("stream", true),
			zap.Int64("latency_ms", int64(elapsed*1000)),
			zap.Error(err),
		)
	}
	return resp, err
}

func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	provider := g.parseProvider(req.Model)
	client, ok := g.embeddingClients[provider]
	if !ok {
		client, ok = g.embeddingClients[g.defaultProvider]
		if !ok {
			return nil, fmt.Errorf("no embedding client registered")
		}
	}
	return client.CreateEmbeddings(ctx, req)
}

func (g *Gateway) Health(ctx context.Context) error {
	for provider, client := range g.clients {
		if err := client.Health(ctx); err != nil {
			return fmt.Errorf("provider %s health check failed: %w", provider, err)
		}
	}
	return nil
}

// ListEmbeddingModels returns embedding model names for all registered embedding providers, sorted.
func (g *Gateway) ListEmbeddingModels() []string {
	var models []string
	for provider := range g.embeddingClients {
		switch provider {
		case ProviderQwen:
			models = append(models, "text-embedding-v3", "text-embedding-v2")
		case ProviderZhipu:
			models = append(models, "embedding-3")
		default:
			models = append(models, "text-embedding-3-small", "text-embedding-3-large", "text-embedding-ada-002")
		}
	}
	sort.Strings(models)
	return models
}

// ListChatModels returns all chat model names across registered providers, sorted.
func (g *Gateway) ListChatModels() []string {
	var models []string
	for _, client := range g.clients {
		models = append(models, client.Models()...)
	}
	sort.Strings(models)
	return models
}

func (g *Gateway) parseProvider(model string) ModelProvider {
	switch {
	case strings.HasPrefix(model, "text-embedding-v3"), strings.HasPrefix(model, "qwen-"):
		return ProviderQwen
	case strings.HasPrefix(model, "embedding-3"), strings.HasPrefix(model, "glm-"):
		return ProviderZhipu
	default:
		return g.defaultProvider
	}
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
