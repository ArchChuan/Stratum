package domain

import (
	"errors"
	"time"
)

// ErrUpstreamRequestFailed indicates the provider's API returned an unexpected
// HTTP status on an operational endpoint (model discovery, health check).
// The wrapping error carries diagnostic details: provider name, URL, status code.
var ErrUpstreamRequestFailed = errors.New("upstream provider request failed")

// ErrStreamTruncated indicates a streaming response ended before the
// provider's termination marker ([DONE]/finish_reason, message_stop,
// done:true) after content had already been emitted — the answer is
// incomplete and must not be treated as success.
var ErrStreamTruncated = errors.New("stream truncated before completion marker")

// ProviderKind enumerates supported LLM provider categories.
type ProviderKind string

const (
	ProviderOpenAICompat ProviderKind = "openai_compat"
	ProviderAnthropic    ProviderKind = "anthropic"
	ProviderOllama       ProviderKind = "ollama"
	// ProviderCohere 表示 rerank 能力 provider。rerank 调用走独立 HTTP 服务
	// （knowledge/infrastructure/rerank），不进 ModelRegistry 的 chat/embedding
	// 网关；kind 仅用于目录中 rerank 能力模型的 provider 关联与筛选。
	ProviderCohere ProviderKind = "cohere"
)

// Provider represents a configured LLM provider instance.
// apiKey is write-only: it is accepted on create/update but never returned.
// ExtraHeaders/DefaultSampling 同样 write-only（值不回显，避免凭据泄漏）。
type Provider struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Kind            ProviderKind      `json:"kind"`
	BaseURL         string            `json:"baseUrl"`
	APIKey          string            `json:"-"`
	DefaultModel    string            `json:"defaultModel"`
	Enabled         bool              `json:"enabled"`
	ExtraHeaders    map[string]string `json:"-"`
	DefaultSampling map[string]any    `json:"-"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}
