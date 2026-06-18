package handler

import (
	"context"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"go.uber.org/zap"
)

const previewMaxChars = 50

type AgentHandler struct {
	agentRegistry   *agent.Registry
	logger          *zap.Logger
	metrics         observability.MetricsProvider
	executionStore  agent.ExecutionStore
	skillLookup     port.SkillLookup
	tenantSettings  port.TenantSettings
	tenantResolver  port.TenantCapabilityResolver
	ragService      *knowledge.RAGService
	mcpToolProvider port.MCPToolProvider
	chatStore       agent.ChatStore
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

// UpdateAgentRequest is identical to CreateAgentRequest but omits EmbedModel —
// the embedding model is immutable after creation.
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

func NewAgentHandler(
	agentRegistry *agent.Registry,
	logger *zap.Logger,
	metrics observability.MetricsProvider,
	execStore agent.ExecutionStore,
	skillLookup port.SkillLookup,
	tenantSettings port.TenantSettings,
	tenantResolver port.TenantCapabilityResolver,
	ragService *knowledge.RAGService,
	mcpToolProvider port.MCPToolProvider,
	chatStore agent.ChatStore,
) *AgentHandler {
	return &AgentHandler{
		agentRegistry:   agentRegistry,
		logger:          logger,
		metrics:         metrics,
		executionStore:  execStore,
		skillLookup:     skillLookup,
		tenantSettings:  tenantSettings,
		tenantResolver:  tenantResolver,
		ragService:      ragService,
		mcpToolProvider: mcpToolProvider,
		chatStore:       chatStore,
	}
}

func parseAgentType(t string) agent.AgentType {
	switch t {
	case "react":
		return agent.ReActAgent
	case "cot":
		return agent.CoTAgent
	case "planning":
		return agent.PlanningAgent
	case "tool_calling":
		return agent.ToolCallingAgent
	case "rag":
		return agent.RAGAgent
	case "swarm":
		return agent.SwarmAgent
	default:
		return agent.ReActAgent
	}
}

// buildExtraTools converts MCPServerIDs and AllowedSkills into ToolDefinitions for the ReAct loop.
func (h *AgentHandler) buildExtraTools(ctx context.Context, mcpServerIDs, allowedSkills []string) []port.ToolDefinition {
	var tools []port.ToolDefinition

	for _, serverID := range mcpServerIDs {
		if h.mcpToolProvider == nil {
			continue
		}
		tools = append(tools, h.mcpToolProvider.ToolsForServer(ctx, serverID)...)
	}

	for _, skillID := range allowedSkills {
		name := skillID
		description := skillID
		if h.skillLookup != nil {
			if tc, ok := tenantdb.FromContext(ctx); ok && tc.TenantID != "" {
				if n, d, err := h.skillLookup.LookupSkill(ctx, tc.TenantID, skillID); err == nil && n != "" {
					name = n
					description = d
				}
			}
		}
		tools = append(tools, port.ToolDefinition{
			Name:        skillID,
			Description: name + ": " + description,
			InputSchema: map[string]interface{}{"type": "object"},
		})
	}

	return tools
}
