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
	MaxSteps    int
	Timeout     time.Duration
	Temperature float32
	MaxTokens   int
	// CompactionRecentGroups overrides in-loop compaction recent groups.
	// 0 = auto-derive from MaxContextTokens.
	CompactionRecentGroups int
	// CompactionSafetyRatio overrides the compaction safety ratio. 0 = default.
	CompactionSafetyRatio     float32
	EnableTools               bool
	AvailableTools            []string
	Stream                    bool
	TokenCallback             func(string)
	TenantID                  string
	TraceID                   string
	ExecutionID               string
	RAGSearchFn               func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	ExtraTools                []port.ToolDefinition
	SkillCatalog              map[string]port.SkillActivation
	ToolExecutionFn           port.ToolExecutionFn
	Actives                   []port.SkillActivation
	TracePayloadStore         port.TracePayloadStore
	ConversationID            string
	UserID                    string
	HistoryWindow             int
	EvolutionTrace            EvolutionTraceMetadata
	SystemAssistantMode       bool
	SystemAssistantRoleClass  string
	OfficialDocsSearchFn      func(context.Context, string) ([]domain.Citation, error)
	DiagnosticFn              func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)
	ProposalCreateFn          func(context.Context, map[string]any) (domain.ResourceChangeProposalArtifact, error)
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

// agentExecContext bundles immutable execution-scope values extracted under lock.
type agentExecContext struct {
	cfg              *ExecutionConfig
	tracer           oteltrace.Tracer
	agentID          string
	agentName        string
	systemPrompt     string
	llmModel         string
	capGW            port.CapabilityGateway
	historyCompactor port.HistoryCompactor
	maxContextTokens int
	memoryScope      string
	workspaceNames   []string
	workspaceDescs   []string
	memCtx           string
	history          []*ChatMessage
	input            string
}

// Execute implements the Agent interface - base implementation with ReAct pattern
// agentExecSnapshot is the immutable view of the mutable agent configuration
// taken under lock at execution start, released before the long LLM call.
type agentExecSnapshot struct {
	agentID          string
	agentName        string
	agentType        domain.AgentType
	systemPrompt     string
	llmModel         string
	capGW            port.CapabilityGateway
	historyCompactor port.HistoryCompactor
	chatStore        ChatStore
	metrics          observability.MetricsProvider
	workspaceNames   []string
	workspaceDescs   []string
	maxContextTokens int
	memoryScope      string
}

// snapshotExecutionConfig copies the mutable configuration under lock and
// backfills unset execution options: explicit options win, agent-config values
// fill in fields the caller left at zero so the revision → execution path
// carries temperature / max_tokens / compaction through.
func (a *BaseAgent) snapshotExecutionConfig(cfg *ExecutionConfig) agentExecSnapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = a.MaxIterations
	}
	// cfg.Timeout stays 0 (no deadline) unless the client explicitly passes a
	// timeout option. Step limits + per-operation timeouts bound execution;
	// a wall-clock deadline is optional and client-controlled.
	if cfg.Temperature == 0 {
		cfg.Temperature = a.Temperature
	}
	if cfg.MaxTokens == 0 {
		cfg.MaxTokens = a.MaxTokens
	}
	if cfg.CompactionRecentGroups == 0 {
		cfg.CompactionRecentGroups = a.CompactionRecentGroups
	}
	if cfg.CompactionSafetyRatio == 0 {
		cfg.CompactionSafetyRatio = a.CompactionSafetyRatio
	}
	return agentExecSnapshot{
		agentID:          a.ID,
		agentName:        a.Name,
		agentType:        domain.ReActAgent,
		systemPrompt:     a.SystemPrompt + globalSystemSuffix(a.GlobalSystemSuffix),
		llmModel:         a.LLMModel,
		capGW:            a.CapGateway,
		historyCompactor: a.HistoryCompactor,
		chatStore:        a.ChatStore,
		metrics:          a.metrics,
		workspaceNames:   a.KnowledgeWorkspaceNames,
		workspaceDescs:   a.KnowledgeWorkspaceDescriptions,
		maxContextTokens: a.MaxContextTokens,
		memoryScope:      a.MemoryScope,
	}
}

// globalSystemSuffix appends the global suffix to the agent system prompt,
// or nothing when unset.
func globalSystemSuffix(suffix string) string {
	if suffix == "" {
		return ""
	}
	return "\n\n" + suffix
}

