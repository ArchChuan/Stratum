// Package application provides the core agent system.
package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// Domain type aliases — canonical definitions live in
// internal/agent/domain. Aliases preserve source-compat for the dozens
// of call-sites still spelled `application.AgentType`, etc.
type (
	AgentType       = domain.AgentType
	AgentCapability = domain.AgentCapability
	AgentConfig     = domain.AgentConfig
	Message         = domain.Message
	Thought         = domain.Thought
	ToolCall        = domain.ToolCall
	AgentResult     = domain.AgentResult
	AgentState      = domain.AgentState
)

// ExecutionConfig holds parameters for a single agent execution. It lives
// in the application layer because it references port.ToolDefinition and
// function types that depend on cross-context ports.
type ExecutionConfig struct {
	MaxSteps       int
	Timeout        time.Duration
	Temperature    float32
	EnableTools    bool
	AvailableTools []string
	Stream         bool
	TokenCallback  func(string)
	TenantID       string
	TraceID        string
	LLMAPIKeys     map[string]string
	RAGSearchFn    func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	ExtraTools     []port.ToolDefinition
	// SkillToolIndex maps tenant-scoped tool names to skill UUIDs for execution routing.
	SkillToolIndex map[string]string
	ConversationID string
	UserID         string
	HistoryWindow  int
}

const (
	ReActAgent       = domain.ReActAgent
	CoTAgent         = domain.CoTAgent
	PlanningAgent    = domain.PlanningAgent
	ToolCallingAgent = domain.ToolCallingAgent
	RAGAgent         = domain.RAGAgent
	SwarmAgent       = domain.SwarmAgent
)

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
	Logger             *zap.Logger
	metrics            observability.MetricsProvider
	Ledger             agentgraph.TokenRecorder
	State              AgentState
	Memory             []Message
	mu                 sync.Mutex
	CapGateway         port.CapabilityGateway
	ChatStore          ChatStore
	MemoryInjector     port.MemoryInjector
	RecallMemoryFn     port.RecallMemoryFn
	GlobalSystemSuffix string
}

// NewBaseAgent creates a new base agent
func NewBaseAgent(config *AgentConfig, logger *zap.Logger) *BaseAgent {
	return &BaseAgent{
		AgentConfig: config,
		Logger:      logger,
		metrics:     observability.NoopMetrics{},
		Ledger:      agentgraph.NoopTokenRecorder{},
		State:       AgentState{},
		Memory:      []Message{},
		mu:          sync.Mutex{},
	}
}

