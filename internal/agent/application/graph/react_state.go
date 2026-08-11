package graph

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

const (
	nodeLLM  = "llm"
	nodeTool = "tool"
)

// ReActState is the mutable state threaded through the ReAct graph.
type ReActState struct {
	TenantID       string
	TraceID        string
	ConversationID string
	Model          string
	Temperature    float32 // 0 = provider default
	MaxTokens      int     // 0 = unset
	// ModelResolved 是本次执行最后一次 LLM 调用实际成功的模型名（fallback
	// 降级后与 Model 不同）；ModelRoutedVia 是实际尝试过的模型链。
	ModelResolved              string
	ModelRoutedVia             []string
	AvailableTools             []port.ToolDefinition
	SkillCatalog               map[string]port.SkillActivation
	Actives                    []port.SkillActivation
	TracePayloadStore          port.TracePayloadStore
	ToolExecutionFn            port.ToolExecutionFn
	GovernedAssistant          bool
	AssistantToolArtifacts     []domain.SystemAssistantToolArtifact
	ExecutionID                string
	AgentKnowledgeWorkspaceIDs []string
	AgentMemoryScope           string
	Messages                   []port.LLMMessage
	AllToolCalls               []port.ToolCall
	ToolObservations           []domain.ToolObservation
	TraceEvents                []domain.AgentTraceEvent
	Output                     string
	Steps                      int
	TotalTokens                int
	TotalCostUSD               float64
	OnToken                    func(string) // if non-nil, stream tokens from the final LLM response
	RAGSearchFn                func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	// RAGSearchFnWithEvidence is the evidence-capable variant; the tool node
	// prefers it over RAGSearchFn when set.
	RAGSearchFnWithEvidence func(ctx context.Context, workspaces []string, query string, topK int) (port.RAGSearchEvidence, error)
	// PromptVersions records the prompt key → content fingerprint map
	// applied to this execution; startLLMTrace writes stratum.prompt.*
	// attributes from it. nil means no version was resolved.
	PromptVersions            map[string]string
	RecallMemoryFn            func(ctx context.Context, input map[string]any) (string, error)
	OfficialDocsSearchFn      func(context.Context, string) ([]domain.Citation, error)
	DiagnosticFn              func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)
	ProposalCreateFn          func(context.Context, map[string]any) (domain.ResourceChangeProposalArtifact, error)
	ResourceChangeApplyFn     func(context.Context, map[string]any) (domain.ApplyResult, error)
	InternalToolResultGuardFn func(any) (port.GuardedToolResult, error)
	// MaxLLMSteps caps LLM-node invocations; on the last allowed call tools are
	// stripped and the model is asked to produce a final answer from collected context.
	MaxLLMSteps int
	// MaxContextTokens bounds each ReAct LLM request. When the
	// accumulated Messages exceed it, older tool-call/tool-result groups are compacted
	// (summarized or dropped) before dispatch. Zero disables in-loop compaction.
	MaxContextTokens int
	// CompactionRecentGroups overrides the recent-groups count during in-loop
	// compaction. 0 = auto-derive from MaxContextTokens.
	CompactionRecentGroups int
	// CompactionSafetyRatio overrides the compaction safety ratio. 0 = default.
	CompactionSafetyRatio float32
	// TokenCorrection is the EMA correction factor applied to the compaction
	// threshold, learned from the previous step's estimated-vs-actual prompt
	// token ratio. Must be > 0; buildReActInitState initializes it to 1.0.
	TokenCorrection float64
	// LastEstimatedTokens is the estimated token count of the previous
	// dispatched request (post-compaction messages + tools). It is the ratio
	// baseline for TokenCorrection; 0 until the first request is dispatched.
	LastEstimatedTokens int
	// HistoryCompactor optionally summarizes evicted groups into a breadcrumb; nil
	// degrades to plain drop-with-marker. Never fails the loop.
	HistoryCompactor port.HistoryCompactor

	// Lazy planning — non-zero StuckThreshold enables Reflect→Plan→Execute path.
	StuckThreshold         int // 0 = disabled
	PlanTriggered          bool
	ReflectionSummary      string
	Plan                   []domain.PlanStep
	PlanTemplateID         string
	CurrentStepIndex       int
	StepResults            []domain.StepResult
	CheckpointEnabled      bool
	ActivePlan             *domain.Plan
	PlanCheckpointWriter   PlanCheckpointWriter
	PlanCheckpointIdentity PlanCheckpointIdentity
	PlanIDSource           func() string
	PlanLimits             domain.PlanLimits
	PlanToolsDisabled      bool
	PlanNodeExecutor       PlanNodeExecutor
	// PlanWavePending 是已排程 plan 波次对应的 plan.Nodes 索引（由
	// stratum_continue_plan 排程填充、槽位节点消费）；PlanWaveOutcomes 收集
	// 本波各槽位的执行结果，由 finalize 节点汇合应用；PlanContinueCallID
	// 标记已排程波次的工具调用 ID，tool 节点据此跳过直接消息追加（观察
	// 由 finalize 在波次汇合后补全）。
	PlanWavePending    []int
	PlanWaveOutcomes   []PlanWaveOutcome
	PlanContinueCallID string
}

// TokenRecorder 是 TokenLedger 的最小接口，供 graph 包使用，避免 import application 包循环。
// Record 返回 (total tokens, cost USD)。
type TokenRecorder interface {
	Record(ctx context.Context, model string, usage port.TokenUsage) (int, float64)
}

// NoopTokenRecorder 满足 TokenRecorder 接口但不执行任何操作，供测试使用。
type NoopTokenRecorder struct{}

func (NoopTokenRecorder) Record(_ context.Context, _ string, usage port.TokenUsage) (int, float64) {
	return usage.Total, 0
}