func (a *BaseAgent) Execute(ctx context.Context, input string, options ...ExecutionOption) (*AgentResult, error) {
	startTime := time.Now()

	cfg := &ExecutionConfig{}
	cfg.ApplyOptions(options)

	// Snapshot mutable fields under lock, then release before the long LLM call.
	snap := a.snapshotExecutionConfig(cfg)

	tracer := otel.Tracer("stratum/agent")
	executionAttrs := agentExecutionAttributes(snap.agentID, snap.agentName, snap.agentType, *cfg)
	requestSpan := oteltrace.SpanFromContext(ctx)
	requestSpan.SetAttributes(executionAttrs...)
	ctx, execSpan := tracer.Start(ctx, "agent.execute",
		oteltrace.WithAttributes(executionAttrs...),
	)
	defer execSpan.End()

	memCtx, memErr := a.injectMemoryContext(ctx, tracer, cfg, snap.agentID, snap.memoryScope, input)

	a.Logger.Info("agent execution started",
		zap.String("agent_id", snap.agentID),
		zap.String("trace_id", cfg.TraceID),
		zap.String("conversation_id", cfg.ConversationID),
		zap.String("type", string(snap.agentType)))

	result := &AgentResult{
		AgentID:  snap.agentID,
		Input:    input,
		Metadata: map[string]interface{}{},
	}

	history, histErr := a.loadConversationHistory(ctx, tracer, snap.chatStore, cfg)

	ec := agentExecContext{
		cfg: cfg, tracer: tracer, agentID: snap.agentID, agentName: snap.agentName,
		systemPrompt: snap.systemPrompt, llmModel: snap.llmModel, capGW: snap.capGW,
		historyCompactor: snap.historyCompactor, maxContextTokens: snap.maxContextTokens,
		memoryScope: snap.memoryScope, workspaceNames: snap.workspaceNames,
		workspaceDescs: snap.workspaceDescs, memCtx: memCtx, history: history,
		input: input,
	}

	var execErr error
	// Fail closed before any LLM/tool work: memory context and conversation
	// history are part of the execution context, and the execution must not
	// start when either cannot be loaded (see injectMemoryContext and
	// loadConversationHistory).
	switch {
	case memErr != nil:
		execErr = fmt.Errorf("agent: memory context preparation: %w", memErr)
	case histErr != nil:
		execErr = fmt.Errorf("agent: conversation history preparation: %w", histErr)
	default:
		switch snap.agentType {
		case ReActAgent:
			execErr = a.executeReAct(ctx, ec, result)
		case CoTAgent:
			execErr = a.executeCoT(cfg, input, result)
		case PlanningAgent:
			execErr = a.executePlanning(ctx, ec, result)

		case ToolCallingAgent, RAGAgent, SwarmAgent:
			result.Output = fmt.Sprintf("%s agent type not yet implemented", string(snap.agentType))
			execErr = fmt.Errorf("agent type %s not implemented", snap.agentType)

		default:
			result.Output = "Unknown agent type"
			execErr = fmt.Errorf("unknown agent type: %s", snap.agentType)
		}
	}
	result.Artifacts = buildExecutionArtifacts(result.AssistantToolArtifacts, cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"])

	a.persistChatMessages(ctx, tracer, snap.chatStore, cfg, result, input, snap.agentID, snap.memoryScope, execErr)

	result.Duration = time.Since(startTime)
	a.mu.Lock()
	result.Steps = a.State.StepsTaken
	a.mu.Unlock()

	status := domain.ExecStatusSuccess
	if execErr != nil {
		status = domain.ExecStatusError
	}
	completionAttrs := []attribute.KeyValue{
		attribute.String("opik.metadata.stratum.status", status),
		attribute.Int64("opik.metadata.stratum.duration_ms", result.Duration.Milliseconds()),
		attribute.Int64("opik.metadata.stratum.total_tokens", int64(result.TokensUsed)),
		attribute.Float64("opik.metadata.stratum.cost_usd", result.CostUSD),
	}
	execSpan.SetAttributes(completionAttrs...)
	requestSpan.SetAttributes(completionAttrs...)
	snap.metrics.IncAgentExecution(snap.agentID, string(snap.agentType), status)
	snap.metrics.RecordAgentExecutionDuration(snap.agentID, string(snap.agentType), result.Duration.Seconds())
	snap.metrics.RecordAgentStepCount(snap.agentID, string(snap.agentType), result.Steps)

	recordFingerprintAndKPI(snap.metrics, execSpan, requestSpan, snap.agentID, string(snap.agentType), snap.llmModel, snap.systemPrompt, cfg, snap.maxContextTokens, result, status)

	return result, execErr
}

func recordFingerprintAndKPI(
	metrics observability.MetricsProvider,
	execSpan, requestSpan oteltrace.Span,
	agentID, taskKind, llmModel, systemPrompt string,
	cfg *ExecutionConfig,
	maxContextTokens int,
	result *AgentResult,
	status string,
) {
	metrics.IncAgentTaskCompleted(agentID, taskKind, taskKind, status)
	metrics.RecordAgentTaskLatency(agentID, taskKind, result.Duration.Seconds())
	metrics.RecordAgentCostPerTask(agentID, taskKind, result.CostUSD)
	metrics.RecordAgentConversationTurn(agentID, result.Steps)
	// 指纹记录实际解析模型与路由链：fallback 降级后 ModelResolved 为实际
	// 成功模型，ModelRoutedVia 为尝试过的模型链；未降级时保持配置模型。
	resolved := llmModel
	if result.ModelResolved != "" {
		resolved = result.ModelResolved
	}
	fp := CaptureFingerprint(resolved, result.ModelRoutedVia, systemPrompt, skillRevisionHashes(cfg.SkillCatalog),
		tunableSnapshot(cfg, maxContextTokens), 0)
	fpAttrs := fingerprintAttributes(fp)
	execSpan.SetAttributes(fpAttrs...)
	requestSpan.SetAttributes(fpAttrs...)
}

// tunableSnapshot records the effective tunable values applied to this
// execution so the fingerprint attributes attribute runs to their tunables.
func tunableSnapshot(cfg *ExecutionConfig, maxContextTokens int) map[string]any {
	return map[string]any{
		"temperature":              cfg.Temperature,
		"max_tokens":               cfg.MaxTokens,
		"max_context_tokens":       maxContextTokens,
		"compaction_recent_groups": cfg.CompactionRecentGroups,
		"compaction_safety_ratio":  cfg.CompactionSafetyRatio,
	}
}

// injectMemoryContext builds the memory context injected into the system
// prompt. When a MemoryInjector is configured, memory retrieval is part of the
// execution contract: a failure aborts the execution (fail closed) instead of
// silently running without memory context and producing a different answer.
func (a *BaseAgent) injectMemoryContext(ctx context.Context, tracer oteltrace.Tracer, cfg *ExecutionConfig, agentID, memoryScope, input string) (string, error) {
	if cfg.SystemAssistantMode || a.MemoryInjector == nil || cfg.ConversationID == "" {
		return "", nil
	}
	ic := port.InjectionContext{
		TenantID: cfg.TenantID, UserID: cfg.UserID, AgentID: agentID,
		ConversationID: cfg.ConversationID, Query: input, Scope: memoryScope,
	}
	memSpanCtx, memSpan := tracer.Start(ctx, "agent.memory_inject")
	memInjectCtx, memInjectCancel := context.WithTimeout(memSpanCtx, constants.AgentMemoryInjectTimeout)
	mctx, memInjectErr := a.MemoryInjector.BuildContext(memInjectCtx, ic)
	memInjectCancel()
	memSpan.End()
	if memInjectErr != nil {
		a.Logger.Error("agent.memory_inject_failed",
			zap.String("agent_id", agentID),
			zap.String("conversation_id", cfg.ConversationID),
			zap.Error(memInjectErr))
		return "", fmt.Errorf("inject memory context: %w", memInjectErr)
	}
	return mctx, nil
}

// loadConversationHistory loads prior turns from the chat store. History is
// part of the execution context: a load failure aborts the execution (fail
// closed) instead of running without conversation continuity.
func (a *BaseAgent) loadConversationHistory(ctx context.Context, tracer oteltrace.Tracer, chatStore ChatStore, cfg *ExecutionConfig) ([]*ChatMessage, error) {
	if chatStore == nil || cfg.ConversationID == "" {
		return nil, nil
	}
	histSpanCtx, histSpan := tracer.Start(ctx, "agent.history_load")
	histCtx, histCancel := context.WithTimeout(histSpanCtx, constants.AgentDBQueryTimeout)
	msgs, histErr := chatStore.ListMessages(histCtx, cfg.TenantID, cfg.ConversationID, cfg.UserID)
	histCancel()
	histSpan.End()
	if histErr != nil {
		a.Logger.Error("agent.history_load_failed",
			zap.String("agent_id", a.ID),
			zap.String("conversation_id", cfg.ConversationID),
			zap.Error(histErr))
		return nil, fmt.Errorf("load conversation history: %w", histErr)
	}
	return msgs, nil
}

func (a *BaseAgent) executeReAct(ctx context.Context, ec agentExecContext, result *AgentResult) error {
	if ec.capGW == nil {
		return fmt.Errorf("react: CapGateway not set")
	}
	cg, buildErr := agentgraph.BuildReActGraph(ec.capGW, a.Ledger, a.Logger)
	if buildErr != nil {
		return fmt.Errorf("react: build graph: %w", buildErr)
	}
	maxTokens := ec.maxContextTokens
	if maxTokens <= 0 {
		maxTokens = constants.DefaultAgentContextTokens
	}
	initMessages := BuildContextMessagesWithCompaction(
		ctx, ec.systemPrompt, ec.memCtx, ec.history, ec.input, maxTokens, ec.cfg.HistoryWindow, ec.historyCompactor,
	)

	// Resume from checkpoint if one exists.
	activePlan, restoredActives, initMessages := a.resumeFromCheckpoint(
		ctx, ec, initMessages,
	)

	initState := a.buildReActInitState(ec, initMessages, maxTokens)
	initState.ActivePlan = activePlan
	if len(restoredActives) > 0 {
		initState.Actives = restoredActives
	}
	initState.PlanNodeExecutor = a.buildPlanNodeExecutor(ec, ec.capGW)
	if !ec.cfg.SystemAssistantMode && a.RecallMemoryFn != nil {
		fn := a.RecallMemoryFn
		initState.RecallMemoryFn = func(ctx context.Context, input map[string]any) (string, error) {
			return fn(ctx, ec.cfg.TenantID, ec.cfg.UserID, ec.agentID, ec.memoryScope, input)
		}
	}
	var execCtx context.Context
	var cancel context.CancelFunc
	if ec.cfg.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, ec.cfg.Timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}
	execCtx = reqctx.WithTraceID(execCtx, ec.cfg.TraceID)
	execCtx = reqctx.WithTenantID(execCtx, ec.cfg.TenantID)
	defer cancel()
	// Graph steps count both LLM and Tool node executions.
	// MaxLLMSteps (set in buildReActInitState from ec.cfg.MaxSteps)
	// counts only LLM calls and triggers forced answer at
	// s.Steps >= MaxLLMSteps-1. Double it and add one so the
	// forced-answer mechanism engages before the graph loop
	// exhausts the step budget. Keep 0 as-is so Invoke falls
	// back to its internal default.
	graphSteps := ec.cfg.MaxSteps
	if graphSteps > 0 {
		graphSteps = graphSteps*2 + 1
	}
	runCfg := agentgraph.RunConfig[agentgraph.ReActState]{MaxSteps: graphSteps}
	if a.CheckpointEnabled && a.CheckpointStore != nil {
		runCfg.AfterStep = func(afterCtx context.Context, afterState agentgraph.ReActState) error {
			return agentgraph.PersistReActCheckpoint(afterCtx, a.CheckpointStore, ec.cfg.TenantID, agentgraph.PlanCheckpointIdentity{
				ExecutionID: ec.cfg.ExecutionID, TraceID: ec.cfg.TraceID, ConversationID: ec.cfg.ConversationID, AgentID: ec.agentID, UserID: ec.cfg.UserID,
			}, &afterState, "")
		}
	}
	graphCtx, reactSpan := ec.tracer.Start(execCtx, "react.graph.invoke",
		oteltrace.WithAttributes(attribute.Int("max_steps", ec.cfg.MaxSteps)),
	)
	finalState, runErr := cg.Invoke(graphCtx, initState, runCfg)
	reactSpan.End()
	if runErr == nil && a.CheckpointStore != nil {
		markCtx, markCancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
		_ = a.CheckpointStore.MarkCompleted(markCtx, ec.cfg.TenantID, ec.cfg.ExecutionID)
		markCancel()
	}
	if runErr != nil {
		return fmt.Errorf("react: %w", runErr)
	}
	a.collectGraphResult(result, finalState, ec)
	a.appendFinalAnswerEvent(result, finalState, ec)
	a.mu.Lock()
	a.State.StepsTaken = finalState.Steps
	a.mu.Unlock()
	for _, tc := range finalState.AllToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{ToolName: tc.Name, Input: tc.Arguments})
	}
	return nil
}

