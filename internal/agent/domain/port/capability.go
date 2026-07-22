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
	CapLLM CapabilityType = "llm"
)

type CapabilityRequest struct {
	TraceID     string
	TenantID    string
	Type        CapabilityType
	LLM         *LLMCapRequest
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
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	ProviderType string         `json:"-"`
	ProviderID   string         `json:"-"`
	ServerID     string         `json:"-"`
	CapabilityID string         `json:"-"`
	NodeID       string         `json:"-"`
	NodeType     string         `json:"-"`
	Metadata     map[string]any `json:"-"`
}

type SkillRevisionRef struct {
	SkillID    string
	RevisionID string
}

// SkillActivation is an immutable instruction-bundle snapshot resolved for a
// single Agent run. It contains no executable implementation.
type SkillActivation struct {
	SkillID               string
	RevisionID            string
	Name                  string
	Description           string
	Instructions          string
	MCPToolIDs            []string
	KnowledgeWorkspaceIDs []string
	MemoryScopes          []string
	ExperimentID          string
	Variant               string
}

type SkillRevisionAssignment struct {
	RevisionID   string
	ExperimentID string
	Variant      string
}

type SkillActivationResolver interface {
	ResolveSkills(
		ctx context.Context,
		tenantID string,
		refs []SkillRevisionRef,
	) (map[string]SkillActivation, error)
}

type SkillRevisionResolver interface {
	ResolveSkillRevision(
		ctx context.Context,
		tenantID, skillID, subjectID string,
	) (assignment SkillRevisionAssignment, found bool, err error)
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
