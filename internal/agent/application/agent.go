// Package application provides the core agent system.
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	AgentTraceEvent = domain.AgentTraceEvent
	Message         = domain.Message
	Thought         = domain.Thought
	ToolCall        = domain.ToolCall
	ToolObservation = domain.ToolObservation
	AgentResult     = domain.AgentResult
	AgentState      = domain.AgentState
)

// ExecutionConfig holds parameters for a single agent execution. It lives
// in the application layer because it references port.ToolDefinition and
// function types that depend on cross-context ports.
type ExecutionConfig struct {
	MaxSteps                  int
	Timeout                   time.Duration
	Temperature               float32
	EnableTools               bool
	AvailableTools            []string
	Stream                    bool
	TokenCallback             func(string)
	TenantID                  string
	TraceID                   string
	ExecutionID               string
	LLMAPIKeys                map[string]string
	RAGSearchFn               func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	ExtraTools                []port.ToolDefinition
	SkillCatalog              map[string]port.SkillActivation
	ToolExecutionFn           port.ToolExecutionFn
	ActiveSkill               *port.SkillActivation
	TracePayloadStore         port.TracePayloadStore
	ConversationID            string
	UserID                    string
	HistoryWindow             int
	EvolutionTrace            EvolutionTraceMetadata
	OfficialDocsSearchFn      func(context.Context, string) ([]domain.Citation, error)
	DiagnosticFn              func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)
	SystemAssistantMode       bool
	SystemAssistantRoleClass  string
	InternalToolResultGuardFn func(any) (port.GuardedToolResult, error)
}

// EvolutionTraceMetadata attributes an execution to evaluation and rollout evidence.
type EvolutionTraceMetadata struct {
	Evaluation            bool
	SecurityViolation     bool
	ExperimentID          string
	Variant               string
	ResourceManifest      map[string]string
	ExperimentAssignments map[string]ExperimentAssignment
}