func (a *BaseAgent) executeCoT(cfg *ExecutionConfig, input string, result *AgentResult) error {
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
			return nil
		}
	}
	return nil
}

// effectiveStuckThreshold returns the configured stuck threshold or the
// default when unset (≤0).
func (a *BaseAgent) effectiveStuckThreshold() int {
	if a.StuckThreshold <= 0 {
		return constants.DefaultStuckThreshold
	}
	return a.StuckThreshold
}

func (a *BaseAgent) executePlanning(ctx context.Context, ec agentExecContext, result *AgentResult) error {
	if ec.capGW == nil {
		return fmt.Errorf("planning: CapGateway not set")
	}
	stuckThreshold := a.effectiveStuckThreshold()
	var cpWriter agentgraph.PlanCheckpointWriter
	if a.CheckpointStore != nil {
		cpWriter = a.CheckpointStore
	}
	cg, buildErr := agentgraph.BuildPlanExecuteGraph(ec.capGW, a.Ledger, cpWriter, nil, a.Logger)
	if buildErr != nil {
		return fmt.Errorf("planning: build graph: %w", buildErr)
	}
	maxTokens := ec.maxContextTokens
	if maxTokens <= 0 {
		maxTokens = constants.DefaultAgentContextTokens
	}
	initMessages := BuildContextMessagesWithCompaction(
		ctx, ec.systemPrompt, ec.memCtx, ec.history, ec.input, maxTokens, ec.cfg.HistoryWindow, ec.historyCompactor,
	)
	initState := a.buildReActInitState(ec, initMessages, maxTokens)
	initState.StuckThreshold = stuckThreshold
	initState.CheckpointEnabled = a.CheckpointEnabled
	if a.RecallMemoryFn != nil {
		fn := a.RecallMemoryFn
		initState.RecallMemoryFn = func(ctx context.Context, input map[string]any) (string, error) {
			return fn(ctx, ec.cfg.TenantID, ec.cfg.UserID, ec.agentID, ec.memoryScope, input)
		}
	}
	var execCtx context.Context
	var cancel context.CancelFunc
	if ec.cfg.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, ec.cfg.Timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}
	execCtx = reqctx.WithTraceID(execCtx, ec.cfg.TraceID)
	execCtx = reqctx.WithTenantID(execCtx, ec.cfg.TenantID)
	defer cancel()
	graphCtx, planSpan := ec.tracer.Start(execCtx, "planning.graph.invoke",
		oteltrace.WithAttributes(attribute.Int("stuck_threshold", stuckThreshold)),
	)
	planSteps := ec.cfg.MaxSteps
	if planSteps > 0 {
		planSteps = planSteps*2 + 1
	}
	finalState, runErr := cg.Invoke(graphCtx, initState, agentgraph.RunConfig[agentgraph.ReActState]{MaxSteps: planSteps})
	planSpan.End()
	if runErr != nil {
		return fmt.Errorf("planning: %w", runErr)
	}
	a.collectGraphResult(result, finalState, ec)
	a.appendFinalAnswerEvent(result, finalState, ec)
	a.mu.Lock()
	a.State.StepsTaken = finalState.Steps
	a.mu.Unlock()
	for _, tc := range finalState.AllToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{ToolName: tc.Name, Input: tc.Arguments})
	}
	return nil
}

