package graph

import (
	"context"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
	// ReasoningEffort 是本次执行透传到 LLM 调用的采样强度档位:""|low|medium|
	// high。空串 = unset,由网关按模型能力门控决定是否忽略。
	ReasoningEffort string
	MaxTokens       int // 0 = unset
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
	// ViewerID is the end user executing this session; it scopes every
	// knowledge retrieval to the user's document whitelist.
	ViewerID    string
	RAGSearchFn func(ctx context.Context, workspaces []string, query string, topK int, viewerID string) (string, error)
	// RAGSearchFnWithEvidence is the evidence-capable variant; the tool node
	// prefers it over RAGSearchFn when set.
	RAGSearchFnWithEvidence func(ctx context.Context, workspaces []string, query string, topK int, viewerID string) (port.RAGSearchEvidence, error)
	// CitationSources accumulate retrieval evidence across searches for the
	// chat UI: deduplicated by chunk ID, capped at MaxAgentResultSources,
	// newest search winning. Populated only by evidence-capable searches.
	CitationSources []port.RAGSearchSource
	// NoAnswer 是最近一次知识检索的无答案信号（工具路径检索全空时填充，
	// 拷贝自 evidence 结果；nil = 无知识检索或无答案未发生）。
	NoAnswer *domain.NoAnswerInfo
	// PromptVersions records the prompt key → content fingerprint map
	// applied to this execution; startLLMTrace writes stratum.prompt.*
	// attributes from it. nil means no version was resolved.
	PromptVersions            map[string]string
	RecallMemoryFn            func(ctx context.Context, input map[string]any) (string, error)
	OfficialDocsSearchFn      func(context.Context, string) ([]domain.Citation, error)
	DiagnosticFn              func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)
	ProposalCreateFn          func(context.Context, map[string]any) (domain.ResourceChangeProposalArtifact, error)
	ResourceChangeApplyFn     func(context.Context, map[string]any) (domain.ApplyResult, error)
	ListModelsFn              func(context.Context) (map[string]any, error)
	UpdateSystemModelFn       func(context.Context, string) (map[string]any, error)
	ListAgentsFn              func(context.Context) (map[string]any, error)
	ListMCPServersFn          func(context.Context) (map[string]any, error)
	InternalToolResultGuardFn func(any) (port.GuardedToolResult, error)
	// MaxLLMSteps caps LLM-node invocations; on the last allowed call tools are
	// stripped and the model is asked to produce a final answer from collected context.
	MaxLLMSteps int
	// MaxContextTokens bounds each ReAct LLM request. When the
	// accumulated Messages exceed it, older tool-call/tool-result groups are compacted
	// (summarized or dropped) before dispatch. Zero disables in-loop compaction.
	MaxContextTokens int
	// MaxTokensPerExecution 是本次执行的累计 LLM token 预算（0 = 不设限）。
	// 图级每次 LLM 调用后累计检查，超限终止循环（Spec 第 3 节）。
	MaxTokensPerExecution int
	// TerminatedBy 标记业务终止原因（如 CostBudgetTerminated）；空 = 正常结束。
	TerminatedBy string
	// Budget 是本次执行的预算账本快照（Spec 第 2 节）：初始组装与 ReAct 循环
	// 共享同一来源，一次执行一个。TaskHint 由 application 层 WithTask 登记
	// （最新用户输入），已从 HistoryCap 扣减。零值 = 未初始化 → 循环内压缩
	// 与工具裁剪禁用。
	Budget Budget
	// CompactionRecentGroups overrides the recent-groups count during in-loop
	// compaction. 0 = auto-derive from MaxContextTokens.
	CompactionRecentGroups int
	// TokenCorrection is the EMA correction factor applied to the compaction
	// threshold, learned from the previous step's estimated-vs-actual prompt
	// token ratio. Must be > 0; buildReActInitState initializes it to 1.0.
	TokenCorrection float64
	// CompactionCooldownSec 是压缩冷却窗口（秒）：一次执行内压缩触发后，
	// 冷却期内超限只截断不重复触发同步 LLM 摘要。0 = 用常量默认。
	CompactionCooldownSec int
	// LastCompactionAt 是最近一次 LLM 压缩完成时间；零值表示未压缩过。
	LastCompactionAt time.Time
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

	// CorrectionStreaks 记录每个工具连续失败次数（仅同错指纹累计），按工具名
	// 键控。map 在子状态（plan 槽位子执行）间按引用共享——止损跨子节点全局
	// 累计（3 节点各失败 1 次 = 全局 3 次）。
	CorrectionStreaks map[string]int
	// LastCorrectionFingerprint 是每个工具上次失败错误消息的规范化指纹；
	// 仅当前指纹与上次相同才递增计数，否则重置为 1（真"同错重犯"才累计）。
	LastCorrectionFingerprint map[string]string
	// StopLossTools 标记已超过止损阈值的工具。非空即推导整体降级——bool
	// Degraded 在子状态间值拷贝不传播，而 map 共享引用可传播到父图。
	StopLossTools map[string]bool
	// Degraded 标记本次执行因工具连续失败进入降级：强制收尾指令切换为降级
	// 文案，最终回答不得声称完成了未验证的操作。
	Degraded bool
	// DegradeReason 是降级原因的固定枚举（如 "tool_stop_loss:<tool>"）。
	// 禁止赋 err.Error()——错误正文含 plan_id/revision 等内部标识。
	DegradeReason string
	// TaskCompleteRequested 标记 LLM 调用了 stratum_complete_task（目标达成）。
	// 执行结束时由挂点读入，task 状态转 completed。完成信号独立于 plan 状态。
	TaskCompleteRequested bool
}

