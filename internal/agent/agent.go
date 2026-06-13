// Package agent provides the core agent system.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	agentgraph "github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/graph"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/memory"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/observability"
	"go.uber.org/zap"
)

// AgentType defines different agent architectures
type AgentType string

const (
	ReActAgent       AgentType = "react"
	CoTAgent         AgentType = "cot"
	PlanningAgent    AgentType = "planning"
	ToolCallingAgent AgentType = "tool_calling"
	RAGAgent         AgentType = "rag"
	SwarmAgent       AgentType = "swarm"
)

// AgentCapability defines what an agent can do
type AgentCapability struct {
	Name        string
	Description string
	CanUseTools bool
	CanPlan     bool
	CanReason   bool
}

// Message represents a message in agent's conversation history
type Message struct {
	Role       string
	Content    string
	Timestamp  time.Time
	Metadata   map[string]interface{}
	TokenCount int
}

// Thought represents a single reasoning step in CoT
type Thought struct {
	Step        int
	Observation string
	Thought     string
}

// ToolCall represents a structured tool invocation
type ToolCall struct {
	ToolName string
	Input    map[string]interface{}
	Output   interface{}
	Error    error
	Duration time.Duration
}

// ExecutionConfig holds configuration for agent execution
type ExecutionConfig struct {
	MaxSteps       int
	Timeout        time.Duration
	Temperature    float32
	EnableMemory   bool
	EnableTools    bool
	AvailableTools []string
	Stream         bool
	TokenCallback  func(string) // called per token when streaming; implies Stream=true
	TenantID       string
	LLMAPIKeys     map[string]string
	RAGSearchFn    func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	ExtraTools     []capgateway.ToolDefinition
	ConversationID string
	UserID         string
	HistoryWindow  int
}

// AgentConfig holds agent configuration
type AgentConfig struct {
	ID                      string
	Name                    string
	Type                    AgentType
	Description             string
	Persona                 string
	SystemPrompt            string
	LLMModel                string
	MaxIterations           int
	AllowedSkills           []string
	MCPServerIDs            []string
	Capabilities            []AgentCapability
	KnowledgeWorkspaceIDs   []string
	KnowledgeWorkspaceNames []string
}

// AgentResult represents output from an agent execution
type AgentResult struct {
	AgentID    string
	Input      string
	Output     string
	Thoughts   []Thought
	ToolCalls  []ToolCall
	Steps      int
	TokensUsed int
	Duration   time.Duration
	Error      error
	Metadata   map[string]interface{}
}

// Agent defines the interface for all agent types
type Agent interface {
	GetConfig() *AgentConfig
	Execute(ctx context.Context, input string, options ...ExecutionOption) (*AgentResult, error)
	Reset()
	GetMemory() []Message
}

// BaseAgent provides common functionality for all agent implementations
type BaseAgent struct {
	*AgentConfig
	Logger         *zap.Logger
	metrics        observability.MetricsProvider
	State          AgentState
	Memory         []Message
	mu             sync.Mutex
	MemoryManager  *memory.MemoryManager
	SessionContext *memory.SessionContext
	CapGateway     capgateway.CapabilityGateway
}

// AgentState represents the current state of an agent
type AgentState struct {
	StepsTaken int
	Thoughts   []Thought
	ToolCalls  []ToolCall
	TokensUsed int
}

// NewBaseAgent creates a new base agent
func NewBaseAgent(config *AgentConfig, logger *zap.Logger) *BaseAgent {
	return &BaseAgent{
		AgentConfig:    config,
		Logger:         logger,
		metrics:        observability.NoopMetrics{},
		State:          AgentState{},
		Memory:         []Message{},
		mu:             sync.Mutex{},
		MemoryManager:  nil,
		SessionContext: nil,
	}
}

// NewBaseAgentWithMemory creates a new base agent with memory support
func NewBaseAgentWithMemory(config *AgentConfig, logger *zap.Logger, memoryManager *memory.MemoryManager, sessionCtx *memory.SessionContext) *BaseAgent {
	return &BaseAgent{
		AgentConfig:    config,
		Logger:         logger,
		metrics:        observability.NoopMetrics{},
		State:          AgentState{},
		Memory:         []Message{},
		mu:             sync.Mutex{},
		MemoryManager:  memoryManager,
		SessionContext: sessionCtx,
	}
}

// WithMetrics injects a MetricsProvider. Must be called before the agent is shared across goroutines.
func (a *BaseAgent) WithMetrics(m observability.MetricsProvider) *BaseAgent {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.metrics = m
	return a
}