func (a *BaseAgent) buildReActInitState(ec agentExecContext, initMessages []port.LLMMessage, maxTokens int) agentgraph.ReActState {
	availableTools := buildBuiltinTools(ec.workspaceNames, ec.workspaceDescs,
		len(ec.workspaceNames) > 0 && ec.cfg.RAGSearchFn != nil, a.MemoryInjector != nil)
	if ec.cfg.SystemAssistantMode {
		availableTools = nil
	}
	return agentgraph.ReActState{
		TenantID:               ec.cfg.TenantID,
		TraceID:                ec.cfg.TraceID,
		ConversationID:         ec.cfg.ConversationID,
		Model:                  ec.llmModel,
		Temperature:            ec.cfg.Temperature,
		MaxTokens:              ec.cfg.MaxTokens,
		CompactionRecentGroups: ec.cfg.CompactionRecentGroups,
		CompactionSafetyRatio:  ec.cfg.CompactionSafetyRatio,
		// TokenCorrection must start at 1.0: the zero value would divide the
		// compaction threshold by zero on the first step.
		TokenCorrection:            1.0,
		Messages:                   initMessages,
		OnToken:                    ec.cfg.TokenCallback,
		AvailableTools:             mergeTools(availableTools, ec.cfg.ExtraTools, a.Logger),
		SkillCatalog:               ec.cfg.SkillCatalog,
		Actives:                    ec.cfg.Actives,
		TracePayloadStore:          ec.cfg.TracePayloadStore,
		ToolExecutionFn:            ec.cfg.ToolExecutionFn,
		GovernedAssistant:          ec.cfg.SystemAssistantMode,
		ExecutionID:                ec.cfg.ExecutionID,
		AgentKnowledgeWorkspaceIDs: ec.workspaceNames,
		AgentMemoryScope:           ec.memoryScope,
		RAGSearchFn:                ec.cfg.RAGSearchFn,
		OfficialDocsSearchFn:       ec.cfg.OfficialDocsSearchFn,
		DiagnosticFn:               ec.cfg.DiagnosticFn,
		ProposalCreateFn:           ec.cfg.ProposalCreateFn,
		InternalToolResultGuardFn:  ec.cfg.InternalToolResultGuardFn,
		MaxLLMSteps:                ec.cfg.MaxSteps,
		MaxContextTokens:           maxTokens,
		CheckpointEnabled:          a.CheckpointEnabled,
		HistoryCompactor:           ec.historyCompactor,
		PlanCheckpointWriter:       a.CheckpointStore,
		PlanCheckpointIdentity: agentgraph.PlanCheckpointIdentity{
			ExecutionID: ec.cfg.ExecutionID, TraceID: ec.cfg.TraceID,
			ConversationID: ec.cfg.ConversationID, AgentID: ec.agentID, UserID: ec.cfg.UserID,
		},
		PlanIDSource: uuid.NewString,
		PlanLimits: domain.PlanLimits{
			MaxNodes: constants.DefaultPlanMaxNodes, MaxRevisions: constants.DefaultPlanMaxRevisions,
			MaxAttemptsPerNode: constants.DefaultPlanMaxAttemptsPerNode, MaxConcurrentNodes: constants.DefaultPlanMaxConcurrentNodes,
		},
	}
}

