// Package port defines outbound interfaces consumed by the agent
// application layer. Concrete implementations live in infrastructure.
package port

import (
	"context"
	"fmt"
	"time"
)

// CapabilityGateway is the unified capability routing facade consumed by
// the agent application layer. Implementations live in
// internal/agent/infrastructure/capability.
type CapabilityGateway interface {
	Route(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error)
}

// Adapter is the per-capability routing seam (LLM / Skill). Held by the
// application layer; concrete adapters live in infrastructure.
type Adapter interface {
	Route(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error)
}

type CapabilityType string

const (
	CapLLM   CapabilityType = "llm"
	CapSkill CapabilityType = "skill"
)

type CapabilityRequest struct {
	TraceID     string
	TenantID    string
	Type        CapabilityType
	LLM         *LLMCapRequest
	Skill       *SkillCapRequest
	Timeout     time.Duration
	LLMAPIKeys  map[string]string
	TokenStream func(string)
}

func (r CapabilityRequest) Validate() error {
	switch r.Type {
	case CapLLM:
		if r.LLM == nil {
			return fmt.Errorf("capability: LLM request required for type %q", CapLLM)
		}
	case CapSkill:
		if r.Skill == nil {
			return fmt.Errorf("capability: Skill request required for type %q", CapSkill)
		}
	default:
		return fmt.Errorf("capability: unknown capability type %q", r.Type)
	}
	return nil
}

type LLMCapRequest struct {
	Model       string
	Messages    []LLMMessage
	Tools       []ToolDefinition
	Temperature float32
	MaxTokens   int
}

type SkillCapRequest struct {
	SkillID string
	Input   any
}

type CapabilityResponse struct {
	TraceID   string
	Type      CapabilityType
	Duration  time.Duration
	Content   string
	ToolCalls []ToolCall
	Usage     TokenUsage
	Output    any
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type LLMMessage struct {
	Role       string
	Content    string
	ToolCallID string
	ToolCalls  []ToolCall
}

type TokenUsage struct {
	Prompt     int
	Completion int
	Total      int
}