// WithMetrics injects a MetricsProvider. Must be called before the agent is shared across goroutines.
func (a *BaseAgent) WithMetrics(m observability.MetricsProvider) *BaseAgent {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.metrics = m
	return a
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
	if a.GlobalSystemSuffix != "" {
		systemPrompt += "\n\n" + a.GlobalSystemSuffix
	}
	llmModel := a.LLMModel
	capGW := a.CapGateway
	chatStore := a.ChatStore
	metrics := a.metrics
	workspaceNames := a.KnowledgeWorkspaceNames
	workspaceDescs := a.KnowledgeWorkspaceDescriptions
	maxContextTokens := a.MaxContextTokens
	memoryScope := a.MemoryScope
	a.mu.Unlock()

	tracer := otel.Tracer("stratum/agent")
	ctx, execSpan := tracer.Start(ctx, "agent.execute",
		oteltrace.WithAttributes(
			attribute.String("agent.id", agentID),
			attribute.String("agent.type", string(agentType)),
			attribute.String("conversation.id", cfg.ConversationID),
		),
	)
	defer execSpan.End()

	// Inject memory context into system prompt
	var memCtx string
	if a.MemoryInjector != nil && cfg.ConversationID != "" {
		ic := port.InjectionContext{
			TenantID:       cfg.TenantID,
			UserID:         cfg.UserID,
			AgentID:        agentID,
			ConversationID: cfg.ConversationID,
			Query:          input,
			Scope:          memoryScope,
		}
		_, memSpan := tracer.Start(ctx, "agent.memory_inject")
		memInjectCtx, memInjectCancel := context.WithTimeout(ctx, constants.AgentMemoryInjectTimeout)
		mctx, memInjectErr := a.MemoryInjector.BuildContext(memInjectCtx, ic)
		memInjectCancel()
		memSpan.End()
		if memInjectErr != nil {
			a.Logger.Warn("memory injection failed", zap.Error(memInjectErr))
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
		_, histSpan := tracer.Start(ctx, "agent.history_load")
		histCtx, histCancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
		msgs, histErr := chatStore.ListMessages(histCtx, cfg.TenantID, cfg.ConversationID, cfg.UserID)
		histCancel()
		histSpan.End()
		if histErr != nil {
			a.Logger.Warn("agent: failed to load conversation history",
				zap.String("conversation_id", cfg.ConversationID),
				zap.Error(histErr))
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
		cg, buildErr := agentgraph.BuildReActGraph(capGW, a.Ledger, a.Logger)
		if buildErr != nil {
			execErr = fmt.Errorf("react: build graph: %w", buildErr)
			break
		}
		maxTokens := maxContextTokens
		if maxTokens <= 0 {
			maxTokens = constants.DefaultAgentContextTokens
		}
		initMessages := BuildContextMessages(systemPrompt, memCtx, history, input, maxTokens, cfg.HistoryWindow)

		availableTools := buildBuiltinTools(workspaceNames, workspaceDescs,
			len(workspaceNames) > 0 && cfg.RAGSearchFn != nil,
			a.MemoryInjector != nil)
		initState := agentgraph.ReActState{
			TenantID:       cfg.TenantID,
			TraceID:        cfg.TraceID,
			ConversationID: cfg.ConversationID,
			LLMAPIKeys:     cfg.LLMAPIKeys,
			Model:          llmModel,
			Messages:       initMessages,
			OnToken:        cfg.TokenCallback,
			AvailableTools: mergeTools(availableTools, cfg.ExtraTools, a.Logger),
			SkillToolIndex: cfg.SkillToolIndex,
			RAGSearchFn:    cfg.RAGSearchFn,
			MaxLLMSteps:    cfg.MaxSteps,
		}
		if a.RecallMemoryFn != nil {
			fn := a.RecallMemoryFn
			initState.RecallMemoryFn = func(ctx context.Context, input map[string]any) (string, error) {
				return fn(ctx, cfg.TenantID, cfg.UserID, agentID, memoryScope, input)
			}
		}
		execCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		execCtx = reqctx.WithTraceID(execCtx, cfg.TraceID)
		execCtx = reqctx.WithTenantID(execCtx, cfg.TenantID)
		defer cancel()
		_, reactSpan := tracer.Start(execCtx, "react.graph.invoke",
			oteltrace.WithAttributes(attribute.Int("max_steps", cfg.MaxSteps)),
		)
		finalState, runErr := cg.Invoke(execCtx, initState, agentgraph.RunConfig{MaxSteps: cfg.MaxSteps})
		reactSpan.End()
		if runErr != nil {
			execErr = fmt.Errorf("react: %w", runErr)
			break
		}
		result.Output = finalState.Output
		result.Steps = finalState.Steps
		result.TokensUsed = finalState.TotalTokens
		result.CostUSD = finalState.TotalCostUSD
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
		userMsg := &ChatMessage{
			ConversationID: cfg.ConversationID,
			Role:           "user",
			Content:        input,
			UserID:         cfg.UserID,
			AgentID:        agentID,
			MemoryScope:    memoryScope,
			SkipOutbox:     false,
		}
		_, saveUserSpan := tracer.Start(ctx, "agent.chat_store.save_user")
		saveCtx1, saveCancel1 := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
		addUserErr := chatStore.AddMessage(saveCtx1, cfg.TenantID, userMsg)
		saveCancel1()
		saveUserSpan.End()
		if addUserErr != nil {
			a.Logger.Warn("agent: failed to save user message",
				zap.String("conversation_id", cfg.ConversationID),
				zap.Error(addUserErr))
		}
		agentMsg := &ChatMessage{
			ConversationID: cfg.ConversationID,
			Role:           "assistant",
			Content:        result.Output,
			UserID:         cfg.UserID,
			AgentID:        agentID,
			MemoryScope:    memoryScope,
			SkipOutbox:     false,
		}
		_, saveAgentSpan := tracer.Start(ctx, "agent.chat_store.save_assistant")
		saveCtx2, saveCancel2 := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
		addAgentErr := chatStore.AddMessage(saveCtx2, cfg.TenantID, agentMsg)
		saveCancel2()
		saveAgentSpan.End()
		if addAgentErr != nil {
			a.Logger.Warn("agent: failed to save agent message",
				zap.String("conversation_id", cfg.ConversationID),
				zap.Error(addAgentErr))
		}
	}

	result.Duration = time.Since(startTime)
	a.mu.Lock()
	result.Steps = a.State.StepsTaken
	a.mu.Unlock()

	status := domain.ExecStatusSuccess
	if execErr != nil {
		status = domain.ExecStatusError
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

// WithSkillToolIndex sets the mapping from tenant-scoped tool names to skill UUIDs.
func WithSkillToolIndex(index map[string]string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.SkillToolIndex = index
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
// chat history. History is truncated to the most recent window messages.
// window ≤ 0 defaults to 20.
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
		msgs = append(msgs, port.LLMMessage{Role: m.Role, Content: m.Content})
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

// buildBuiltinTools constructs the agent's built-in tool definitions (knowledge search, memory recall).
func buildBuiltinTools(workspaceNames, workspaceDescs []string, hasRAG, hasMemory bool) []port.ToolDefinition {
	var tools []port.ToolDefinition
	if hasRAG {
		enumVals := make([]interface{}, len(workspaceNames))
		for i, n := range workspaceNames {
			enumVals[i] = n
		}
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
		tools = append(tools, port.ToolDefinition{
			Name:        "stratum_search_knowledge",
			Description: strings.TrimRight(b.String(), "\n"),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspaces": map[string]interface{}{
						"type":        "array",
						"description": "Knowledge workspaces to search (one or more)",
						"items":       map[string]interface{}{"type": "string", "enum": enumVals},
						"minItems":    1,
					},
					"query": map[string]interface{}{"type": "string", "description": "Search query"},
					"top_k": map[string]interface{}{"type": "integer", "description": "Number of results per workspace (1-20, default 5)"},
				},
				"required": []string{"workspaces", "query"},
			},
		})
	}
	if hasMemory {
		tools = append(tools, port.ToolDefinition{
			Name:        "stratum_recall_memory",
			Description: "Search long-term memory for relevant past interactions, entities, and context. Use when you need to recall information from previous conversations.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "Search query to find relevant memories"},
					"limit": map[string]interface{}{"type": "integer", "description": "Max results (1-20, default 5)"},
				},
				"required": []string{"query"},
			},
		})
	}
	tools = append(tools, port.ToolDefinition{
		Name:        "stratum_continue_reasoning",
		Description: "Request another reasoning turn to continue chain-of-thought before calling other tools or producing a final answer. Use when you need more reasoning steps.",
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	})
	return tools
}