func (a *BaseAgent) buildPlanNodeExecutor(ec agentExecContext, capGW port.CapabilityGateway) agentgraph.PlanNodeExecutor {
	return func(nodeCtx context.Context, parent agentgraph.ReActState, node domain.PlanNode, summaries map[string]string) (agentgraph.PlanNodeExecutionResult, error) {
		nodeGraph, graphErr := agentgraph.BuildReActGraph(capGW, a.Ledger, a.Logger)
		if graphErr != nil {
			return agentgraph.PlanNodeExecutionResult{}, graphErr
		}
		systemMessage := port.LLMMessage{Role: "system", Content: ec.systemPrompt}
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
		subSteps := constants.DefaultStepMaxLLMSteps*2 + 1
		final, invokeErr := nodeGraph.Invoke(nodeCtx, child, agentgraph.RunConfig[agentgraph.ReActState]{MaxSteps: subSteps})
		if invokeErr != nil {
			return agentgraph.PlanNodeExecutionResult{}, invokeErr
		}
		return agentgraph.PlanNodeExecutionResult{Summary: final.Output}, nil
	}
}

func (a *BaseAgent) collectGraphResult(result *AgentResult, finalState agentgraph.ReActState, ec agentExecContext) {
	result.Output = finalState.Output
	result.Steps = finalState.Steps
	result.TokensUsed = finalState.TotalTokens
	result.CostUSD = finalState.TotalCostUSD
	result.ModelResolved = finalState.ModelResolved
	result.ModelRoutedVia = finalState.ModelRoutedVia
	result.ToolObservations = enrichToolObservations(finalState.ToolObservations, ec.cfg.TraceID, ec.cfg.ExecutionID, ec.cfg.ConversationID, ec.agentID, ec.cfg.UserID)
	result.TraceEvents = enrichTraceEvents(finalState.TraceEvents, ec.cfg.TraceID, ec.cfg.ExecutionID, ec.cfg.ConversationID, ec.agentID, ec.cfg.UserID)
	result.AssistantToolArtifacts = append([]domain.SystemAssistantToolArtifact(nil), finalState.AssistantToolArtifacts...)
}