// ExperimentAssignment identifies the rollout selected for one versioned resource.
type ExperimentAssignment struct {
	ExperimentID string `json:"experiment_id"`
	Variant      string `json:"variant"`
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
	CheckpointStore    CheckpointStore
	MemoryInjector     port.MemoryInjector
	HistoryCompactor   port.HistoryCompactor
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

func (a *BaseAgent) SetHistoryCompactor(compactor port.HistoryCompactor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.HistoryCompactor = compactor
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

func (a *BaseAgent) SetCheckpointStore(store CheckpointStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.CheckpointStore = store
}

func (a *BaseAgent) WithCheckpointStore(store CheckpointStore) *BaseAgent {
	a.SetCheckpointStore(store)
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
	agentName := a.Name
	// Architecture is unified: historical persisted type values are compatibility data only.
	agentType := domain.ReActAgent
	systemPrompt := a.SystemPrompt
	if a.GlobalSystemSuffix != "" {
		systemPrompt += "\n\n" + a.GlobalSystemSuffix
	}
	llmModel := a.LLMModel
	capGW := a.CapGateway
	historyCompactor := a.HistoryCompactor
	chatStore := a.ChatStore
	metrics := a.metrics
	workspaceNames := a.KnowledgeWorkspaceNames
	workspaceDescs := a.KnowledgeWorkspaceDescriptions
	maxContextTokens := a.MaxContextTokens
	memoryScope := a.MemoryScope
	a.mu.Unlock()

	tracer := otel.Tracer("stratum/agent")
	ctx, execSpan := tracer.Start(ctx, "agent.execute",
		oteltrace.WithAttributes(agentExecutionAttributes(agentID, agentName, agentType, *cfg)...),
	)
	defer execSpan.End()

	// Inject memory context into system prompt
	var memCtx string
	if !cfg.SystemAssistantMode && a.MemoryInjector != nil && cfg.ConversationID != "" {
		ic := port.InjectionContext{
			TenantID:       cfg.TenantID,
			UserID:         cfg.UserID,
			AgentID:        agentID,
			ConversationID: cfg.ConversationID,
			Query:          input,
			Scope:          memoryScope,
		}
		memSpanCtx, memSpan := tracer.Start(ctx, "agent.memory_inject")
		memInjectCtx, memInjectCancel := context.WithTimeout(memSpanCtx, constants.AgentMemoryInjectTimeout)
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
		zap.String("type", string(agentType)))

	result := &AgentResult{
		AgentID:  agentID,
		Input:    input,
		Metadata: map[string]interface{}{},
	}

	// Load short-term conversation history from ChatStore (single source of truth).
	var history []*ChatMessage
	if chatStore != nil && cfg.ConversationID != "" {
		histSpanCtx, histSpan := tracer.Start(ctx, "agent.history_load")
		histCtx, histCancel := context.WithTimeout(histSpanCtx, constants.AgentDBQueryTimeout)
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
		initMessages := BuildContextMessagesWithCompaction(
			ctx, systemPrompt, memCtx, history, input, maxTokens, cfg.HistoryWindow, historyCompactor,
		)

		availableTools := buildBuiltinTools(workspaceNames, workspaceDescs,
			len(workspaceNames) > 0 && cfg.RAGSearchFn != nil, a.MemoryInjector != nil)
		if cfg.SystemAssistantMode {
			availableTools = nil
		}
		initState := agentgraph.ReActState{
			TenantID:                   cfg.TenantID,
			TraceID:                    cfg.TraceID,
			ConversationID:             cfg.ConversationID,
			LLMAPIKeys:                 cfg.LLMAPIKeys,
			Model:                      llmModel,
			Messages:                   initMessages,
			OnToken:                    cfg.TokenCallback,
			AvailableTools:             mergeTools(availableTools, cfg.ExtraTools, a.Logger),
			SkillCatalog:               cfg.SkillCatalog,
			ActiveSkill:                cfg.ActiveSkill,
			TracePayloadStore:          cfg.TracePayloadStore,
			ToolExecutionFn:            cfg.ToolExecutionFn,
			OfficialDocsSearchFn:       cfg.OfficialDocsSearchFn,
			DiagnosticFn:               cfg.DiagnosticFn,
			GovernedAssistant:          cfg.SystemAssistantMode,
			InternalToolResultGuardFn:  cfg.InternalToolResultGuardFn,
			ExecutionID:                cfg.ExecutionID,
			AgentKnowledgeWorkspaceIDs: workspaceNames,
			AgentMemoryScope:           memoryScope,
			RAGSearchFn:                cfg.RAGSearchFn,
			MaxLLMSteps:                cfg.MaxSteps,
			MaxContextTokens:           maxTokens,
			HistoryCompactor:           historyCompactor,
			PlanCheckpointWriter:       a.CheckpointStore,
			PlanCheckpointIdentity: agentgraph.PlanCheckpointIdentity{
				ExecutionID: cfg.ExecutionID, TraceID: cfg.TraceID, ConversationID: cfg.ConversationID, AgentID: agentID, UserID: cfg.UserID,
			},
			PlanIDSource: uuid.NewString,
			PlanLimits: domain.PlanLimits{
				MaxNodes: constants.DefaultPlanMaxNodes, MaxRevisions: constants.DefaultPlanMaxRevisions,
				MaxAttemptsPerNode: constants.DefaultPlanMaxAttemptsPerNode, MaxConcurrentNodes: constants.DefaultPlanMaxConcurrentNodes,
			},
		}
		initState.PlanNodeExecutor = func(nodeCtx context.Context, parent agentgraph.ReActState, node domain.PlanNode, summaries map[string]string) (agentgraph.PlanNodeExecutionResult, error) {
			nodeGraph, graphErr := agentgraph.BuildReActGraph(capGW, a.Ledger, a.Logger)
			if graphErr != nil {
				return agentgraph.PlanNodeExecutionResult{}, graphErr
			}
			systemMessage := port.LLMMessage{Role: "system", Content: systemPrompt}
			goal := node.Goal
			if len(summaries) > 0 {
				encoded, _ := json.Marshal(summaries)
				goal += "\nDependency summaries: " + string(encoded)
			}
			child := parent
			child.Messages = []port.LLMMessage{systemMessage, {Role: "user", Content: goal}}
			child.ActivePlan = nil
			child.PlanToolsDisabled = true
			child.MaxLLMSteps = constants.DefaultStepMaxLLMSteps
			final, invokeErr := nodeGraph.Invoke(nodeCtx, child, agentgraph.RunConfig{MaxSteps: constants.DefaultStepMaxLLMSteps})
			if invokeErr != nil {
				return agentgraph.PlanNodeExecutionResult{}, invokeErr
			}
			return agentgraph.PlanNodeExecutionResult{Summary: final.Output}, nil
		}
		if !cfg.SystemAssistantMode && a.RecallMemoryFn != nil {
			fn := a.RecallMemoryFn
			initState.RecallMemoryFn = func(ctx context.Context, input map[string]any) (string, error) {
				return fn(ctx, cfg.TenantID, cfg.UserID, agentID, memoryScope, input)
			}
		}
		execCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
		execCtx = reqctx.WithTraceID(execCtx, cfg.TraceID)
		execCtx = reqctx.WithTenantID(execCtx, cfg.TenantID)
		defer cancel()
		graphCtx, reactSpan := tracer.Start(execCtx, "react.graph.invoke",
			oteltrace.WithAttributes(attribute.Int("max_steps", cfg.MaxSteps)),
		)
		finalState, runErr := cg.Invoke(graphCtx, initState, agentgraph.RunConfig{MaxSteps: cfg.MaxSteps})
		reactSpan.End()
		if runErr != nil {
			execErr = fmt.Errorf("react: %w", runErr)
			break
		}
		result.Output = finalState.Output
		result.Steps = finalState.Steps
		result.TokensUsed = finalState.TotalTokens
		result.CostUSD = finalState.TotalCostUSD
		result.ToolObservations = enrichToolObservations(finalState.ToolObservations, cfg.TraceID, cfg.ExecutionID, cfg.ConversationID, agentID, cfg.UserID)
		result.TraceEvents = enrichTraceEvents(finalState.TraceEvents, cfg.TraceID, cfg.ExecutionID, cfg.ConversationID, agentID, cfg.UserID)
		result.AssistantToolArtifacts = append([]domain.SystemAssistantToolArtifact(nil), finalState.AssistantToolArtifacts...)
		finalAnswerAt := time.Now()
		result.TraceEvents = append(result.TraceEvents, domain.AgentTraceEvent{
			TraceID:         cfg.TraceID,
			ExecutionID:     cfg.ExecutionID,
			ConversationID:  cfg.ConversationID,
			AgentID:         agentID,
			UserID:          cfg.UserID,
			RunType:         domain.RunTypeAgent,
			ObservationType: domain.ObservationTypeAgent,
			EventType:       domain.TraceEventFinalAnswer,
			StepIndex:       finalState.Steps,
			Status:          domain.ToolTraceStatusSuccess,
			Output:          map[string]any{"content": finalState.Output},
			Summary:         truncateRunes(finalState.Output, 500),
			Model:           llmModel,
			TotalTokens:     finalState.TotalTokens,
			CostUSD:         finalState.TotalCostUSD,
			ProviderType:    domain.ProviderTypeLLM,
			ProviderID:      llmModel,
			SequenceNo:      int64(len(result.TraceEvents) + 1),
			StartedAt:       finalAnswerAt,
			EndedAt:         finalAnswerAt,
		})
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

	case PlanningAgent:
		if capGW == nil {
			execErr = fmt.Errorf("planning: CapGateway not set")
			break
		}
		stuckThreshold := a.StuckThreshold
		if stuckThreshold <= 0 {
			stuckThreshold = constants.DefaultStuckThreshold
		}
		var cpWriter agentgraph.PlanCheckpointWriter
		if a.CheckpointStore != nil {
			cpWriter = a.CheckpointStore
		}
		cg, buildErr := agentgraph.BuildPlanExecuteGraph(capGW, a.Ledger, cpWriter, nil, a.Logger)
		if buildErr != nil {
			execErr = fmt.Errorf("planning: build graph: %w", buildErr)
			break
		}
		maxTokens := maxContextTokens
		if maxTokens <= 0 {
			maxTokens = constants.DefaultAgentContextTokens
		}
		initMessages := BuildContextMessagesWithCompaction(
			ctx, systemPrompt, memCtx, history, input, maxTokens, cfg.HistoryWindow, historyCompactor,
		)
		availableTools := buildBuiltinTools(workspaceNames, workspaceDescs,
			len(workspaceNames) > 0 && cfg.RAGSearchFn != nil,
			a.MemoryInjector != nil)
		initState := agentgraph.ReActState{
			TenantID:                   cfg.TenantID,
			TraceID:                    cfg.TraceID,
			ConversationID:             cfg.ConversationID,
			LLMAPIKeys:                 cfg.LLMAPIKeys,
			Model:                      llmModel,
			Messages:                   initMessages,
			OnToken:                    cfg.TokenCallback,
			AvailableTools:             mergeTools(availableTools, cfg.ExtraTools, a.Logger),
			SkillCatalog:               cfg.SkillCatalog,
			ActiveSkill:                cfg.ActiveSkill,
			TracePayloadStore:          cfg.TracePayloadStore,
			ToolExecutionFn:            cfg.ToolExecutionFn,
			ExecutionID:                cfg.ExecutionID,
			AgentKnowledgeWorkspaceIDs: workspaceNames,
			AgentMemoryScope:           memoryScope,
			RAGSearchFn:                cfg.RAGSearchFn,
			MaxLLMSteps:                cfg.MaxSteps,
			MaxContextTokens:           maxTokens,
			StuckThreshold:             stuckThreshold,
			CheckpointEnabled:          a.CheckpointEnabled,
			HistoryCompactor:           historyCompactor,
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
		graphCtx, planSpan := tracer.Start(execCtx, "planning.graph.invoke",
			oteltrace.WithAttributes(attribute.Int("stuck_threshold", stuckThreshold)),
		)
		finalState, runErr := cg.Invoke(graphCtx, initState, agentgraph.RunConfig{MaxSteps: cfg.MaxSteps})
		planSpan.End()
		if runErr != nil {
			execErr = fmt.Errorf("planning: %w", runErr)
			break
		}
		result.Output = finalState.Output
		result.Steps = finalState.Steps
		result.TokensUsed = finalState.TotalTokens
		result.CostUSD = finalState.TotalCostUSD
		result.ToolObservations = enrichToolObservations(finalState.ToolObservations, cfg.TraceID, cfg.ExecutionID, cfg.ConversationID, agentID, cfg.UserID)
		result.TraceEvents = enrichTraceEvents(finalState.TraceEvents, cfg.TraceID, cfg.ExecutionID, cfg.ConversationID, agentID, cfg.UserID)
		finalAnswerAt := time.Now()
		result.TraceEvents = append(result.TraceEvents, domain.AgentTraceEvent{
			TraceID:         cfg.TraceID,
			ExecutionID:     cfg.ExecutionID,
			ConversationID:  cfg.ConversationID,
			AgentID:         agentID,
			UserID:          cfg.UserID,
			RunType:         domain.RunTypeAgent,
			ObservationType: domain.ObservationTypeAgent,
			EventType:       domain.TraceEventFinalAnswer,
			StepIndex:       finalState.Steps,
			Status:          domain.ToolTraceStatusSuccess,
			Output:          map[string]any{"content": finalState.Output},
			Summary:         truncateRunes(finalState.Output, 500),
			Model:           llmModel,
			TotalTokens:     finalState.TotalTokens,
			CostUSD:         finalState.TotalCostUSD,
			ProviderType:    domain.ProviderTypeLLM,
			ProviderID:      llmModel,
			SequenceNo:      int64(len(result.TraceEvents) + 1),
			StartedAt:       finalAnswerAt,
			EndedAt:         finalAnswerAt,
		})
		a.mu.Lock()
		a.State.StepsTaken = finalState.Steps
		a.mu.Unlock()
		for _, tc := range finalState.AllToolCalls {
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ToolName: tc.Name,
				Input:    tc.Arguments,
			})
		}

	case ToolCallingAgent, RAGAgent, SwarmAgent:
		result.Output = fmt.Sprintf("%s agent type not yet implemented", string(agentType))
		execErr = fmt.Errorf("agent type %s not implemented", agentType)

	default:
		result.Output = "Unknown agent type"
		execErr = fmt.Errorf("unknown agent type: %s", agentType)
	}
	result.Artifacts = buildExecutionArtifacts(result.AssistantToolArtifacts, cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"])

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
			Artifacts:      result.Artifacts,
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
		if summary := buildToolObservationSummary(result.ToolObservations); summary != "" {
			summaryMsg := &ChatMessage{
				ConversationID: cfg.ConversationID,
				Role:           "assistant",
				Content:        summary,
				UserID:         cfg.UserID,
				AgentID:        agentID,
				MemoryScope:    memoryScope,
				SkipOutbox:     true,
			}
			_, saveSummarySpan := tracer.Start(ctx, "agent.chat_store.save_tool_summary")
			saveCtx3, saveCancel3 := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
			addSummaryErr := chatStore.AddMessage(saveCtx3, cfg.TenantID, summaryMsg)
			saveCancel3()
			saveSummarySpan.End()
			if addSummaryErr != nil {
				a.Logger.Warn("agent: failed to save tool summary message",
					zap.String("conversation_id", cfg.ConversationID),
					zap.Error(addSummaryErr))
			}
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
	execSpan.SetAttributes(
		attribute.String("opik.metadata.stratum.status", status),
		attribute.Int64("opik.metadata.stratum.duration_ms", result.Duration.Milliseconds()),
		attribute.Int64("opik.metadata.stratum.total_tokens", int64(result.TokensUsed)),
		attribute.Float64("opik.metadata.stratum.cost_usd", result.CostUSD),
	)
	metrics.IncAgentExecution(agentID, string(agentType), status)
	metrics.RecordAgentExecutionDuration(agentID, string(agentType), result.Duration.Seconds())
	metrics.RecordAgentStepCount(agentID, string(agentType), result.Steps)

	return result, execErr
}

func enrichToolObservations(in []domain.ToolObservation, traceID, executionID, conversationID, agentID, userID string) []domain.ToolObservation {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.ToolObservation, len(in))
	for i, obs := range in {
		out[i] = obs
		if out[i].TraceID == "" {
			out[i].TraceID = traceID
		}
		if out[i].ExecutionID == "" {
			out[i].ExecutionID = executionID
		}
		if out[i].ConversationID == "" {
			out[i].ConversationID = conversationID
		}
		out[i].AgentID = agentID
		out[i].UserID = userID
		if out[i].Status == "" {
			out[i].Status = domain.ToolTraceStatusSuccess
		}
		if out[i].ProviderType == "" {
			out[i].ProviderType = domain.ProviderTypeInternal
		}
		if out[i].ProviderID == "" {
			out[i].ProviderID = out[i].ToolName
		}
		if out[i].CapabilityID == "" {
			out[i].CapabilityID = out[i].ToolName
		}
	}
	return out
}

func enrichTraceEvents(in []domain.AgentTraceEvent, traceID, executionID, conversationID, agentID, userID string) []domain.AgentTraceEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.AgentTraceEvent, len(in))
	for i, ev := range in {
		out[i] = ev
		if out[i].TraceID == "" {
			out[i].TraceID = traceID
		}
		if out[i].ExecutionID == "" {
			out[i].ExecutionID = executionID
		}
		if out[i].ConversationID == "" {
			out[i].ConversationID = conversationID
		}
		out[i].AgentID = agentID
		out[i].UserID = userID
		if out[i].RunType == "" {
			out[i].RunType = domain.RunTypeAgent
		}
		if out[i].ObservationType == "" {
			out[i].ObservationType = domain.ObservationTypeCustom
		}
		if out[i].SequenceNo == 0 {
			out[i].SequenceNo = int64(i + 1)
		}
		if out[i].StartedAt.IsZero() && !out[i].EndedAt.IsZero() {
			out[i].StartedAt = out[i].EndedAt
		}
		if out[i].EndedAt.IsZero() && !out[i].StartedAt.IsZero() && out[i].LatencyMs > 0 {
			out[i].EndedAt = out[i].StartedAt.Add(time.Duration(out[i].LatencyMs) * time.Millisecond)
		}
	}
	return out
}

