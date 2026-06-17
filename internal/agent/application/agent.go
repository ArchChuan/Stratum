// Package application provides the core agent system.
package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
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
	EnableTools    bool
	AvailableTools []string
	Stream         bool
	TokenCallback  func(string) // called per token when streaming; implies Stream=true
	TenantID       string
	TraceID        string
	LLMAPIKeys     map[string]string
	RAGSearchFn    func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	ExtraTools     []port.ToolDefinition
	ConversationID string
	UserID         string
	HistoryWindow  int
}

// AgentConfig holds agent configuration
type AgentConfig struct {
	ID                             string
	Name                           string
	Type                           AgentType
	Description                    string
	Persona                        string
	SystemPrompt                   string
	LLMModel                       string
	EmbedModel                     string
	MaxIterations                  int
	AllowedSkills                  []string
	MCPServerIDs                   []string
	Capabilities                   []AgentCapability
	KnowledgeWorkspaceIDs          []string
	KnowledgeWorkspaceNames        []string
	KnowledgeWorkspaceDescriptions []string
	MaxContextTokens               int
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
	CapGateway     port.CapabilityGateway
	ChatStore      ChatStore
	MemoryInjector *pipeline.MemoryInjector
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

func (a *BaseAgent) SetCapGateway(gw port.CapabilityGateway) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.CapGateway = gw
}

// SetChatStore sets the chat store for conversation history persistence (void, for interface assertion).
func (a *BaseAgent) SetChatStore(cs ChatStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ChatStore = cs
}

// WithChatStore sets the chat store for conversation history persistence.
func (a *BaseAgent) WithChatStore(cs ChatStore) *BaseAgent {
	a.SetChatStore(cs)
	return a
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

// AddToMemory adds a message to the in-process memory slice.
// Long-term indexing via MemoryManager is handled asynchronously in Execute().
func (a *BaseAgent) AddToMemory(msg Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	msg.Timestamp = time.Now()
	a.Memory = append(a.Memory, msg)
	if len(a.Memory) > 100 {
		a.Memory = a.Memory[len(a.Memory)-100:]
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
	systemPrompt := buildSystemPrompt(a.Persona, a.SystemPrompt)
	llmModel := a.LLMModel
	capGW := a.CapGateway
	chatStore := a.ChatStore
	metrics := a.metrics
	workspaceNames := a.KnowledgeWorkspaceNames
	workspaceDescs := a.KnowledgeWorkspaceDescriptions
	maxContextTokens := a.MaxContextTokens
	a.mu.Unlock()

	// Inject memory context into system prompt
	var memCtx string
	if a.MemoryInjector != nil && cfg.ConversationID != "" {
		ic := pipeline.InjectionContext{
			TenantID:       cfg.TenantID,
			UserID:         cfg.UserID,
			AgentID:        agentID,
			ConversationID: cfg.ConversationID,
			Query:          input,
		}
		if mctx, err := a.MemoryInjector.BuildContext(ctx, ic); err != nil {
			a.Logger.Warn("memory injection failed", zap.Error(err))
		} else {
			memCtx = mctx
		}
	}

	a.Logger.Info("agent execution started",
		zap.String("agent_id", agentID),
		zap.String("trace_id", cfg.TraceID),
		zap.String("conversation_id", cfg.ConversationID),
		zap.String("type", string(agentType)),
		zap.String("input", input))

	result := &AgentResult{
		AgentID:  agentID,
		Input:    input,
		Metadata: map[string]interface{}{},
	}

	// Load short-term conversation history from ChatStore (single source of truth).
	var history []*ChatMessage
	if chatStore != nil && cfg.ConversationID != "" {
		if msgs, err := chatStore.ListMessages(ctx, cfg.TenantID, cfg.ConversationID, cfg.UserID); err != nil {
			a.Logger.Warn("agent: failed to load conversation history",
				zap.String("conversation_id", cfg.ConversationID),
				zap.Error(err))
		} else {
			history = msgs
		}
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
		maxTokens := maxContextTokens
		if maxTokens <= 0 {
			maxTokens = constants.DefaultAgentContextTokens
		}
		initMessages := BuildContextMessages(systemPrompt, memCtx, history, input, maxTokens, cfg.HistoryWindow)

		var availableTools []port.ToolDefinition
		if len(workspaceNames) > 0 && cfg.RAGSearchFn != nil {
			enumVals := make([]interface{}, len(workspaceNames))
			for i, n := range workspaceNames {
				enumVals[i] = n
			}
			availableTools = append(availableTools, port.ToolDefinition{
				Name: "stratum_search_knowledge",
				Description: func() string {
					var b strings.Builder
					b.WriteString("Search one or more knowledge bases for relevant information. Available workspaces:\n")
					for i, n := range workspaceNames {
						desc := ""
						if i < len(workspaceDescs) {
							desc = workspaceDescs[i]
						}
						if desc != "" {
							b.WriteString("- " + n + ": " + desc + "\n")
						} else {
							b.WriteString("- " + n + "\n")
						}
					}
					return strings.TrimRight(b.String(), "\n")
				}(),
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
		if a.MemoryInjector != nil {
			availableTools = append(availableTools, port.ToolDefinition{
				Name:        "stratum_recall_memory",
				Description: "Search long-term memory for relevant past interactions, entities, and context. Use when you need to recall information from previous conversations.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query": map[string]interface{}{
							"type":        "string",
							"description": "Search query to find relevant memories",
						},
						"scope": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"private", "personal", "shared"},
							"description": "private=this user+agent, personal=this user across agents, shared=all tenant memories",
						},
						"limit": map[string]interface{}{
							"type":        "integer",
							"description": "Max results (1-20, default 5)",
						},
					},
					"required": []string{"query"},
				},
			})
		}
		initState := agentgraph.ReActState{
			TenantID:       cfg.TenantID,
			TraceID:        cfg.TraceID,
			ConversationID: cfg.ConversationID,
			LLMAPIKeys:     cfg.LLMAPIKeys,
			Model:          llmModel,
			Messages:       initMessages,
			OnToken:        cfg.TokenCallback,
			AvailableTools: mergeTools(availableTools, cfg.ExtraTools, a.Logger),
			RAGSearchFn:    cfg.RAGSearchFn,
		}
		if a.MemoryInjector != nil {
			recallHandler := pipeline.NewRecallHandler(
				a.MemoryInjector.Pool(), a.Logger,
				a.MemoryInjector.EmbedSvc(), a.MemoryInjector.EmbedResolver(), a.MemoryInjector.VectorDB(),
			)
			initState.RecallMemoryFn = func(ctx context.Context, input map[string]any) (string, error) {
				return recallHandler.Handle(ctx, cfg.TenantID, cfg.UserID, agentID, input)
			}
		}
		execCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		execCtx = reqctx.WithTraceID(execCtx, cfg.TraceID)
		execCtx = reqctx.WithTenantID(execCtx, cfg.TenantID)
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

	// Persist user input and agent output to ChatStore (outside switch — all agent types benefit).
	if chatStore != nil && cfg.ConversationID != "" && execErr == nil {
		saveCtx := ctx
		userMsg := &ChatMessage{
			ConversationID: cfg.ConversationID,
			Role:           "user",
			Content:        input,
			UserID:         cfg.UserID,
			AgentID:        agentID,
		}
		if err := chatStore.AddMessage(saveCtx, cfg.TenantID, userMsg); err != nil {
			a.Logger.Warn("agent: failed to save user message",
				zap.String("conversation_id", cfg.ConversationID),
				zap.Error(err))
		}
		agentMsg := &ChatMessage{
			ConversationID: cfg.ConversationID,
			Role:           "agent",
			Content:        result.Output,
			UserID:         cfg.UserID,
			AgentID:        agentID,
		}
		if err := chatStore.AddMessage(saveCtx, cfg.TenantID, agentMsg); err != nil {
			a.Logger.Warn("agent: failed to save agent message",
				zap.String("conversation_id", cfg.ConversationID),
				zap.Error(err))
		}
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

func WithTraceID(id string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.TraceID = id
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
func WithExtraTools(tools []port.ToolDefinition) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.ExtraTools = tools
	}
}

// WithConversationID sets the conversation ID for multi-turn history loading.
func WithConversationID(id string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.ConversationID = id
	}
}

// WithUserID sets the user ID for conversation history access control.
func WithUserID(id string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.UserID = id
	}
}

// WithHistoryWindow sets the max number of history messages to load. n≤0 uses default (20).
func WithHistoryWindow(n int) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		if n > 0 {
			cfg.HistoryWindow = n
		}
	}
}

