// Package application — agent_service.go.
//
// AgentService is the orchestration façade handlers consume for agent
// CRUD + execution. It aggregates Registry / TenantSettings / repos so
// HTTP handlers degrade to pure transport. SQL/HTTP/IO never appear in
// this file — every persistence call goes through a domain port.

package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/application/factcheck"
	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

type AgentServiceDeps struct {
	Registry                  *Registry
	SkillLookup               port.SkillLookup
	SkillActivationResolver   port.SkillActivationResolver
	SkillRevisionResolver     port.SkillRevisionResolver
	AgentRevisionResolver     port.AgentRevisionResolver
	MCPRevisionResolver       port.MCPRevisionResolver
	KnowledgeRevisionResolver port.KnowledgeRevisionResolver
	RAGSearch                 port.RAGSearchProvider
	TenantResolver            port.TenantCapabilityResolver
	TenantModelValidator      port.TenantChatModelValidator
	TenantModelCatalog        port.TenantChatModelCatalog
	ModelContextProvider      port.ModelContextProvider
	ModelDetailsProvider      port.TenantModelDetailsProvider
	// VendorWindowLookup 解析内置厂商静态能力表（窗口 + 最大输出）。
	// 由 wiring 注入 llmgateway.LookupModelSpec；nil 时回退链跳过 vendor 层。
	VendorWindowLookup func(string) (int, int)
	// HistoryCompactorFactory builds the history compactor for an execution.
	// 压缩三值（提示词/温度/模型）由 compactor 内部从平台参数统一解析
	// （唯一来源，所有 agent 一致），工厂只需 gateway/logger/输出预算。
	HistoryCompactorFactory func(port.CapabilityGateway, *zap.Logger, int) port.HistoryCompactor
	// PlatformPromptResolver 解析平台级提示词参数（agent.compaction_prompt /
	// agent.system_prompt），运行时热更新。nil 时压缩/执行 fail-closed。
	PlatformPromptResolver port.PlatformPromptResolver
	MCPTools               port.MCPToolProvider
	MCPToolExecutor        port.MCPToolExecutor
	MCPToolPolicy          port.MCPToolPolicyResolver
	// MCPServerLister 供系统助手 stratum_list_mcp_servers 只读枚举当前租户
	// 已连接的 MCP server。由 wiring 以薄 ACL 适配 mcp context 的 MCPService；
	// nil 时该工具 fail-closed（graph 层 ListMCPServersFn 未装配）。
	MCPServerLister   port.MCPServerLister
	ToolAuthorizer    *ToolAuthorizer
	ApprovalService   *ToolApprovalService
	ChatStore         ChatStore
	EvidenceProvider  port.TraceEvidenceProvider
	TracePayloadStore port.TracePayloadStore
	CheckpointStore   CheckpointStore
	// CompactionStore 跨轮复用压缩摘要存储。nil 时组装侧保持无复用行为
	// （与旧 BuildContextMessagesWithCompaction 逐字节一致）。
	CompactionStore      port.CompactionStore
	MemoryCleaner        port.AgentMemoryCleaner
	MemoryBuffer         port.BufferMemoryFn
	TrajectoryReflection port.EnqueueTrajectoryReflectionFn
	MemoryInjector       port.MemoryInjector
	RecallMemory         port.RecallMemoryFn
	Metrics              observability.MetricsProvider
	// Ledger 记录 LLM token/成本（span cost + Prometheus 指标）。nil 时保持
	// NoopTokenRecorder（成本恒 0），生产由 wiring 注入 TokenLedger。
	Ledger                    agentgraph.TokenRecorder
	OfficialDocsSearch        func(context.Context, string) ([]domain.Citation, error)
	DiagnosticProvider        port.DiagnosticEvidenceProvider
	ProposalService           *ResourceChangeProposalService
	ResourceChangeApplier     func(context.Context, string, map[string]any) (domain.ApplyResult, error)
	ResourceEditorRepo        port.ResourceEditorRepo
	OperationGate             port.OperationGate
	TenantRoleResolver        port.TenantRoleResolver
	WorkspaceBindingValidator port.WorkspaceBindingValidator
	ParametersProvider        port.ParametersProvider
	// FactCheck 是幻觉校验配置（nil/Enabled=false = 关闭，fail-closed）。
	// EvidenceFn 留空，执行时由 RAGSearchFnWithEvidence 填充。
	FactCheck *factcheck.Settings
	Logger    *zap.Logger
	// FailureAudit 旁路记录失败的资源操作（best-effort，nil 时跳过）。
	FailureAudit auditport.FailureAuditRecorder
}

