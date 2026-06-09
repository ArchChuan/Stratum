// Package llmgateway provides LLM gateway abstraction.
package llmgateway

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
)

type ModelProvider string

const (
	ProviderQwen  ModelProvider = "qwen"
	ProviderZhipu ModelProvider = "zhipu"
)

type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"` // "function"
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // JSON string
	} `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type CompletionRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float32   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	TopP        float32   `json:"top_p,omitempty"`
	Tools       []Tool    `json:"tools,omitempty"`
	ToolChoice  string    `json:"tool_choice,omitempty"`
}

type CompletionResponse struct {
	Content   string     `json:"content"`
	Model     string     `json:"model"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Usage     struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type LLMClient interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
	Health(ctx context.Context) error
	// Models returns the chat model names supported by this provider.
	Models() []string
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
