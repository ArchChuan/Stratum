// Package llmgateway provides LLM gateway abstraction.
package llmgateway

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
)

type ModelProvider string

const (
	ProviderOpenAI ModelProvider = "openai"
	ProviderClaude ModelProvider = "claude"
	ProviderGemini ModelProvider = "gemini"
	ProviderOllama ModelProvider = "ollama"
	ProviderLLaMA  ModelProvider = "llama"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	TopP        float32   `json:"top_p,omitempty"`
}

type CompletionResponse struct {
	Content string `json:"content"`
	Model   string `json:"model"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type LLMClient interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	Health(ctx context.Context) error
}

type EmbeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

type EmbeddingResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

type EmbeddingClient interface {
	CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error)
}

type Gateway struct {
	clients          map[ModelProvider]LLMClient
	embeddingClients map[ModelProvider]EmbeddingClient
	defaultProvider  ModelProvider
	metrics          observability.MetricsProvider
}

// NewGateway creates a Gateway with a no-op metrics provider.
// Call WithMetrics to inject a real provider.
func NewGateway() *Gateway {
	return &Gateway{
		clients:          make(map[ModelProvider]LLMClient),
		embeddingClients: make(map[ModelProvider]EmbeddingClient),
		metrics:          observability.NoopMetrics{},
	}
}

// WithMetrics injects a MetricsProvider into the gateway.
func (g *Gateway) WithMetrics(m observability.MetricsProvider) *Gateway {
	g.metrics = m
	return g
}

func (g *Gateway) RegisterClient(provider ModelProvider, client LLMClient) {
	g.clients[provider] = client
}

func (g *Gateway) RegisterEmbeddingClient(provider ModelProvider, client EmbeddingClient) {
	g.embeddingClients[provider] = client
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
	}

	return resp, err
}

func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
	client, ok := g.embeddingClients[g.defaultProvider]
	if !ok {
		client, ok = g.embeddingClients[ProviderOpenAI]
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

func (g *Gateway) parseProvider(model string) ModelProvider {
	switch model {
	case "gpt-4", "gpt-3.5-turbo":
		return ProviderOpenAI
	case "claude-3-opus", "claude-3-sonnet":
		return ProviderClaude
	case "gemini-pro":
		return ProviderGemini
	case "ollama":
		return ProviderOllama
	default:
		return g.defaultProvider
	}
}