func (a *BaseAgent) appendFinalAnswerEvent(result *AgentResult, finalState agentgraph.ReActState, ec agentExecContext) {
	finalAnswerAt := time.Now()
	result.TraceEvents = append(result.TraceEvents, domain.AgentTraceEvent{
		TraceID:         ec.cfg.TraceID,
		ExecutionID:     ec.cfg.ExecutionID,
		ConversationID:  ec.cfg.ConversationID,
		AgentID:         ec.agentID,
		UserID:          ec.cfg.UserID,
		RunType:         domain.RunTypeAgent,
		ObservationType: domain.ObservationTypeAgent,
		EventType:       domain.TraceEventFinalAnswer,
		StepIndex:       finalState.Steps,
		Status:          domain.ToolTraceStatusSuccess,
		Output:          map[string]any{"content": finalState.Output},
		Summary:         truncateRunes(finalState.Output, 500),
		Model:           ec.llmModel,
		TotalTokens:     finalState.TotalTokens,
		CostUSD:         finalState.TotalCostUSD,
		ProviderType:    domain.ProviderTypeLLM,
		ProviderID:      ec.llmModel,
		SequenceNo:      int64(len(result.TraceEvents) + 1),
		StartedAt:       finalAnswerAt,
		EndedAt:         finalAnswerAt,
	})
}