// AgentService aggregates agent CRUD + Execute/ExecuteStream and shields
// HTTP handlers from cross-context wiring. Construct via NewAgentService.

type AgentService struct {
	deps AgentServiceDeps
}

// NewAgentService wires an AgentService. Logger defaults to NopLogger
// when nil so callers can omit it in unit tests.

func NewAgentService(deps AgentServiceDeps) *AgentService {
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &AgentService{deps: deps}
}

func (s *AgentService) SetSkillRevisionResolver(resolver port.SkillRevisionResolver) {
	s.deps.SkillRevisionResolver = resolver
}

func (s *AgentService) SetResourceChangeProposalService(service *ResourceChangeProposalService) {
	s.deps.ProposalService = service
}

// SetResourceChangeApplier injects the direct-write apply entry point used by
// the system assistant's stratum_apply_resource_change tool. The actorID
// parameter is the conversation initiator; ownership is inherited from their
// role (no system-actor backdoor). Without it the tool fails closed in
// execApplyResourceChangeTool.

func (s *AgentService) SetResourceChangeApplier(fn func(context.Context, string, map[string]any) (domain.ApplyResult, error)) {
	s.deps.ResourceChangeApplier = fn
}

// SetOperationGate injects the operation approval gate. Without a gate the
// gated self-modify entry point fails closed.

func (s *AgentService) SetOperationGate(gate port.OperationGate) {
	s.deps.OperationGate = gate
}

func (s *AgentService) SetTenantRoleResolver(resolver port.TenantRoleResolver) {
	s.deps.TenantRoleResolver = resolver
}

func (s *AgentService) SetAgentRevisionResolver(resolver port.AgentRevisionResolver) {
	s.deps.AgentRevisionResolver = resolver
}

func (s *AgentService) SetMCPRevisionResolver(resolver port.MCPRevisionResolver) {
	s.deps.MCPRevisionResolver = resolver
}

func (s *AgentService) SetKnowledgeRevisionResolver(resolver port.KnowledgeRevisionResolver) {
	s.deps.KnowledgeRevisionResolver = resolver
}

func (s *AgentService) SetMCPToolExecutor(executor port.MCPToolExecutor) {
	s.deps.MCPToolExecutor = executor
}

// CreateAgentInput is the create-agent payload application receives from
// transport.

type CreateAgentInput struct {
	TenantID              string
	ActorID               string
	Name                  string
	Type                  string
	Description           string
	SystemPrompt          string
	LLMModel              string
	MaxIterations         int
	MaxContextTokens      int
	Temperature           float32
	ReasoningEffort       string
	MaxTokens             int
	AllowedSkills         []string
	MCPToolIDs            []string
	KnowledgeWorkspaceIDs []string
	MemoryScope           string
	// DelegateEnabled/MaxDepth/DefaultMaxSteps 控制 stratum_delegate 子 agent
	// 派发：开启后主循环可将子任务委托给复用本配置的隔离子 agent。深度/步数
	// 0=unset → 运行时回落全局默认。
	DelegateEnabled         bool
	DelegateMaxDepth        int
	DelegateDefaultMaxSteps int
	// Parameters carries registry resource-scope values as a flat object;
	// only the memory.* dotted keys persist on the agent (bare sampling keys
	// remain expressed through the explicit fields above). Same merge
	// semantics as UpdateAgentInput.Parameters.
	Parameters map[string]any
	Editors    []string
}