// SetMemoryManager sets the memory manager for the agent
func (a *BaseAgent) SetMemoryManager(manager *memory.MemoryManager, sessionCtx *memory.SessionContext) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.MemoryManager = manager
	a.SessionContext = sessionCtx
}

func (a *BaseAgent) SetCapGateway(gw capgateway.CapabilityGateway) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.CapGateway = gw
}

// GetConfig implements Agent interface
func (a *BaseAgent) GetConfig() *AgentConfig {
	return a.AgentConfig
}

// Reset implements Agent interface
func (a *BaseAgent) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.State = AgentState{}
	a.Memory = []Message{}
}

// GetMemory returns the agent's conversation memory
func (a *BaseAgent) GetMemory() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Memory
}

// AddToMemory adds a message to the agent's memory
func (a *BaseAgent) AddToMemory(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	msg.Timestamp = time.Now()
	a.Memory = append(a.Memory, msg)
	if len(a.Memory) > 100 {
		a.Memory = a.Memory[len(a.Memory)-100:]
	}

	// Also add to memory manager if available
	if a.MemoryManager != nil && a.SessionContext != nil {
		entry := &memory.MemoryEntry{
			Role:      msg.Role,
			Content:   msg.Content,
			TenantID:  a.SessionContext.TenantID,
			UserID:    a.SessionContext.UserID,
			SessionID: a.SessionContext.SessionID,
			AgentID:   a.SessionContext.AgentID,
			Metadata:  msg.Metadata,
		}
		ctx := context.Background()
		if err := a.MemoryManager.Add(ctx, entry); err != nil {
			a.Logger.Warn("failed to add to memory manager", zap.Error(err))
		}
	}
}

// RetrieveMemory retrieves relevant memory entries for context
func (a *BaseAgent) RetrieveMemory(ctx context.Context, query string, limit int) ([]*memory.MemorySearchResult, error) {
	if a.MemoryManager == nil || a.SessionContext == nil {
		return []*memory.MemorySearchResult{}, nil
	}

	searchReq := &memory.MemorySearchRequest{
		Query:   query,
		Context: a.SessionContext,
		Limit:   limit,
	}

	return a.MemoryManager.Search(ctx, searchReq)
}

// Execute implements the Agent interface - base implementation with ReAct pattern
func (a *BaseAgent) Execute(ctx context.Context, input string, options ...ExecutionOption) (*AgentResult, error) {
	startTime := time.Now()

	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	// Snapshot mutable fields under lock, then release before the long LLM call.
	a.mu.Lock()
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = a.MaxIterations
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 120 * time.Second
	}
	agentID := a.ID
	agentType := a.Type
	systemPrompt := a.SystemPrompt
	llmModel := a.LLMModel
	capGW := a.CapGateway
	metrics := a.metrics
	workspaceNames := a.KnowledgeWorkspaceNames
	a.mu.Unlock()

	a.Logger.Info("agent execution started",
		zap.String("agent_id", agentID),
		zap.String("type", string(agentType)),
		zap.String("input", input))

	result := &AgentResult{
		AgentID:  agentID,
		Input:    input,
		Metadata: map[string]interface{}{},
	}

	var execErr error
	switch agentType {
	case ReActAgent:
		if capGW == nil {
			execErr = fmt.Errorf("react: CapGateway not set")
			break
		}
		cg, buildErr := agentgraph.BuildReActGraph(capGW, a.Logger)
		if buildErr != nil {
			execErr = fmt.Errorf("react: build graph: %w", buildErr)
			break
		}
		initMessages := make([]capgateway.LLMMessage, 0, 2)
		if systemPrompt != "" {
			initMessages = append(initMessages, capgateway.LLMMessage{Role: "system", Content: systemPrompt})
		}
		initMessages = append(initMessages, capgateway.LLMMessage{Role: "user", Content: input})

		var availableTools []capgateway.ToolDefinition
		if len(workspaceNames) > 0 && cfg.RAGSearchFn != nil {
			enumVals := make([]interface{}, len(workspaceNames))
			for i, n := range workspaceNames {
				enumVals[i] = n
			}
			availableTools = append(availableTools, capgateway.ToolDefinition{
				Name:        "search_knowledge",
				Description: "Search one or more knowledge bases for relevant information. Pass all relevant workspaces to search simultaneously.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"workspaces": map[string]interface{}{
							"type":        "array",
							"description": "Knowledge workspaces to search (one or more)",
							"items": map[string]interface{}{
								"type": "string",
								"enum": enumVals,
							},
							"minItems": 1,
						},
						"query": map[string]interface{}{
							"type":        "string",
							"description": "Search query",
						},
						"top_k": map[string]interface{}{
							"type":        "integer",
							"description": "Number of results per workspace (1-20, default 5)",
						},
					},
					"required": []string{"workspaces", "query"},
				},
			})
		}
		initState := agentgraph.ReActState{
			TenantID:       cfg.TenantID,
			LLMAPIKeys:     cfg.LLMAPIKeys,
			Model:          llmModel,
			Messages:       initMessages,
			OnToken:        cfg.TokenCallback,
			AvailableTools: append(availableTools, cfg.ExtraTools...),
			RAGSearchFn:    cfg.RAGSearchFn,
		}
		execCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
		finalState, runErr := cg.Invoke(execCtx, initState, agentgraph.RunConfig{MaxSteps: cfg.MaxSteps})
		if runErr != nil {
			execErr = fmt.Errorf("react: %w", runErr)
			break
		}
		result.Output = finalState.Output
		result.Steps = finalState.Steps
		result.TokensUsed = finalState.TotalTokens
		a.mu.Lock()
		a.State.StepsTaken = finalState.Steps
		a.mu.Unlock()
		for _, tc := range finalState.AllToolCalls {
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ToolName: tc.Name,
				Input:    tc.Arguments,
			})
		}

	case CoTAgent:
		for i := 0; i < cfg.MaxSteps; i++ {
			thought := Thought{
				Step:        i + 1,
				Observation: "Thinking about: " + input,
				Thought:     "Considering possible responses",
			}
			result.Thoughts = append(result.Thoughts, thought)
			a.mu.Lock()
			a.State.StepsTaken++
			a.mu.Unlock()

			if i >= 2 {
				result.Output = fmt.Sprintf("Response for: %s", input)
				break
			}
		}

	case PlanningAgent, ToolCallingAgent, RAGAgent, SwarmAgent:
		result.Output = fmt.Sprintf("%s agent type not yet implemented", string(agentType))
		execErr = fmt.Errorf("agent type %s not implemented", agentType)

	default:
		result.Output = "Unknown agent type"
		execErr = fmt.Errorf("unknown agent type: %s", agentType)
	}

	result.Duration = time.Since(startTime)
	a.mu.Lock()
	result.Steps = a.State.StepsTaken
	a.mu.Unlock()

	status := "success"
	if execErr != nil {
		status = "error"
	}
	metrics.IncAgentExecution(agentID, string(agentType), status)
	metrics.RecordAgentExecutionDuration(agentID, string(agentType), result.Duration.Seconds())
	metrics.RecordAgentStepCount(agentID, string(agentType), result.Steps)

	return result, execErr
}

