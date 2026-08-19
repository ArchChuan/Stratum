package domain

import (
	"errors"
	"fmt"
	"time"
)

// ModelCapability enumerates capabilities that a model may support.
type ModelCapability string

const (
	CapChat      ModelCapability = "chat"
	CapEmbedding ModelCapability = "embedding"
	CapRerank    ModelCapability = "rerank"
	CapVision    ModelCapability = "vision"
	CapToolUse   ModelCapability = "tool_use"
	CapReasoning ModelCapability = "reasoning"
)

// ErrModelNotEmbeddingEnabled indicates the target model is disabled or lacks
// the embedding capability, so it cannot be promoted to the tenant default
// embedding model. It is a client-input mistake and must map to 4xx, never 5xx.
var ErrModelNotEmbeddingEnabled = errors.New("model is not an enabled embedding model")

// ErrModelNotFound indicates the target model does not exist for the tenant
// (or belongs to another tenant). It is a client-input mistake and must map
// to 4xx (404), never 5xx.
var ErrModelNotFound = errors.New("model not found")

// SamplingParams 是模型级默认采样参数；nil 表示未配置（回退 provider 层）。
// 0=unset 语义与 agent 侧一致：请求未显式设置时注入。
type SamplingParams struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	Seed             *int64   `json:"seed,omitempty"`
}

// CapabilitySource describes where the observed model capability came from.
type CapabilitySource string

const (
	CapabilitySourceProviderAPI     CapabilitySource = "provider_api"
	CapabilitySourceCatalog         CapabilitySource = "catalog"
	CapabilitySourceAdapterEstimate CapabilitySource = "adapter_estimate"
	CapabilitySourceManualUnknown   CapabilitySource = "manual_unknown"
	CapabilitySourceLegacyUnknown   CapabilitySource = "legacy_unknown"
)

// Model represents an LLM model that can be used for completions or embeddings.
// ContextWindow is the total context capacity (input + output), not only the
// provider's input limit. MaxTemperature is a sampling upper bound.
type Model struct {
	ID                      string            `json:"id"`
	ProviderID              string            `json:"providerId"`
	Name                    string            `json:"name"`
	DisplayName             string            `json:"displayName"`
	Capabilities            []ModelCapability `json:"capabilities"`
	ContextWindow           int               `json:"contextWindow"`
	MaxTokens               int               `json:"maxTokens"`
	OperatorContextWindow   *int              `json:"operatorContextWindow,omitempty"`
	OperatorMaxTokens       *int              `json:"operatorMaxTokens,omitempty"`
	DefaultOutputTokens     *int              `json:"defaultOutputTokens,omitempty"`
	ContextWindowSource     CapabilitySource  `json:"contextWindowSource"`
	MaxTokensSource         CapabilitySource  `json:"maxTokensSource"`
	ContextWindowObservedAt *time.Time        `json:"contextWindowObservedAt,omitempty"`
	MaxTokensObservedAt     *time.Time        `json:"maxTokensObservedAt,omitempty"`
	InputPrice              float64           `json:"inputPrice"`
	OutputPrice             float64           `json:"outputPrice"`
	Recommended             bool              `json:"recommended"`
	DefaultEmbedding        bool              `json:"defaultEmbedding"`
	Enabled                 bool              `json:"enabled"`
	ProviderManaged         bool              `json:"providerManaged"`
	SamplingParams          *SamplingParams   `json:"samplingParams,omitempty"`
	MaxTemperature          *float64          `json:"maxTemperature,omitempty"`
	CreatedAt               time.Time         `json:"createdAt"`
	UpdatedAt               time.Time         `json:"updatedAt"`
}

// EffectiveModelPolicy is the immutable policy projection consumed by runtime
// callers. ContextWindow is total context capacity and MaxOutputTokens is the
// hard output cap after applying an optional operator ceiling.
type EffectiveModelPolicy struct {
	ContextWindow       int
	MaxOutputTokens     int
	DefaultOutputTokens int
	ContextSource       CapabilitySource
	MaxOutputSource     CapabilitySource
}