// ApplyOptions applies options to the execution config
func (cfg *ExecutionConfig) ApplyOptions(opts []ExecutionOption) {
	for _, opt := range opts {
		opt(cfg)
	}
}

// BuildInitMessages constructs the initial LLM message slice from a system prompt and
// chat history. History is truncated to the most recent window messages; role "agent"
// is normalized to "assistant" for LLM protocol. window ≤ 0 defaults to 20.
func BuildInitMessages(systemPrompt string, history []*ChatMessage, window int) []port.LLMMessage {
	if window <= 0 {
		window = constants.DefaultInitHistoryWindow
	}
	if len(history) > window {
		history = history[len(history)-window:]
	}
	msgs := make([]port.LLMMessage, 0, len(history)+1)
	if systemPrompt != "" {
		msgs = append(msgs, port.LLMMessage{Role: "system", Content: systemPrompt})
	}
	for _, m := range history {
		role := m.Role
		if role == "agent" {
			role = "assistant"
		}
		msgs = append(msgs, port.LLMMessage{Role: role, Content: m.Content})
	}
	return msgs
}

// mergeTools combines built-in and extra tools, dropping duplicates (by name) with a warning.
// Built-in tools take priority: if an extra tool shares a name, it is silently dropped.
func mergeTools(builtins []port.ToolDefinition, extras []port.ToolDefinition, logger *zap.Logger) []port.ToolDefinition {
	seen := make(map[string]struct{}, len(builtins)+len(extras))
	out := make([]port.ToolDefinition, 0, len(builtins)+len(extras))
	for _, t := range builtins {
		seen[t.Name] = struct{}{}
		out = append(out, t)
	}
	for _, t := range extras {
		if _, dup := seen[t.Name]; dup {
			logger.Warn("tool name collision: extra tool shadowed by built-in, skipping",
				zap.String("tool_name", t.Name))
			continue
		}
		seen[t.Name] = struct{}{}
		out = append(out, t)
	}
	return out
}

// buildSystemPrompt prepends persona to systemPrompt with a blank line separator.
// If persona is empty, systemPrompt is returned as-is.
func buildSystemPrompt(persona, systemPrompt string) string {
	if persona == "" {
		return systemPrompt
	}
	if systemPrompt == "" {
		return persona
	}
	return persona + "\n\n" + systemPrompt
}