func (a *BaseAgent) persistChatMessages(ctx context.Context, tracer oteltrace.Tracer, chatStore ChatStore, cfg *ExecutionConfig, result *AgentResult, input, agentID, memoryScope string, execErr error) {
	if chatStore == nil || cfg.ConversationID == "" || execErr != nil {
		return
	}
	userMsg := &ChatMessage{
		ConversationID: cfg.ConversationID, Role: "user", Content: input,
		UserID: cfg.UserID, AgentID: agentID, MemoryScope: memoryScope,
		SkipOutbox: false, Visibility: domain.ChatMessageVisibilityUser,
	}
	_, saveUserSpan := tracer.Start(ctx, "agent.chat_store.save_user")
	saveCtx1, saveCancel1 := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	addUserErr := chatStore.AddMessage(saveCtx1, cfg.TenantID, userMsg)
	saveCancel1()
	saveUserSpan.End()
	if addUserErr != nil {
		a.Logger.Warn("agent: failed to save user message", zap.String("conversation_id", cfg.ConversationID), zap.Error(addUserErr))
	}
	agentMsg := &ChatMessage{
		ConversationID: cfg.ConversationID, Role: "assistant", Content: result.Output,
		UserID: cfg.UserID, AgentID: agentID, MemoryScope: memoryScope,
		SkipOutbox: false, Visibility: domain.ChatMessageVisibilityUser, Artifacts: result.Artifacts,
	}
	_, saveAgentSpan := tracer.Start(ctx, "agent.chat_store.save_assistant")
	saveCtx2, saveCancel2 := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	addAgentErr := chatStore.AddMessage(saveCtx2, cfg.TenantID, agentMsg)
	saveCancel2()
	saveAgentSpan.End()
	if addAgentErr != nil {
		a.Logger.Warn("agent: failed to save agent message", zap.String("conversation_id", cfg.ConversationID), zap.Error(addAgentErr))
	}
	summary := buildToolObservationSummary(result.ToolObservations)
	if summary == "" {
		return
	}
	summaryMsg := &ChatMessage{
		ConversationID: cfg.ConversationID, Role: "assistant", Content: summary,
		UserID: cfg.UserID, AgentID: agentID, MemoryScope: memoryScope,
		SkipOutbox: true, Visibility: domain.ChatMessageVisibilityInternal,
	}
	_, saveSummarySpan := tracer.Start(ctx, "agent.chat_store.save_tool_summary")
	saveCtx3, saveCancel3 := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	addSummaryErr := chatStore.AddMessage(saveCtx3, cfg.TenantID, summaryMsg)
	saveCancel3()
	saveSummarySpan.End()
	if addSummaryErr != nil {
		a.Logger.Warn("agent: failed to save tool summary message", zap.String("conversation_id", cfg.ConversationID), zap.Error(addSummaryErr))
	}
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

// WithMaxTokens sets the max output tokens for each LLM request. 0 = unset.
func WithMaxTokens(maxTokens int) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.MaxTokens = maxTokens
	}
}

// WithCompactionRecentGroups overrides in-loop compaction recent groups.
// 0 = auto-derive from MaxContextTokens.
func WithCompactionRecentGroups(recentGroups int) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.CompactionRecentGroups = recentGroups
	}
}

// WithCompactionSafetyRatio overrides the compaction safety ratio. 0 = default.
func WithCompactionSafetyRatio(ratio float32) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.CompactionSafetyRatio = ratio
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

// WithActiveSkills pins the initial active skill activations for this run
// (scenario path). The slice is copied to avoid aliasing the caller's storage.
func WithActiveSkills(actives []port.SkillActivation) ExecutionOption {
	return func(cfg *ExecutionConfig) {
		cfg.Actives = append([]port.SkillActivation(nil), actives...)
	}
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

func WithSystemAssistantMode() ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.SystemAssistantMode = true }
}

func withSystemAssistantRoleClass(roleClass string) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.SystemAssistantRoleClass = roleClass }
}

// WithOfficialDocsSearchFn attaches the in-process official docs search
// capability used by the system assistant tool.
func WithOfficialDocsSearchFn(fn func(context.Context, string) ([]domain.Citation, error)) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.OfficialDocsSearchFn = fn }
}

// WithDiagnosticFn attaches the in-process tenant diagnostics capability used
// by the system assistant tool.
func WithDiagnosticFn(fn func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.DiagnosticFn = fn }
}