func buildToolObservationSummary(observations []domain.ToolObservation) string {
	if len(observations) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("本轮工具观察摘要：")
	for i, obs := range observations {
		if obs.Summary == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("%d. %s：%s", i+1, obs.ToolName, obs.Summary))
	}
	if b.Len() == len("本轮工具观察摘要：") {
		return ""
	}
	return truncateRunes(b.String(), 3000)
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

func WithExecutionID(id string) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.ExecutionID = id
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

// WithSkillCatalog sets immutable instruction-bundle snapshots for this run.
func WithSkillCatalog(catalog map[string]port.SkillActivation) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.SkillCatalog = catalog
	}
}

func WithActiveSkill(skill port.SkillActivation) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.ActiveSkill = &skill }
}

func WithToolExecutionFn(fn port.ToolExecutionFn) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.ToolExecutionFn = fn
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

func WithTracePayloadStore(store port.TracePayloadStore) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.TracePayloadStore = store
	}
}

// WithEvolutionTraceMetadata attaches evaluation and rollout evidence to the root Agent span.
func WithEvolutionTraceMetadata(metadata EvolutionTraceMetadata) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.EvolutionTrace = metadata
	}
}

func WithOfficialDocsSearchFn(fn func(context.Context, string) ([]domain.Citation, error)) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.OfficialDocsSearchFn = fn }
}

