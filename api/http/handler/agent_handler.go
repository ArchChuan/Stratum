// Package handler — agent_handler.go.
//
// Transport-only seam for agent HTTP endpoints. Handlers bind requests,
// extract tenant/user/trace from middleware context, delegate to
// AgentService, and render DTOs. No SQL, no infrastructure imports.
package handler

import (
	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"go.uber.org/zap"
)

// AgentHandler is a transport-only façade. All business logic lives in
// agent.AgentService; this type only holds the service handle and a
// logger for transport-layer events.
type AgentHandler struct {
	svc    *agent.AgentService
	logger *zap.Logger
}

// NewAgentHandler constructs an AgentHandler. The service handle is
// produced by api/wiring; nothing else is allowed in.
func NewAgentHandler(svc *agent.AgentService, logger *zap.Logger) *AgentHandler {
	return &AgentHandler{svc: svc, logger: logger}
}

type CreateAgentRequest struct {
	Name                  string   `json:"name" binding:"required"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	Persona               string   `json:"persona"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel" binding:"required"`
	EmbedModel            string   `json:"embedModel"`
	MaxIterations         int      `json:"maxIterations" binding:"required"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPServerIDs          []string `json:"mcpServerIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
}

// UpdateAgentRequest mirrors CreateAgentRequest minus EmbedModel — the
// embedding model is immutable post-create.
type UpdateAgentRequest struct {
	Name                  string   `json:"name" binding:"required"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	Persona               string   `json:"persona"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel" binding:"required"`
	MaxIterations         int      `json:"maxIterations" binding:"required"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPServerIDs          []string `json:"mcpServerIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
}

type AgentResponse struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Type                  string   `json:"type"`
	Description           string   `json:"description"`
	Persona               string   `json:"persona"`
	SystemPrompt          string   `json:"systemPrompt"`
	LLMModel              string   `json:"llmModel"`
	EmbedModel            string   `json:"embedModel"`
	MaxIterations         int      `json:"maxIterations"`
	MaxContextTokens      int      `json:"maxContextTokens"`
	AllowedSkills         []string `json:"allowedSkills"`
	MCPServerIDs          []string `json:"mcpServerIds"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds"`
	CreatedAt             string   `json:"createdAt"`
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
		Persona:               d.Persona,
		SystemPrompt:          d.SystemPrompt,
		LLMModel:              d.LLMModel,
		EmbedModel:            d.EmbedModel,
		MaxIterations:         d.MaxIterations,
		MaxContextTokens:      d.MaxContextTokens,
		AllowedSkills:         d.AllowedSkills,
		MCPServerIDs:          d.MCPServerIDs,
		KnowledgeWorkspaceIDs: d.KnowledgeWorkspaceIDs,
		CreatedAt:             d.CreatedAt,
	}
}