type UpdateAgentInput struct {
	ActorID          string
	Name             string
	Type             string
	Description      string
	SystemPrompt     string
	LLMModel         string
	MaxIterations    int
	MaxContextTokens int
	Temperature      float32
	ReasoningEffort  string
	MaxTokens        int
	// Parameters carries the registry sampling parameters as a flat object;
	// merge semantics — only present keys overwrite, and only non-zero values
	// persist (0 = unset, never clears an existing value). Keys that appear
	// here take precedence over the top-level sampling fields above.
	Parameters map[string]any
	// ReplaceParameters selects agents.parameters JSONB semantics:
	// true = overall replace (promote 写回,零值清除旧值);
	// false = merge (表单路径,零值不落库,旧客户端 PUT 不清除已存参数)。
	ReplaceParameters     bool
	AllowedSkills         []string
	MCPToolIDs            []string
	KnowledgeWorkspaceIDs []string
	MemoryScope           string
	// 委托配置同 CreateAgentInput；DelegateEnabled 为 *bool:缺省(nil)保留已存值,
	// 显式 false 才关闭(存量默认关闭,Update 全量列写不能把缺省当显式 false 覆盖)。
	DelegateEnabled         *bool
	DelegateMaxDepth        int
	DelegateDefaultMaxSteps int
}

// AgentDTO is the wire shape returned by AgentService for transport
// rendering. Strings only — handler reuses field-for-field.

type AgentDTO struct {
	ID                      string
	Name                    string
	Type                    string
	Description             string
	SystemPrompt            string
	LLMModel                string
	MaxIterations           int
	MaxContextTokens        int
	Temperature             float32
	ReasoningEffort         string
	MaxTokens               int
	AllowedSkills           []string
	MCPToolIDs              []string
	KnowledgeWorkspaceIDs   []string
	CreatedAt               string
	MemoryScope             string
	DelegateEnabled         bool
	DelegateMaxDepth        int
	DelegateDefaultMaxSteps int
	Parameters              map[string]any
	Editors                 []string
}

type SystemAssistantSettings struct {
	AgentID         string
	Model           string
	Ready           bool
	AvailableModels []string
}

// Create persists a new agent for the tenant. Only owner/admin roles may
// create; the caller becomes the resource owner (created_by).
// validateSamplingParams rejects out-of-bounds sampling values
// (temperature / max_tokens / reasoning_effort) against the parameter
// registry before persist. 压缩五值（提示词/温度/模型/最近轮数/冷却）是平台级
// 参数，不走 per-agent 写时校验。
// Zero means unset (gateway default) and is skipped; a nil
// provider (db unavailable) degrades to no-op, matching resolve.

func (s *AgentService) validateSamplingParams(
	ctx context.Context, temperature float32, maxTokens int, reasoningEffort string,
) error {
	if s.deps.ParametersProvider == nil {
		return nil
	}
	declared := map[string]any{}
	if temperature != 0 {
		declared["temperature"] = float64(temperature)
	}
	if maxTokens != 0 {
		declared["max_tokens"] = maxTokens
	}
	// ReasoningEffort "" = unset 跳过;非空必须在 low/medium/high 枚举内,否则
	// 非法值会沿执行链路透传到网关对严格端点打 400,中止整条 fallback 链。
	if reasoningEffort != "" {
		declared["reasoning_effort"] = reasoningEffort
	}
	if len(declared) == 0 {
		return nil
	}
	if err := s.deps.ParametersProvider.ValidateResource(ctx, declared); err != nil {
		// %w 保留 sentinel 供错误中间件映射 400;%v 保留越界详情给调用方。
		return fmt.Errorf("%w: agent service: validate sampling parameters: %v",
			domain.ErrInvalidSamplingParameters, err)
	}
	return nil
}

// validateAgentMaxIterations rejects an out-of-range explicit max iterations
// value. 0 = unset (each path keeps its own zero semantics: update/system
// assistant treat it as keep-current, repo persists it as-is elsewhere);
// non-zero must lie in [MinAgentMaxIterations, MaxAgentMaxIterations].

func validateAgentMaxIterations(n int) error {
	if n != 0 && (n < constants.MinAgentMaxIterations || n > constants.MaxAgentMaxIterations) {
		return fmt.Errorf("%w: max iterations must be between %d and %d",
			domain.ErrInvalidMaxIterations, constants.MinAgentMaxIterations, constants.MaxAgentMaxIterations)
	}
	return nil
}