func WithDiagnosticFn(fn func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.DiagnosticFn = fn }
}

func WithSystemAssistantMode() ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.SystemAssistantMode = true }
}

func withSystemAssistantRoleClass(roleClass string) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.SystemAssistantRoleClass = roleClass }
}

func withInternalToolResultGuard(fn func(any) (port.GuardedToolResult, error)) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.InternalToolResultGuardFn = fn }
}

func agentExecutionAttributes(agentID, agentName string, agentType AgentType, cfg ExecutionConfig) []attribute.KeyValue {
	resourceManifest := cfg.EvolutionTrace.ResourceManifest
	if resourceManifest == nil {
		resourceManifest = map[string]string{}
	}
	experimentAssignments := cfg.EvolutionTrace.ExperimentAssignments
	if experimentAssignments == nil {
		experimentAssignments = map[string]ExperimentAssignment{}
	}
	manifest, _ := json.Marshal(resourceManifest)
	assignments, _ := json.Marshal(experimentAssignments)
	return []attribute.KeyValue{
		attribute.String("agent.id", agentID),
		attribute.String("agent.type", string(agentType)),
		attribute.String("conversation.id", cfg.ConversationID),
		attribute.String("stratum.tenant.id", cfg.TenantID),
		attribute.String("stratum.user.id", cfg.UserID),
		attribute.String("stratum.trace.id", cfg.TraceID),
		attribute.String("stratum.execution.id", cfg.ExecutionID),
		attribute.String("stratum.conversation.id", cfg.ConversationID),
		attribute.String("stratum.evaluation", fmt.Sprintf("%t", cfg.EvolutionTrace.Evaluation)),
		attribute.String("stratum.security_violation", fmt.Sprintf("%t", cfg.EvolutionTrace.SecurityViolation)),
		attribute.String("stratum.experiment.id", cfg.EvolutionTrace.ExperimentID),
		attribute.String("stratum.experiment.variant", cfg.EvolutionTrace.Variant),
		attribute.String("stratum.experiment.assignments", string(assignments)),
		attribute.String("stratum.resource.manifest", string(manifest)),
		attribute.String("opik.metadata.stratum.tenant_id", cfg.TenantID),
		attribute.String("opik.metadata.stratum.user_id", cfg.UserID),
		attribute.String("opik.metadata.stratum.trace_id", cfg.TraceID),
		attribute.String("opik.metadata.stratum.execution_id", cfg.ExecutionID),
		attribute.String("opik.metadata.stratum.conversation_id", cfg.ConversationID),
		attribute.String("opik.metadata.stratum.agent_id", agentID),
		attribute.String("opik.metadata.stratum.agent_name", agentName),
		attribute.String("opik.metadata.stratum.evaluation", fmt.Sprintf("%t", cfg.EvolutionTrace.Evaluation)),
		attribute.String("opik.metadata.stratum.security_violation", fmt.Sprintf("%t", cfg.EvolutionTrace.SecurityViolation)),
		attribute.String("opik.metadata.stratum.experiment_id", cfg.EvolutionTrace.ExperimentID),
		attribute.String("opik.metadata.stratum.experiment_variant", cfg.EvolutionTrace.Variant),
		attribute.String("opik.metadata.stratum.experiment_assignments", string(assignments)),
		attribute.String("opik.metadata.stratum.resource_manifest", string(manifest)),
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
			Name:         "stratum_search_knowledge",
			Description:  strings.TrimRight(b.String(), "\n"),
			ProviderType: domain.ProviderTypeBuiltin,
			ProviderID:   "stratum_search_knowledge",
			CapabilityID: "stratum_search_knowledge",
			NodeType:     domain.ObservationTypeRetriever,
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
			Name:         "stratum_recall_memory",
			Description:  "Search long-term memory for relevant past interactions, entities, and context. Use when you need to recall information from previous conversations.",
			ProviderType: domain.ProviderTypeBuiltin,
			ProviderID:   "stratum_recall_memory",
			CapabilityID: "stratum_recall_memory",
			NodeType:     domain.ObservationTypeMemory,
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
		Name:         "stratum_continue_reasoning",
		Description:  "Request another reasoning turn to continue chain-of-thought before calling other tools or producing a final answer. Use when you need more reasoning steps.",
		ProviderType: domain.ProviderTypeBuiltin,
		ProviderID:   "stratum_continue_reasoning",
		CapabilityID: "stratum_continue_reasoning",
		NodeType:     domain.ObservationTypeAgent,
		InputSchema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
			"required":   []string{},
		},
	})
	return tools
}

