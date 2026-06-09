// Package capgateway provides the unified capability routing facade.
package capgateway

import (
	"fmt"
	"time"
)

type CapabilityType string

const (
	CapLLM   CapabilityType = "llm"
	CapSkill CapabilityType = "skill"
)

type CapabilityRequest struct {
	TraceID  string
	TenantID string
	Type     CapabilityType
	LLM      *LLMCapRequest
	Skill    *SkillCapRequest
	Timeout  time.Duration
}

func (r CapabilityRequest) Validate() error {
	switch r.Type {
	case CapLLM:
		if r.LLM == nil {
			return fmt.Errorf("capgateway: LLM request required for type %q", CapLLM)
		}
	case CapSkill:
		if r.Skill == nil {
			return fmt.Errorf("capgateway: Skill request required for type %q", CapSkill)
		}
	default:
		return fmt.Errorf("capgateway: unknown capability type %q", r.Type)
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