// EffectivePolicy resolves observed capability and optional operator limits
// without mutating the catalog record. Operator limits can only tighten a
// known observed limit; when the observation is unknown, they become an
// explicitly manual bound.
func (m Model) EffectivePolicy() EffectiveModelPolicy {
	contextWindow, contextSource := effectiveLimit(m.ContextWindow, m.ContextWindowSource, m.OperatorContextWindow)
	maxOutput, maxSource := effectiveLimit(m.MaxTokens, m.MaxTokensSource, m.OperatorMaxTokens)
	defaultOutput := 0
	if m.DefaultOutputTokens != nil {
		defaultOutput = *m.DefaultOutputTokens
	}
	return EffectiveModelPolicy{
		ContextWindow:       contextWindow,
		MaxOutputTokens:     maxOutput,
		DefaultOutputTokens: defaultOutput,
		ContextSource:       contextSource,
		MaxOutputSource:     maxSource,
	}
}

func effectiveLimit(observed int, source CapabilitySource, operator *int) (int, CapabilitySource) {
	if operator == nil {
		return observed, source
	}
	if observed <= 0 {
		return *operator, CapabilitySourceManualUnknown
	}
	if *operator < observed {
		return *operator, source
	}
	return observed, source
}

// ValidateOperatorPolicy validates a final merged policy. Callers must merge
// a PATCH with the persisted model before calling it.
func ValidateOperatorPolicy(m Model) error {
	for _, v := range []*int{m.OperatorContextWindow, m.OperatorMaxTokens, m.DefaultOutputTokens} {
		if v != nil && *v <= 0 {
			return errors.New("operator policy values must be positive")
		}
	}
	policy := m.EffectivePolicy()
	if policy.DefaultOutputTokens > 0 && policy.MaxOutputTokens > 0 &&
		policy.DefaultOutputTokens > policy.MaxOutputTokens {
		return errors.New("default output tokens exceeds effective max output")
	}
	return nil
}

// validateInUnitRange 校验采样键值域；与网关 L3 拦截共用同一套边界。
func validateInUnitRange(name string, v *float64) error {
	if v == nil {
		return nil
	}
	if *v < 0 || *v > 1 {
		return fmt.Errorf("%s %v out of range [0,1]", name, *v)
	}
	return nil
}

// ValidateMaxTemperature 校验采样上限：nil 或 [0,1]。
func ValidateMaxTemperature(v *float64) error {
	if v == nil {
		return nil
	}
	if *v < 0 || *v > 1 {
		return fmt.Errorf("max_temperature %v out of range [0,1]", *v)
	}
	return nil
}

// ValidateSamplingWrite 校验模型级采样参数写入门禁（防注入值被运行时 L3
// 拒绝）：temperature/top_p/frequency_penalty/presence_penalty ∈ [0,1]；
// temperature ≤ max_temperature（max_temperature>0 时）；max_temperature=0
// 时禁止 temperature（不支持）。
func ValidateSamplingWrite(p *SamplingParams, maxTemp *float64) error {
	if p == nil {
		return ValidateMaxTemperature(maxTemp)
	}
	if err := ValidateMaxTemperature(maxTemp); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value *float64
	}{
		{"temperature", p.Temperature},
		{"top_p", p.TopP},
		{"frequency_penalty", p.FrequencyPenalty},
		{"presence_penalty", p.PresencePenalty},
	} {
		if err := validateInUnitRange(field.name, field.value); err != nil {
			return err
		}
	}
	if maxTemp == nil || p.Temperature == nil {
		return nil
	}
	if *maxTemp == 0 {
		return fmt.Errorf("temperature not supported (max_temperature=0)")
	}
	if *p.Temperature > *maxTemp {
		return fmt.Errorf("temperature %v exceeds max_temperature %v", *p.Temperature, *maxTemp)
	}
	return nil
}
