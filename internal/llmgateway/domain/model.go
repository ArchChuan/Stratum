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

// Model represents an LLM model that can be used for completions or embeddings.
// MaxTemperature 是采样上限（NULL=全局契约 [0,1]；0=不支持 temperature）。
type Model struct {
	ID               string            `json:"id"`
	ProviderID       string            `json:"providerId"`
	Name             string            `json:"name"`
	DisplayName      string            `json:"displayName"`
	Capabilities     []ModelCapability `json:"capabilities"`
	ContextWindow    int               `json:"contextWindow"`
	MaxTokens        int               `json:"maxTokens"`
	InputPrice       float64           `json:"inputPrice"`
	OutputPrice      float64           `json:"outputPrice"`
	Recommended      bool              `json:"recommended"`
	DefaultEmbedding bool              `json:"defaultEmbedding"`
	Enabled          bool              `json:"enabled"`
	ProviderManaged  bool              `json:"providerManaged"`
	SamplingParams   *SamplingParams   `json:"samplingParams,omitempty"`
	MaxTemperature   *float64          `json:"maxTemperature,omitempty"`
	CreatedAt        time.Time         `json:"createdAt"`
	UpdatedAt        time.Time         `json:"updatedAt"`
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
	if err := validateInUnitRange("temperature", p.Temperature); err != nil {
		return err
	}
	if err := validateInUnitRange("top_p", p.TopP); err != nil {
		return err
	}
	if err := validateInUnitRange("frequency_penalty", p.FrequencyPenalty); err != nil {
		return err
	}
	if err := validateInUnitRange("presence_penalty", p.PresencePenalty); err != nil {
		return err
	}
	if err := ValidateMaxTemperature(maxTemp); err != nil {
		return err
	}
	if maxTemp != nil {
		if *maxTemp == 0 && p.Temperature != nil {
			return fmt.Errorf("temperature not supported (max_temperature=0)")
		}
		if *maxTemp > 0 && p.Temperature != nil && *p.Temperature > *maxTemp {
			return fmt.Errorf("temperature %v exceeds max_temperature %v", *p.Temperature, *maxTemp)
		}
	}
	return nil
}