func buildExecutionArtifacts(toolArtifacts []domain.SystemAssistantToolArtifact, profileVersion string) []domain.ExecutionArtifact {
	if len(toolArtifacts) == 0 {
		return []domain.ExecutionArtifact{}
	}
	citations := make([]domain.Citation, 0)
	seenCitations := make(map[string]struct{})
	hasDiagnostic := false
	for _, artifact := range toolArtifacts {
		if artifact.Tool == "stratum_search_official_docs" {
			for _, citation := range domain.BoundCitations(artifact.Citations) {
				key := citation.DocumentID + "\x00" + citation.Section + "\x00" + citation.URL
				if _, ok := seenCitations[key]; ok {
					continue
				}
				seenCitations[key] = struct{}{}
				if len(citations) < constants.SystemAssistantCitationMaxCount {
					citations = append(citations, citation)
				}
			}
		}
		if artifact.Evidence != nil || artifact.Tool == "stratum_diagnose_tenant" {
			hasDiagnostic = true
		}
	}
	out := make([]domain.ExecutionArtifact, 0, 2)
	if len(citations) > 0 {
		out = append(out, domain.ExecutionArtifact{Type: "citations", ProfileVersion: profileVersion, Citations: citations})
	}
	if hasDiagnostic {
		out = append(out, domain.ExecutionArtifact{Type: "diagnostic_report", ProfileVersion: profileVersion, DiagnosticReport: domain.BuildDiagnosticReport(toolArtifacts)})
	}
	return boundExecutionArtifactsJSON(out)
}