// countToolFailure 在工具失败处统一计数一次：先规范化错误消息取同错指纹，
// 与上次相同才累计否则重置为 1；达 AgentToolStopLossThreshold 后标记
// StopLossTools + Degraded + 固定枚举 DegradeReason。nil 安全：裸 ReActState{}
// 构造（如 plan_tools_test 直接调用）不会 panic。
func (s *ReActState) countToolFailure(toolName, errMsg string) {
	if s.CorrectionStreaks == nil {
		s.CorrectionStreaks = map[string]int{}
	}
	if s.LastCorrectionFingerprint == nil {
		s.LastCorrectionFingerprint = map[string]string{}
	}
	if s.StopLossTools == nil {
		s.StopLossTools = map[string]bool{}
	}
	fp := correctionFingerprint(errMsg)
	if s.LastCorrectionFingerprint[toolName] == fp {
		s.CorrectionStreaks[toolName]++
	} else {
		s.CorrectionStreaks[toolName] = 1
	}
	s.LastCorrectionFingerprint[toolName] = fp
	if s.CorrectionStreaks[toolName] >= constants.AgentToolStopLossThreshold {
		s.StopLossTools[toolName] = true
		s.Degraded = true
		if s.DegradeReason == "" {
			s.DegradeReason = constants.AgentDegradeReasonStopLossPrefix + toolName
		}
	}
}

// recordToolFailure 是通用工具失败（result.status == Error）的统一计数入口，
// 供 makeToolNode 在结果层面调用；fatal 错误（需人工审批、知识版本失效）由
// 调用方跳过，不计入止损。
func (s *ReActState) recordToolFailure(toolName, errMsg string) {
	s.countToolFailure(toolName, errMsg)
}

// recordCorrection 在 plan 工具校验失败（correction 路径）处计数并返回
// correction 文案。plan 校验失败在工具节点表现为 status=Success（correction
// 作为观察返回，err 为 nil），通用 status==Error 计数点覆盖不到，必须在此
// 计数，统一止损门才有数据。内容与 correction() 同源。
func (s *ReActState) recordCorrection(toolName string, err error, plan *domain.Plan) string {
	s.countToolFailure(toolName, err.Error())
	return correction(toolName, err, plan)
}

// correctionFingerprint 规范化错误消息作为同错指纹：小写、去首尾空白、折叠
// 连续空白，使格式差异不打断累计。仅用于同错比较，不落日志，无需截断防 PII。
func correctionFingerprint(errMsg string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(errMsg))), " ")
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
