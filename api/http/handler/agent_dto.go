// Package handler — agent_dto.go.
//
// Wire DTOs for agent HTTP endpoints. Field shapes are frozen by
// api/http/contract_test.go + testdata/contracts/*.golden.json.
package handler

import agent "github.com/byteBuilderX/stratum/internal/agent/application"

type CreateAgentRequest struct {
	Name                  string   `json:"name" binding:"required"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel" binding:"required"`
	EmbedModel            string   `json:"embedModel"`
	MaxIterations         int      `json:"maxIterations" binding:"required"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPToolIDs            []string `json:"mcpToolIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	MemoryScope           string   `json:"memoryScope"`
}

// UpdateAgentRequest mirrors CreateAgentRequest minus EmbedModel — the
// embedding model is immutable post-create.
type UpdateAgentRequest struct {
	Name                  string   `json:"name" binding:"required"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel" binding:"required"`
	MaxIterations         int      `json:"maxIterations"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPToolIDs            []string `json:"mcpToolIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	MemoryScope           string   `json:"memoryScope"`
}

type AgentResponse struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel"`
	EmbedModel            string   `json:"embedModel"`
	MaxIterations         int      `json:"maxIterations"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPToolIDs            []string `json:"mcpToolIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	CreatedAt             string   `json:"createdAt"`
	MemoryScope           string   `json:"memoryScope"`
}

type ExecuteAgentRequest struct {
	Query          string                 `json:"query"`
	ConversationID string                 `json:"conversation_id"`
	UserID         string                 `json:"user_id"`
	Context        map[string]interface{} `json:"context"`
	Options        map[string]interface{} `json:"options"`
}

type AgentExecutionResult struct {
	AgentID    string                 `json:"agentId"`
	Input      string                 `json:"input"`
	Output     string                 `json:"output"`
	Steps      int                    `json:"steps"`
	TokensUsed int                    `json:"tokensUsed"`
	Duration   string                 `json:"duration"`
	Thoughts   []agent.Thought        `json:"thoughts"`
	ToolCalls  []agent.ToolCall       `json:"toolCalls"`
	Metadata   map[string]interface{} `json:"metadata"`
	Error      string                 `json:"error,omitempty"`
}

// dtoToResponse maps the service-side AgentDTO to the wire AgentResponse.
// Field-for-field copy — no transformation logic here.
func dtoToResponse(d agent.AgentDTO) AgentResponse {
	return AgentResponse{
		ID:                    d.ID,
		Name:                  d.Name,
		Type:                  d.Type,
		Description:           d.Description,
		SystemPrompt:          d.SystemPrompt,
		LLMModel:              d.LLMModel,
		EmbedModel:            d.EmbedModel,
		MaxIterations:         d.MaxIterations,
		MaxContextTokens:      d.MaxContextTokens,
		AllowedSkills:         d.AllowedSkills,
		MCPToolIDs:            d.MCPToolIDs,
		KnowledgeWorkspaceIDs: d.KnowledgeWorkspaceIDs,
		CreatedAt:             d.CreatedAt,
		MemoryScope:           d.MemoryScope,
	}
}