func boundExecutionArtifactsJSON(artifacts []domain.ExecutionArtifact) []domain.ExecutionArtifact {
	for {
		raw, err := json.Marshal(artifacts)
		if err == nil && len(raw) <= constants.SystemAssistantToolMaxJSONBytes {
			return artifacts
		}
		changed := false
		for i := range artifacts {
			report := artifacts[i].DiagnosticReport
			if report == nil {
				continue
			}
			switch {
			case len(report.Facts) > 0:
				report.Facts = report.Facts[:len(report.Facts)-1]
				changed = true
			case len(report.EvidenceGaps) > 0:
				report.EvidenceGaps = report.EvidenceGaps[:len(report.EvidenceGaps)-1]
				changed = true
			case len(report.Citations) > 0:
				report.Citations = report.Citations[:len(report.Citations)-1]
				changed = true
			}
		}
		if !changed {
			return []domain.ExecutionArtifact{{Type: "diagnostic_report", ProfileVersion: artifacts[0].ProfileVersion, DiagnosticReport: &domain.DiagnosticReport{Facts: []domain.DiagnosticFact{}, Inferences: []string{}, EvidenceGaps: []domain.EvidenceGap{{Source: "artifact_aggregate", Code: "truncated"}}, RecommendedActions: []string{}, Citations: []domain.Citation{}, Steps: []domain.DiagnosticStep{{Tool: "artifact_aggregate", Outcome: "error", ErrorCode: "truncated"}}}}}
		}
	}
}
