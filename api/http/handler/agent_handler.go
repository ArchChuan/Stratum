package handler

import (
	"context"
	"encoding/json"
	"fmt"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"go.uber.org/zap"
)

const previewMaxChars = 50

type AgentHandler struct {
	agentRegistry  *agent.Registry
	logger         *zap.Logger
	gateway        *llmgateway.Gateway
	metrics        observability.MetricsProvider
	executionStore agent.ExecutionStore
	db             PgxPool
	aesKey         [32]byte
	gatewayCache   *llmgateway.TenantGatewayCache
	ragService     *knowledge.RAGService
	mcpRegistry    *mcp.MCPSkillRegistry
	skillAdapter   port.Adapter
	chatStore      agent.ChatStore
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
	gateway *llmgateway.Gateway,
	metrics observability.MetricsProvider,
	execStore agent.ExecutionStore,
	db PgxPool,
	aesKey [32]byte,
	gatewayCache *llmgateway.TenantGatewayCache,
	ragService *knowledge.RAGService,
	mcpRegistry *mcp.MCPSkillRegistry,
	skillAdapter port.Adapter,
	chatStore agent.ChatStore,
) *AgentHandler {
	return &AgentHandler{
		agentRegistry:  agentRegistry,
		logger:         logger,
		gateway:        gateway,
		metrics:        metrics,
		executionStore: execStore,
		db:             db,
		aesKey:         aesKey,
		gatewayCache:   gatewayCache,
		ragService:     ragService,
		mcpRegistry:    mcpRegistry,
		skillAdapter:   skillAdapter,
		chatStore:      chatStore,
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

// resolveTenantGateway looks up per-tenant LLM API keys from DB settings,
// decrypts them, builds a Gateway, and caches with 5-minute TTL.
// Returns (nil, nil) when no keys are configured or on any error (fallback to global gateway).
func (h *AgentHandler) resolveTenantGateway(ctx context.Context, tenantID string) (*llmgateway.Gateway, map[string]string) {
	if h.db == nil || h.gatewayCache == nil {
		return nil, nil
	}
	if gw, keys, ok := h.gatewayCache.Get(tenantID); ok {
		return gw, keys
	}

	var settingsJSON []byte
	err := h.db.QueryRow(ctx,
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
		tenantID,
	).Scan(&settingsJSON)
	if err != nil {
		h.logger.Warn("resolveTenantGateway: settings query failed",
			zap.String("tenant_id", tenantID), zap.Error(err))
		return nil, nil
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &settings); err != nil {
		return nil, nil
	}

	apiKeysRaw, ok := settings["llm_api_keys"].(map[string]interface{})
	if !ok || len(apiKeysRaw) == 0 {
		return nil, nil
	}

	decrypted := make(map[string]string, len(apiKeysRaw))
	for provider, enc := range apiKeysRaw {
		encStr, ok := enc.(string)
		if !ok || encStr == "" {
			continue
		}
		plain, err := pkgcrypto.Decrypt(h.aesKey, encStr)
		if err != nil {
			h.logger.Warn("resolveTenantGateway: decrypt failed",
				zap.String("tenant_id", tenantID), zap.String("provider", provider))
			continue
		}
		decrypted[provider] = plain
	}

	if len(decrypted) == 0 {
		return nil, nil
	}

	gw := llmgateway.NewGateway().WithLogger(h.logger)
	if qwenKey, ok := decrypted["qwen"]; ok {
		qwenClient := llmgateway.NewQwenClient(qwenKey, h.logger)
		gw.RegisterClient(llmgateway.ProviderQwen, qwenClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderQwen, qwenClient)
	}
	if zhipuKey, ok := decrypted["zhipu"]; ok {
		zhipuClient := llmgateway.NewZhipuClient(zhipuKey, h.logger)
		gw.RegisterClient(llmgateway.ProviderZhipu, zhipuClient)
		gw.RegisterEmbeddingClient(llmgateway.ProviderZhipu, zhipuClient)
	}

	// set default to first available provider
	for _, pref := range []llmgateway.ModelProvider{llmgateway.ProviderQwen, llmgateway.ProviderZhipu} {
		if _, ok := decrypted[string(pref)]; ok {
			gw.SetDefault(pref)
			break
		}
	}

	h.gatewayCache.Set(tenantID, gw, decrypted, constants.GatewayCacheTTL)
	return gw, decrypted
}

// buildExtraTools converts MCPServerIDs and AllowedSkills into ToolDefinitions for the ReAct loop.
func (h *AgentHandler) buildExtraTools(ctx context.Context, mcpServerIDs, allowedSkills []string) []port.ToolDefinition {
	var tools []port.ToolDefinition

	for _, serverID := range mcpServerIDs {
		if h.mcpRegistry == nil {
			continue
		}
		adapter := h.mcpRegistry.GetAdapterForServer(serverID)
		if adapter == nil {
			continue
		}
		for _, w := range adapter.GetAllSkills() {
			tools = append(tools, port.ToolDefinition{
				Name:        w.GetID(),
				Description: w.Tool.Description,
				InputSchema: w.Tool.InputSchema,
			})
		}
	}

	for _, skillID := range allowedSkills {
		name := skillID
		description := skillID
		if h.db != nil {
			if tc, ok := tenantdb.FromContext(ctx); ok && tc.TenantID != "" {
				schema := `"tenant_` + tc.TenantID + `"`
				_ = h.db.QueryRow(ctx,
					fmt.Sprintf(`SELECT name, description FROM %s.skills WHERE id=$1`, schema),
					skillID,
				).Scan(&name, &description)
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
