// Package handler — agent_dto.go.
//
// Wire DTOs for agent HTTP endpoints. Field shapes are frozen by
// api/http/contract_test.go + testdata/contracts/*.golden.json.
package handler

import (
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

type CreateAgentRequest struct {
	Name                  string   `json:"name" binding:"required"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel" binding:"required"`
	MaxIterations         int      `json:"maxIterations" binding:"required"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	Temperature           float32  `json:"temperature"`
	ReasoningEffort       string   `json:"reasoning_effort"`
	MaxTokens             int      `json:"max_tokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPToolIDs            []string `json:"mcpToolIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	MemoryScope           string   `json:"memoryScope"`
	// Parameters carries registry resource-scope values as a flat object; only
	// the memory.* dotted keys persist on the agent (sampling keys stay on the
	// explicit fields). Same merge semantics as UpdateAgentRequest.Parameters.
	Parameters map[string]any `json:"parameters"`
	Editors    []string       `json:"editors"`
}

// embedding model is immutable post-create.
type UpdateAgentRequest struct {
	Name                  string   `json:"name" binding:"required"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel" binding:"required"`
	MaxIterations         int      `json:"maxIterations"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	Temperature           float32  `json:"temperature"`
	ReasoningEffort       string   `json:"reasoning_effort"`
	MaxTokens             int      `json:"max_tokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPToolIDs            []string `json:"mcpToolIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	MemoryScope           string   `json:"memoryScope"`
	// Parameters carries the registry sampling parameters as a flat object
	// (temperature/max_tokens/reasoning_effort；压缩配置已迁平台参数)。
	// 压缩三值（提示词/温度/模型）为平台级参数，不在 agent 上暴露/保存。
	// Merge semantics: only keys present in this map are written; a 0 value is
	// unset and never overwrites a persisted value.
	Parameters map[string]any `json:"parameters"`
}

type AgentResponse struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel"`
	MaxIterations         int      `json:"maxIterations"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	Temperature           float32  `json:"temperature"`
	ReasoningEffort       string   `json:"reasoning_effort"`
	MaxTokens             int      `json:"max_tokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPToolIDs            []string `json:"mcpToolIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	CreatedAt             string   `json:"createdAt"`
	MemoryScope           string   `json:"memoryScope"`
	IsSystem              bool     `json:"isSystem"`
	ManagementMode        string   `json:"managementMode"`
	// Parameters echoes the persisted sampling parameters (0=unset keys
	// omitted), symmetric with UpdateAgentRequest.parameters.
	Parameters map[string]any `json:"parameters"`
	// Editors is the current granted editor set, for form prefill.
	Editors []string `json:"editors"`
}

type ExecuteAgentRequest struct {
	Query          string                 `json:"query"`
	ConversationID string                 `json:"conversation_id"`
	ExecutionID    string                 `json:"execution_id"`
	UserID         string                 `json:"user_id"`
	Context        map[string]interface{} `json:"context"`
	Options        map[string]interface{} `json:"options"`
}

type AgentExecutionResult struct {
	AgentID    string                     `json:"agentId"`
	Input      string                     `json:"input"`
	Output     string                     `json:"output"`
	Steps      int                        `json:"steps"`
	TokensUsed int                        `json:"tokensUsed"`
	Duration   string                     `json:"duration"`
	Thoughts   []agent.Thought            `json:"thoughts"`
	ToolCalls  []agent.ToolCall           `json:"toolCalls"`
	Metadata   map[string]interface{}     `json:"metadata"`
	Error      string                     `json:"error,omitempty"`
	Artifacts  []domain.ExecutionArtifact `json:"artifacts"`
}

// dtoToResponse maps the service-side AgentDTO to the wire AgentResponse.
func dtoToResponse(d agent.AgentDTO) AgentResponse {
	return AgentResponse{
		ID:                    d.ID,
		Name:                  d.Name,
		Type:                  d.Type,
		Description:           d.Description,
		SystemPrompt:          d.SystemPrompt,
		LLMModel:              d.LLMModel,
		MaxIterations:         d.MaxIterations,
		MaxContextTokens:      d.MaxContextTokens,
		Temperature:           d.Temperature,
		ReasoningEffort:       d.ReasoningEffort,
		MaxTokens:             d.MaxTokens,
		AllowedSkills:         d.AllowedSkills,
		MCPToolIDs:            d.MCPToolIDs,
		KnowledgeWorkspaceIDs: d.KnowledgeWorkspaceIDs,
		CreatedAt:             d.CreatedAt,
		MemoryScope:           d.MemoryScope,
		IsSystem:              d.IsSystem,
		ManagementMode:        d.ManagementMode,
		Parameters:            d.Parameters,
		Editors:               d.Editors,
	}
}