// ExecutionOption configures agent execution behavior
type ExecutionOption func(*ExecutionConfig)

// WithMaxSteps sets the maximum number of steps
func WithMaxSteps(maxSteps int) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.MaxSteps = maxSteps
	}
}

// WithTimeout sets the execution timeout
func WithTimeout(timeout time.Duration) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.Timeout = timeout
	}
}

// WithTemperature sets the LLM temperature
func WithTemperature(temperature float32) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.Temperature = temperature
	}
}

// WithMemory enables memory usage
func WithMemory(enable bool) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.EnableMemory = enable
	}
}

// WithTools enables tool usage
func WithTools(tools []string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.AvailableTools = tools
		cfg.EnableTools = true
	}
}

// WithStream enables streaming output
func WithStream(enable bool) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.Stream = enable
	}
}

// WithTokenCallback sets a per-token callback, enabling streaming automatically.
func WithTokenCallback(cb func(string)) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.TokenCallback = cb
		cfg.Stream = true
	}
}

// WithTenantID sets the tenant ID for the execution context.
func WithTenantID(id string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.TenantID = id
	}
}

// WithLLMAPIKeys injects per-tenant decrypted LLM API keys into the execution.
func WithLLMAPIKeys(keys map[string]string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.LLMAPIKeys = keys
	}
}

// WithRAGSearchFn injects a knowledge-base search function for the search_knowledge tool.
func WithRAGSearchFn(fn func(ctx context.Context, workspaces []string, query string, topK int) (string, error)) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.RAGSearchFn = fn
	}
}

// WithExtraTools appends extra tool definitions (from MCP servers and allowed skills) to AvailableTools.
func WithExtraTools(tools []capgateway.ToolDefinition) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.ExtraTools = tools
	}
}

func WithConversationID(id string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.ConversationID = id
	}
}

func WithUserID(id string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.UserID = id
	}
}

func WithHistoryWindow(n int) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.HistoryWindow = n
	}
}

// ApplyOptions applies options to the execution config
func (cfg *ExecutionConfig) ApplyOptions(opts []ExecutionOption) {
	for _, opt := range opts {
		opt(cfg)
	}
}