func withProposalCreateFn(fn func(context.Context, map[string]any) (domain.ResourceChangeProposalArtifact, error)) ExecutionOption {
	return func(cfg *ExecutionConfig) { cfg.ProposalCreateFn = fn }
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
	hasReport := false
	out := make([]domain.ExecutionArtifact, 0, 3)
	for _, artifact := range toolArtifacts {
		if artifact.Proposal != nil {
			proposal := *artifact.Proposal
			out = append(out, domain.ExecutionArtifact{Type: "resource_change_proposal", ProfileVersion: profileVersion, ResourceChangeProposal: &proposal})
		}
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
			hasReport = true
		}
		if artifact.ErrorCode != "" {
			hasReport = true
		}
	}
	if len(citations) > 0 {
		out = append(out, domain.ExecutionArtifact{Type: "citations", ProfileVersion: profileVersion, Citations: citations})
	}
	if hasReport {
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

// isResumableCheckpoint reports whether a checkpoint with status s can be
// resumed (running or paused).
func isResumableCheckpoint(s string) bool {
	return s == "running" || s == "paused"
}

// resumeFromCheckpoint restores execution state from the latest checkpoint.
func (a *BaseAgent) resumeFromCheckpoint(
	ctx context.Context, ec agentExecContext, msgs []port.LLMMessage,
) (*domain.Plan, []port.SkillActivation, []port.LLMMessage) {
	if !a.CheckpointEnabled || a.CheckpointStore == nil || ec.cfg.ExecutionID == "" {
		return nil, nil, msgs
	}
	resumeCp, err := a.CheckpointStore.GetLatest(ctx, ec.cfg.TenantID, ec.cfg.ExecutionID)
	if err != nil || resumeCp == nil || !isResumableCheckpoint(resumeCp.Status) {
		return nil, nil, msgs
	}
	a.Logger.Info("agent: resuming from checkpoint",
		zap.String("checkpoint_id", resumeCp.ID),
		zap.String("execution_id", ec.cfg.ExecutionID),
		zap.Int("step_index", resumeCp.StepIndex),
	)
	msgs = restoreMessages(resumeCp.MessagesSnapshotJSON, msgs)
	plan, actives := restorePlanCheckpointState(resumeCp.RuntimeStateJSON, ec.cfg.SkillCatalog)
	return plan, actives, msgs
}

func restoreMessages(raw json.RawMessage, fallback []port.LLMMessage) []port.LLMMessage {
	if len(raw) == 0 {
		return fallback
	}
	var saved []port.LLMMessage
	if json.Unmarshal(raw, &saved) == nil {
		return saved
	}
	return fallback
}

// skillRevisionHashes extracts skillID → revision hash from the skill catalog.
func skillRevisionHashes(catalog map[string]port.SkillActivation) map[string]string {
	if len(catalog) == 0 {
		return nil
	}
	out := make(map[string]string, len(catalog))
	for id, act := range catalog {
		out[id] = act.RevisionID
	}
	return out
}

// fingerprintAttributes converts an ExecutionFingerprint into OTEL span attributes.
func fingerprintAttributes(fp *domain.ExecutionFingerprint) []attribute.KeyValue {
	if fp == nil {
		return nil
	}
	attrs := []attribute.KeyValue{
		attribute.String("stratum.fingerprint.model_resolved", fp.ModelResolved),
		attribute.String("stratum.fingerprint.prompt_version", fp.PromptVersion),
		attribute.String("stratum.fingerprint.content_hash", fp.ContentHash()),
		attribute.Int("stratum.fingerprint.ab_bucket", fp.ABBucket),
	}
	if len(fp.ModelRoutedVia) > 0 {
		b, _ := json.Marshal(fp.ModelRoutedVia)
		attrs = append(attrs, attribute.String("stratum.fingerprint.model_routed_via", string(b)))
	}
	if len(fp.SkillRevisions) > 0 {
		b, _ := json.Marshal(fp.SkillRevisions)
		attrs = append(attrs, attribute.String("stratum.fingerprint.skill_revisions", string(b)))
	}
	if len(fp.TunableSnapshot) > 0 {
		b, _ := json.Marshal(fp.TunableSnapshot)
		attrs = append(attrs, attribute.String("stratum.fingerprint.tunable_snapshot", string(b)))
	}
	return attrs
}

func restorePlanCheckpointState(raw json.RawMessage, catalog map[string]port.SkillActivation) (*domain.Plan, []port.SkillActivation) {
	if len(raw) == 0 {
		return nil, nil
	}
	decoded, err := agentgraph.DecodePlanCheckpoint(raw)
	if err != nil {
		return nil, nil
	}
	var plan *domain.Plan
	if decoded.Plan != nil {
		plan = decoded.Plan
	}
	if len(decoded.ActiveSkills) > 0 {
		// 数组优先；逐条校验 revision、跳过 catalog 外条目并按 SkillID 去重。
		return plan, restoreActivesFromRefs(decoded.ActiveSkills, catalog)
	}
	return plan, restoreLegacyActiveSkill(decoded, catalog)
}

// restoreActivesFromRefs 将 checkpoint 中的 skill refs 还原为 catalog 中的激活
// 快照。revision 不匹配或不在 catalog 的条目跳过；重复 SkillID 保留首个。
func restoreActivesFromRefs(refs []agentgraph.CheckpointSkillRef, catalog map[string]port.SkillActivation) []port.SkillActivation {
	seen := map[string]struct{}{}
	var actives []port.SkillActivation
	for _, ref := range refs {
		activation, ok := catalog[ref.SkillID]
		if !ok || (ref.RevisionID != "" && activation.RevisionID != ref.RevisionID) {
			continue
		}
		if _, dup := seen[activation.SkillID]; dup {
			continue
		}
		seen[activation.SkillID] = struct{}{}
		actives = append(actives, activation)
	}
	return actives
}

// restoreLegacyActiveSkill 回退旧版单条 checkpoint 字段，供旧 payload 恢复。
func restoreLegacyActiveSkill(decoded agentgraph.PlanCheckpointPayload, catalog map[string]port.SkillActivation) []port.SkillActivation {
	if decoded.ActiveSkillID == "" {
		return nil
	}
	activation, ok := catalog[decoded.ActiveSkillID]
	if !ok || (decoded.ActiveSkillRevisionID != "" && activation.RevisionID != decoded.ActiveSkillRevisionID) {
		return nil
	}
	return []port.SkillActivation{activation}
}
