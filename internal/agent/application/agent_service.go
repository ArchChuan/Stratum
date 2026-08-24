// Package application — agent_service.go.
//
// AgentService is the orchestration façade handlers consume for agent
// CRUD + execution. It aggregates Registry / TenantSettings / repos so
// HTTP handlers degrade to pure transport. SQL/HTTP/IO never appear in
// this file — every persistence call goes through a domain port.

package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/application/factcheck"
	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// AgentServiceDeps groups the consumer-side dependencies of AgentService.
// Everything is an interface or value type — no concrete infrastructure
// imports allowed.
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
	CompactionStore           port.CompactionStore
	MemoryCleaner             port.AgentMemoryCleaner
	MemoryBuffer              port.BufferMemoryFn
	TrajectoryReflection      port.EnqueueTrajectoryReflectionFn
	MemoryInjector            port.MemoryInjector
	RecallMemory              port.RecallMemoryFn
	Metrics                   observability.MetricsProvider
	OfficialDocsSearch        func(context.Context, string) ([]domain.Citation, error)
	DiagnosticProvider        port.DiagnosticEvidenceProvider
	ProposalService           *ResourceChangeProposalService
	ResourceChangeApplier     func(context.Context, string, map[string]any) (domain.ApplyResult, error)
	ResourceEditorRepo        port.ResourceEditorRepo
	OperationGate             port.OperationGate
	TenantRoleResolver        port.TenantRoleResolver
	WorkspaceBindingValidator port.WorkspaceBindingValidator
	// SystemResourceGuard 判定 MCP server 是否平台托管(写路径 A2 + 运行时净化 C)。
	// 由 wiring 注入 TTL 缓存适配器;nil 时对非空 MCP 绑定 fail closed。
	SystemResourceGuard port.SystemResourceGuard
	ParametersProvider  port.ParametersProvider
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
	SystemKey               string
	IsSystem                bool
	ManagementMode          string
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

func (s *AgentService) Create(ctx context.Context, in CreateAgentInput) (AgentDTO, error) {
	if err := s.checkOwnership(ctx, in.ActorID, in.ActorID, nil); err != nil {
		return AgentDTO{}, err
	}
	// 压缩五值（提示词/温度/模型/最近轮数/冷却）为平台级参数，不进入 agent
	// 配置；所有 agent 主链路统一从平台 resolver 读取。
	if err := s.validateSamplingParams(ctx, in.Temperature, in.MaxTokens,
		in.ReasoningEffort); err != nil {
		return AgentDTO{}, err
	}
	if err := validateAgentMaxIterations(in.MaxIterations); err != nil {
		return AgentDTO{}, err
	}
	id := uuid.Must(uuid.NewV7()).String()
	memoryParams, err := s.validateAndExtractMemoryParameters(ctx, in.Parameters)
	if err != nil {
		return AgentDTO{}, err
	}
	cfg := &domain.AgentConfig{
		ID:                      id,
		Name:                    in.Name,
		Type:                    parseAgentTypeWire(in.Type),
		Description:             in.Description,
		SystemPrompt:            in.SystemPrompt,
		LLMModel:                in.LLMModel,
		MaxIterations:           in.MaxIterations,
		MaxContextTokens:        in.MaxContextTokens, // 0 = 未配置，执行时两阶段解析
		Temperature:             in.Temperature,
		ReasoningEffort:         in.ReasoningEffort,
		MaxTokens:               in.MaxTokens,
		AllowedSkills:           in.AllowedSkills,
		MCPToolIDs:              in.MCPToolIDs,
		KnowledgeWorkspaceIDs:   in.KnowledgeWorkspaceIDs,
		MemoryScope:             in.MemoryScope,
		DelegateEnabled:         in.DelegateEnabled,
		DelegateMaxDepth:        in.DelegateMaxDepth,
		DelegateDefaultMaxSteps: in.DelegateDefaultMaxSteps,
		MemoryParameters:        memoryParams,
		Capabilities:            []domain.AgentCapability{},
		CreatedBy:               in.ActorID,
	}

	if err := s.validateWorkspaceBindings(ctx, in.TenantID, in.KnowledgeWorkspaceIDs); err != nil {
		return AgentDTO{}, err
	}
	if err := s.validateSystemBindings(ctx, in.TenantID, in.AllowedSkills, in.MCPToolIDs); err != nil {
		return AgentDTO{}, err
	}
	a := NewBaseAgent(cfg, s.deps.Logger)
	if s.deps.Metrics != nil {
		a = a.WithMetrics(s.deps.Metrics)
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindAgent, id, auditdomain.ChangeOpCreate, in.ActorID, nil, AgentSafeProjection(cfg))
	if err != nil {
		return AgentDTO{}, err
	}
	if err := s.deps.Registry.Register(ctx, a, audit, in.Editors); err != nil {
		s.recordFailure(ctx, id, "create", err)
		return AgentDTO{}, err
	}
	s.deps.Logger.Info("agent created", zap.String("id", id), zap.String("name", in.Name))
	return cfgToDTO(cfg), nil
}

// Get returns the agent's DTO or ErrNotFound.
func (s *AgentService) Get(ctx context.Context, id string) (AgentDTO, error) {
	a, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("agent service get: %w", err)
	}
	if !ok {
		return AgentDTO{}, ErrNotFound
	}
	dto := cfgToDTO(a.GetConfig())
	if s.deps.ResourceEditorRepo != nil {
		editors, listErr := s.deps.ResourceEditorRepo.ListEditors(ctx, reqctx.TenantIDFromContext(ctx), id)
		if listErr != nil {
			return AgentDTO{}, fmt.Errorf("agent service get: list editors: %w", listErr)
		}
		dto.Editors = editors
	}
	return dto, nil
}

// SnapshotRevision returns a deterministic, execution-ready snapshot of the
// currently authorized Agent configuration. Tenant routing remains explicit
// in the call and is enforced by the repository context supplied by wiring.
func (s *AgentService) SnapshotRevision(ctx context.Context, tenantID, id string) (domain.AgentRevision, error) {
	if strings.TrimSpace(tenantID) == "" {
		return domain.AgentRevision{}, fmt.Errorf("agent service: tenant id required")
	}
	a, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return domain.AgentRevision{}, fmt.Errorf("agent service: snapshot revision: %w", err)
	}
	if !ok {
		return domain.AgentRevision{}, ErrNotFound
	}
	cfg := a.GetConfig()
	if cfg.SystemKey == domain.SystemAssistantKey {
		return domain.AgentRevision{}, domain.ErrSystemAssistantRevisionUnsupported
	}
	revision := domain.AgentRevision{
		AgentID: cfg.ID, Type: cfg.Type, SystemPrompt: cfg.SystemPrompt, Model: cfg.LLMModel,
		MaxIterations: cfg.MaxIterations, MemoryScope: cfg.MemoryScope,
		StuckThreshold: cfg.StuckThreshold,
		ModelParameters: domain.ModelParameters{
			MaxContextTokens: cfg.MaxContextTokens,
			Temperature:      cfg.Temperature,
			MaxTokens:        cfg.MaxTokens,
		},
		Bindings: make([]domain.AgentBinding, 0,
			len(cfg.AllowedSkills)+len(cfg.MCPToolIDs)+len(cfg.KnowledgeWorkspaceIDs)),
	}
	if base, ok := a.(*BaseAgent); ok {
		revision.GlobalSystemSuffix = base.GlobalSystemSuffix
		revision.MemoryInjectorRequired = base.MemoryInjector != nil
		revision.RecallMemoryRequired = base.RecallMemoryFn != nil
	}
	for _, id := range cfg.AllowedSkills {
		revision.Bindings = append(revision.Bindings,
			domain.AgentBinding{Kind: domain.AgentBindingSkill, ID: id, Enabled: true})
	}
	for _, id := range cfg.MCPToolIDs {
		revision.Bindings = append(revision.Bindings,
			domain.AgentBinding{Kind: domain.AgentBindingMCP, ID: id, Enabled: true})
	}
	for i, id := range cfg.KnowledgeWorkspaceIDs {
		var name, description string
		if i < len(cfg.KnowledgeWorkspaceNames) {
			name = cfg.KnowledgeWorkspaceNames[i]
		}
		if i < len(cfg.KnowledgeWorkspaceDescriptions) {
			description = cfg.KnowledgeWorkspaceDescriptions[i]
		}
		revision.Bindings = append(revision.Bindings,
			domain.AgentBinding{Kind: domain.AgentBindingKnowledge, ID: id,
				Name: name, Description: description, Enabled: true})
	}
	if _, err := revision.ContentHash(); err != nil {
		return domain.AgentRevision{}, fmt.Errorf("agent service: snapshot revision: %w", err)
	}
	return revision, nil
}

// ExecuteRevision runs an immutable snapshot without changing the mutable
// Agent row or its binding relations.
func (s *AgentService) ExecuteRevision(
	ctx context.Context, revision domain.AgentRevision, req ExecRequest, meta ExecMeta,
) (*AgentResult, int, error) {
	if strings.TrimSpace(meta.TenantID) == "" {
		return nil, 0, fmt.Errorf("agent service: tenant id required")
	}
	if revision.AgentID == domain.SystemAssistantID {
		return nil, 0, domain.ErrSystemAssistantRevisionUnsupported
	}
	if err := revision.Validate(); err != nil {
		return nil, 0, fmt.Errorf("agent service: validate revision: %w", err)
	}
	a, err := s.buildRevisionAgent(revision)
	if err != nil {
		return nil, 0, err
	}
	if s.deps.Metrics != nil {
		a = a.WithMetrics(s.deps.Metrics)
	}
	executionID := uuid.Must(uuid.NewV7()).String()
	_, options, err := s.assembleOptions(ctx, a, req, meta, executionID)
	if err != nil {
		return nil, 0, err
	}
	options = append(options, WithExecutionID(executionID))
	start := time.Now()
	execCtx, cancel := revisionExecutionContext(ctx)
	defer cancel()
	result, err := a.Execute(execCtx, req.Query, options...)
	return result, int(time.Since(start).Milliseconds()), err
}

func revisionExecutionContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithCancel(context.WithoutCancel(ctx))
}

func (s *AgentService) buildRevisionAgent(revision domain.AgentRevision) (*BaseAgent, error) {
	if revision.AgentID == domain.SystemAssistantID {
		return nil, domain.ErrSystemAssistantRevisionUnsupported
	}
	if revision.MemoryInjectorRequired && s.deps.MemoryInjector == nil {
		return nil, fmt.Errorf("agent service: revision requires memory injector")
	}
	if revision.RecallMemoryRequired && s.deps.RecallMemory == nil {
		return nil, fmt.Errorf("agent service: revision requires recall memory")
	}
	a := NewBaseAgent(revisionConfig(revision), s.deps.Logger)
	a.GlobalSystemSuffix = revision.GlobalSystemSuffix
	a.PlatformPromptResolver = s.deps.PlatformPromptResolver
	if revision.MemoryInjectorRequired {
		a.MemoryInjector = s.deps.MemoryInjector
	}
	if revision.RecallMemoryRequired {
		a.RecallMemoryFn = s.deps.RecallMemory
	}
	return a, nil
}

func revisionConfig(revision domain.AgentRevision) *domain.AgentConfig {
	cfg := &domain.AgentConfig{
		ID: revision.AgentID, Type: revision.Type, SystemPrompt: revision.SystemPrompt,
		LLMModel: revision.Model, MaxIterations: revision.MaxIterations,
		MaxContextTokens: revision.ModelParameters.MaxContextTokens,
		Temperature:      revision.ModelParameters.Temperature,
		MaxTokens:        revision.ModelParameters.MaxTokens,
		MemoryScope:      revision.MemoryScope,
		StuckThreshold:   revision.StuckThreshold,
	}
	for _, binding := range revision.Bindings {
		if !binding.Enabled {
			continue
		}
		switch binding.Kind {
		case domain.AgentBindingSkill:
			cfg.AllowedSkills = append(cfg.AllowedSkills, binding.ID)
		case domain.AgentBindingMCP:
			cfg.MCPToolIDs = append(cfg.MCPToolIDs, binding.ID)
		case domain.AgentBindingKnowledge:
			cfg.KnowledgeWorkspaceIDs = append(cfg.KnowledgeWorkspaceIDs, binding.ID)
			cfg.KnowledgeWorkspaceNames = append(cfg.KnowledgeWorkspaceNames, binding.Name)
			cfg.KnowledgeWorkspaceDescriptions = append(cfg.KnowledgeWorkspaceDescriptions, binding.Description)
		}
	}
	return cfg
}

// List returns all agents in the tenant schema.
func (s *AgentService) List(ctx context.Context) ([]AgentDTO, error) {
	agents, err := s.deps.Registry.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("agent service list: %w", err)
	}
	out := make([]AgentDTO, 0, len(agents))
	for _, a := range agents {
		out = append(out, cfgToDTO(a.GetConfig()))
	}
	return out, nil
}

func (s *AgentService) GetSystemAssistantSettings(ctx context.Context) (SystemAssistantSettings, error) {
	a, found, err := s.deps.Registry.GetSystemAssistant(ctx)
	if err != nil {
		return SystemAssistantSettings{}, fmt.Errorf("agent service get system assistant settings: %w", err)
	}
	if !found {
		return SystemAssistantSettings{}, ErrNotFound
	}
	return s.systemAssistantSettings(ctx, a)
}

func (s *AgentService) systemAssistantSettings(ctx context.Context, a Agent) (SystemAssistantSettings, error) {
	cfg := a.GetConfig()
	tenantID := reqctx.TenantIDFromContext(ctx)
	if tenantID == "" {
		return SystemAssistantSettings{}, fmt.Errorf("agent service get system assistant settings: tenant id required")
	}
	models, err := s.listTenantChatModels(ctx, tenantID)
	if err != nil {
		return SystemAssistantSettings{}, fmt.Errorf("agent service list tenant models: %w", err)
	}
	settings := SystemAssistantSettings{
		AgentID: cfg.ID, Model: cfg.LLMModel, AvailableModels: append([]string(nil), models...),
	}
	if strings.TrimSpace(cfg.LLMModel) == "" {
		return settings, nil
	}
	if s.deps.TenantModelValidator == nil {
		return SystemAssistantSettings{}, fmt.Errorf("agent service validate system assistant model: validator unavailable")
	}
	if err := s.deps.TenantModelValidator.ValidateTenantChatModel(ctx, tenantID, cfg.LLMModel); err != nil {
		if errors.Is(err, domain.ErrAssistantModelUnavailable) ||
			errors.Is(err, domain.ErrInvalidSystemAssistantModel) {
			return settings, nil
		}
		return SystemAssistantSettings{}, fmt.Errorf("agent service validate system assistant model: %w", err)
	}
	settings.Ready = true
	return settings, nil
}

func (s *AgentService) UpdateSystemAssistantModel(ctx context.Context, model string, actorID string) (SystemAssistantSettings, error) {
	model, tenantID, err := s.validateSystemAssistantModel(ctx, model)
	if err != nil {
		return SystemAssistantSettings{}, err
	}
	models, err := s.listTenantChatModels(ctx, tenantID)
	if err != nil {
		return SystemAssistantSettings{}, err
	}
	existing, found, err := s.deps.Registry.GetSystemAssistant(ctx)
	if err != nil {
		return SystemAssistantSettings{}, fmt.Errorf("agent service update system assistant model: %w", err)
	}
	if !found {
		return SystemAssistantSettings{}, ErrNotFound
	}
	existingCfg := existing.GetConfig()
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindAgent, existingCfg.ID, auditdomain.ChangeOpUpdate, actorID,
		AgentSafeProjection(existingCfg), nil)
	if err != nil {
		return SystemAssistantSettings{}, err
	}
	a, err := s.deps.Registry.UpdateSystemAssistantModel(ctx, model, existingCfg.MemoryScope, existingCfg.MaxIterations, existingCfg.MaxContextTokens, audit)
	if err != nil {
		return SystemAssistantSettings{}, fmt.Errorf("agent service update system assistant model: %w", err)
	}
	cfg := a.GetConfig()
	return SystemAssistantSettings{
		AgentID: cfg.ID, Model: cfg.LLMModel,
		Ready: cfg.LLMModel == model, AvailableModels: models,
	}, nil
}

func (s *AgentService) validateSystemAssistantModel(ctx context.Context, model string) (string, string, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "", "", domain.ErrInvalidSystemAssistantModel
	}
	if s.deps.TenantModelValidator == nil {
		return "", "", fmt.Errorf("agent service validate system assistant model: validator unavailable")
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	if tenantID == "" {
		return "", "", fmt.Errorf("agent service validate system assistant model: tenant id required")
	}
	if err := s.deps.TenantModelValidator.ValidateTenantChatModel(ctx, tenantID, model); err != nil {
		if errors.Is(err, domain.ErrAssistantModelUnavailable) ||
			errors.Is(err, domain.ErrInvalidSystemAssistantModel) {
			return "", "", domain.ErrInvalidSystemAssistantModel
		}
		return "", "", fmt.Errorf("agent service validate system assistant model: %w", err)
	}
	return model, tenantID, nil
}

func (s *AgentService) listTenantChatModels(ctx context.Context, tenantID string) ([]string, error) {
	if s.deps.TenantModelCatalog == nil {
		return nil, fmt.Errorf("agent service list tenant models: catalog unavailable")
	}
	models, err := s.deps.TenantModelCatalog.ListTenantChatModels(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("agent service list tenant models: %w", err)
	}
	return append([]string(nil), models...), nil
}

// immutable post-create — callers cannot change it through Update.
func (s *AgentService) Update(ctx context.Context, id string, in UpdateAgentInput) (AgentDTO, error) {
	existing, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("agent service update: %w", err)
	}
	if !ok {
		return AgentDTO{}, ErrNotFound
	}
	if existing.GetConfig().SystemKey != "" {
		return s.updateSystemAssistant(ctx, existing.GetConfig(), in)
	}
	// 委托开关 *bool 语义:缺省(nil)继承已存值,显式 false 才关闭。Update 是
	// 全量列 UPDATE,不在此合并会把缺省字段误写成 false,覆盖管理员的显式开启。
	in.DelegateEnabled = resolveDelegateEnabled(existing.GetConfig().DelegateEnabled, in.DelegateEnabled)
	// 委托深度/默认步数 0=unset：缺省继承已存值，防止无关编辑（如只改
	// system prompt）把管理员配置的深度/步数静默打回运行时默认。
	in.DelegateMaxDepth = resolveDelegateInt(existing.GetConfig().DelegateMaxDepth, in.DelegateMaxDepth)
	in.DelegateDefaultMaxSteps = resolveDelegateInt(existing.GetConfig().DelegateDefaultMaxSteps, in.DelegateDefaultMaxSteps)
	editorActor, err := s.resolveUpdateEditorActor(ctx, in.ActorID, id, existing.GetConfig().CreatedBy)
	if err != nil {
		return AgentDTO{}, err
	}
	// Promote (ReplaceParameters) 全量 replace 会清空 agents.parameters JSONB。
	// memory.* 资源参数不属于 evaluation 候选空间,write-back patch 不携带它们,
	// 故把存量值合并进覆盖集,防止 optimizer 写回把已配置的记忆参数抹掉。
	in.Parameters = mergeParamsForReplace(existing.GetConfig().MemoryParameters, in.Parameters, in.ReplaceParameters)
	cfg, err := s.buildUpdateConfig(ctx, id, in)
	if err != nil {
		return AgentDTO{}, err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindAgent, id, auditdomain.ChangeOpUpdate, in.ActorID,
		AgentSafeProjection(existing.GetConfig()), AgentSafeProjection(cfg))
	if err != nil {
		return AgentDTO{}, err
	}
	if err := s.deps.Registry.Update(ctx, cfg, audit, editorActor, in.ReplaceParameters); err != nil {
		s.recordFailure(ctx, id, "update", err)
		return AgentDTO{}, err
	}
	s.deps.Logger.Info("agent updated", zap.String("id", id), zap.String("name", in.Name))
	// 回读而非返回内存 DTO:API 断言必须以 DB 为准(防假绿),
	// 同时证明采样参数(agents.parameters JSONB)确实落库并反序列化回来。
	fresh, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("agent service update: re-read: %w", err)
	}
	if !ok {
		return AgentDTO{}, ErrNotFound
	}
	return cfgToDTO(fresh.GetConfig()), nil
}

// resolveDelegateEnabled 归一化 Update 委托开关的 *bool 缺省语义：nil（请求缺省
// 字段）继承已存值，非 nil（含显式 false）直接采用。Update 走全量列 UPDATE，缺省
// 必须落到已存值，否则会把管理员显式开启的委托误写回 false。
func resolveDelegateEnabled(existing bool, in *bool) *bool {
	if in != nil {
		return in
	}
	return &existing
}

// resolveDelegateInt 归一化 Update 委托数值参数的 0=unset 缺省语义：0（请求缺省
// 字段）继承已存值，非 0 直接采用。与 resolveDelegateEnabled 的 *bool 语义一致，
// 防止无关编辑把管理员配置的委托深度/默认步数静默重置为运行时默认。
func resolveDelegateInt(existing, in int) int {
	if in != 0 {
		return in
	}
	return existing
}

// recordFailure 旁路记录一次失败的 agent 创建/更新（best-effort）。
// 记录失败仅 WARN，不改变主流程错误。
func (s *AgentService) recordFailure(ctx context.Context, id, op string, err error) {
	if s.deps.FailureAudit == nil {
		return
	}
	if recordErr := s.deps.FailureAudit.Record(ctx, auditport.ResourceFailure{
		ResourceKind: auditdomain.ResourceKindAgent,
		ResourceID:   id,
		Operation:    op,
		ErrorCode:    auditport.ClassifyFailure(err),
	}); recordErr != nil {
		s.deps.Logger.Warn("failed to record agent failure audit",
			zap.String("agent_id", id),
			zap.String("op", op),
			zap.Error(recordErr))
	}
}

// buildUpdateConfig validates the sampling parameters and assembles the
// domain config from the wire input, deriving max context tokens when unset.
func (s *AgentService) buildUpdateConfig(ctx context.Context, id string, in UpdateAgentInput) (*domain.AgentConfig, error) {
	// Parameters map keys take precedence over the top-level sampling fields
	// (only present keys overwrite); validation runs on the merged result.
	temperature, maxTokens, reasoningEffort := applyParameterOverrides(in)
	if err := s.validateSamplingParams(ctx, temperature, maxTokens, reasoningEffort); err != nil {
		return nil, err
	}
	if err := validateAgentMaxIterations(in.MaxIterations); err != nil {
		return nil, err
	}
	memoryParams, err := s.validateAndExtractMemoryParameters(ctx, in.Parameters)
	if err != nil {
		return nil, err
	}
	skills := in.AllowedSkills
	if skills == nil {
		skills = []string{}
	}
	cfg := &domain.AgentConfig{
		ID:                      id,
		Name:                    in.Name,
		Type:                    parseAgentTypeWire(in.Type),
		Description:             in.Description,
		SystemPrompt:            in.SystemPrompt,
		LLMModel:                in.LLMModel,
		MaxIterations:           in.MaxIterations,
		MaxContextTokens:        in.MaxContextTokens, // 0 = 未配置，执行时两阶段解析
		Temperature:             temperature,
		ReasoningEffort:         reasoningEffort,
		MaxTokens:               maxTokens,
		AllowedSkills:           skills,
		MCPToolIDs:              in.MCPToolIDs,
		KnowledgeWorkspaceIDs:   in.KnowledgeWorkspaceIDs,
		MemoryScope:             in.MemoryScope,
		DelegateEnabled:         *in.DelegateEnabled, // Update 已把 nil 解析为已存值
		DelegateMaxDepth:        in.DelegateMaxDepth,
		DelegateDefaultMaxSteps: in.DelegateDefaultMaxSteps,
		MemoryParameters:        memoryParams,
	}
	if err := s.validateWorkspaceBindings(ctx, reqctx.TenantIDFromContext(ctx), in.KnowledgeWorkspaceIDs); err != nil {
		return nil, err
	}
	if err := s.validateSystemBindings(ctx, reqctx.TenantIDFromContext(ctx), in.AllowedSkills, in.MCPToolIDs); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validateWorkspaceBindings fails closed (D10): an un-wired validator or an
// unknown workspace name rejects the binding. Empty workspace lists pass
// trivially — no bindings to verify. GatedSelfModify inherits this check via
// s.Update → buildUpdateConfig.
func (s *AgentService) validateWorkspaceBindings(ctx context.Context, tenantID string, workspaceIDs []string) error {
	if len(workspaceIDs) == 0 {
		return nil
	}
	if s.deps.WorkspaceBindingValidator == nil {
		return fmt.Errorf("agent: workspace binding validation unavailable (validator not wired)")
	}
	return s.deps.WorkspaceBindingValidator.ValidateWorkspaceBindings(ctx, tenantID, workspaceIDs)
}

// validateSystemBindings 拒绝把系统内置资源挂载到普通 agent(写路径 fail closed)。
// skill 按 builtin: 前缀直接判;MCP tool id 形如 mcp:<server>:<tool>,命中平台托管
// server 即拒绝。畸形/空段 id(如 mcp::tool)维持现状不 reject 且显式跳过 guard
// 查询,避免空段无法命中平台 server 却触发意外 DB 失败路径。workspace 已由
// validateWorkspaceBindings 内部校验 platform_managed 覆盖,不在此重复。系统助手
// (SystemKey != "")更新走 updateSystemAssistant,不经 Create/buildUpdateConfig,
// 本校验天然不拦系统助手挂载。
func (s *AgentService) validateSystemBindings(ctx context.Context, tenantID string, skills, mcpToolIDs []string) error {
	if err := rejectBuiltinSkillBindings(skills); err != nil {
		return err
	}
	return rejectPlatformMCPServerBindings(ctx, tenantID, mcpToolIDs, s.deps.SystemResourceGuard)
}

// rejectBuiltinSkillBindings rejects any builtin: skill id in the requested
// mount set (platform-seeded skills are system-assistant only).
func rejectBuiltinSkillBindings(skills []string) error {
	for _, id := range skills {
		if strings.HasPrefix(id, "builtin:") {
			return fmt.Errorf("agent: skill %q is platform-managed and cannot be bound: %w", id, domain.ErrPlatformManagedSkillBinding)
		}
	}
	return nil
}

// rejectPlatformMCPServerBindings rejects MCP tools whose mcp:<server>:<tool>
// server is platform-managed. Malformed/empty-segment ids (e.g. mcp::tool) are
// kept as-is (they cannot target a platform server) and skip the guard query.
// A nil guard fails closed when non-empty bindings are present.
func rejectPlatformMCPServerBindings(ctx context.Context, tenantID string, mcpToolIDs []string, guard port.SystemResourceGuard) error {
	if guard == nil {
		if len(mcpToolIDs) == 0 {
			return nil
		}
		return fmt.Errorf("agent: MCP binding validation unavailable (system resource guard not wired)")
	}
	for _, toolID := range mcpToolIDs {
		parts := strings.Split(toolID, ":")
		if len(parts) != 3 || parts[0] != "mcp" || parts[1] == "" || parts[2] == "" {
			continue
		}
		managed, err := guard.IsPlatformManagedMCPServer(ctx, tenantID, parts[1])
		if err != nil {
			return fmt.Errorf("agent: MCP binding validation for server %q: %w", parts[1], err)
		}
		if managed {
			return fmt.Errorf("agent: MCP server %q is platform-managed and cannot be bound: %w", parts[1], domain.ErrPlatformManagedMCPServerBinding)
		}
	}
	return nil
}

// sanitizeRuntimeBindings 对非系统 agent 在装配前清除平台内置资源绑定
// (in-place mutate AgentConfig)。BaseAgent 内嵌 *AgentConfig,本方法在
// application 包内可直接持 a.mu 锁 mutate —— 与 snapshotExecutionConfig
// (:349 读 KnowledgeWorkspaceNames/Descriptions)共用同一把锁串行,并发
// Execute 无数据竞争;mutate 必达 snapshot 及 assembleOptions 内全部读点
// (buildExtraToolsChecked / knowledgeAssignments / ToolExecutionFn 闭包 /
// RAG gate),隔离无需额外接线。
//
// 返回被剔除的 platform workspace name 集,供 RAG 闭包再交集(E4 兜底,防
// mutate 漏掉的任何路径仍被实时检索)。零绑定 agent 短路,不发批量查询;
// guard 为 nil 或批量查询失败且无缓存时 fail closed(禁止默认放行)。
func (s *AgentService) sanitizeRuntimeBindings(
	ctx context.Context, tenantID string, a Agent,
) ([]string, error) {
	cfg := a.GetConfig()
	if len(cfg.AllowedSkills) == 0 && len(cfg.MCPToolIDs) == 0 && len(cfg.KnowledgeWorkspaceIDs) == 0 {
		return nil, nil
	}
	if s.deps.SystemResourceGuard == nil {
		return nil, fmt.Errorf("agent: runtime binding sanitization unavailable (system resource guard not wired)")
	}
	platformMCP, err := s.deps.SystemResourceGuard.PlatformManagedMCPServerIDs(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("agent: resolve platform mcp servers: %w", err)
	}
	platformWS, err := s.deps.SystemResourceGuard.PlatformManagedWorkspaceIDs(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("agent: resolve platform workspaces: %w", err)
	}
	platformMCPSet := toSet(platformMCP)
	platformWSSet := toSet(platformWS)

	var removedWSNames []string
	apply := func(c *AgentConfig) {
		changedSkills := false
		c.AllowedSkills, changedSkills = filterBuiltinSkills(c.AllowedSkills)
		changedTools := false
		c.MCPToolIDs, changedTools = filterPlatformMCPTools(c.MCPToolIDs, platformMCPSet)
		// KnowledgeWorkspaceIDs/Names/Descriptions 三组同序(agent_repo 同查询
		// 填充),按索引同步剔除,否则只滤 IDs 会漏 search_knowledge enum。
		changedWS := false
		if len(c.KnowledgeWorkspaceIDs) > 0 {
			changedWS = filterPlatformWorkspaces(c, platformWSSet, &removedWSNames)
		}
		if changedSkills || changedTools || changedWS {
			s.deps.Logger.Warn("agent: runtime sanitized platform-managed bindings",
				zap.String("agent_id", cfg.ID),
				zap.String("tenant_id", tenantID))
		}
	}

	if ba, ok := a.(*BaseAgent); ok {
		ba.mu.Lock()
		defer ba.mu.Unlock()
		apply(ba.AgentConfig)
	} else {
		apply(cfg)
	}
	return removedWSNames, nil
}

// toSet converts an id list into a set for O(1) membership checks.
func toSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// filterBuiltinSkills drops builtin skills (ID prefix builtin:) in place,
// reporting whether any row was removed.
func filterBuiltinSkills(skills []string) ([]string, bool) {
	kept := skills[:0]
	changed := false
	for _, id := range skills {
		if strings.HasPrefix(id, "builtin:") {
			changed = true
			continue
		}
		kept = append(kept, id)
	}
	return kept, changed
}

// filterPlatformMCPTools drops MCP tools whose mcp:<server>:<tool> server is in
// the platform set. Malformed/empty-segment ids (e.g. mcp::tool) are kept as-is
// (they cannot target a platform server), matching the write-path guard.
func filterPlatformMCPTools(tools []string, platformMCPSet map[string]struct{}) ([]string, bool) {
	kept := tools[:0]
	changed := false
	for _, toolID := range tools {
		parts := strings.Split(toolID, ":")
		if len(parts) == 3 && parts[0] == "mcp" && parts[1] != "" && parts[2] != "" {
			if _, hit := platformMCPSet[parts[1]]; hit {
				changed = true
				continue
			}
		}
		kept = append(kept, toolID)
	}
	return kept, changed
}

// filterPlatformWorkspaces drops platform-managed workspaces from the three
// parallel slices (IDs/Names/Descriptions share index order, filled by the same
// repo query), collecting removed names for the RAG closure re-intersection.
func filterPlatformWorkspaces(c *AgentConfig, platformWSSet map[string]struct{}, removedWSNames *[]string) bool {
	keptIDs := c.KnowledgeWorkspaceIDs[:0]
	keptNames := c.KnowledgeWorkspaceNames[:0]
	keptDescs := c.KnowledgeWorkspaceDescriptions[:0]
	changed := false
	for index, id := range c.KnowledgeWorkspaceIDs {
		if _, hit := platformWSSet[id]; hit {
			changed = true
			if index < len(c.KnowledgeWorkspaceNames) {
				*removedWSNames = append(*removedWSNames, c.KnowledgeWorkspaceNames[index])
			}
			continue
		}
		keptIDs = append(keptIDs, id)
		if index < len(c.KnowledgeWorkspaceNames) {
			keptNames = append(keptNames, c.KnowledgeWorkspaceNames[index])
		}
		if index < len(c.KnowledgeWorkspaceDescriptions) {
			keptDescs = append(keptDescs, c.KnowledgeWorkspaceDescriptions[index])
		}
	}
	c.KnowledgeWorkspaceIDs = keptIDs
	c.KnowledgeWorkspaceNames = keptNames
	c.KnowledgeWorkspaceDescriptions = keptDescs
	return changed
}

// applyParameterOverrides merges the declared parameters map onto the
// top-level sampling fields. Only keys present in the map overwrite; map
// values win over the top-level fields. Zero values pass through unchanged
// (0 = unset, the merge pack skips them, so an explicit 0 never clears).
// 压缩五值（提示词/温度/模型/最近轮数/冷却）为平台级参数，不在此合并、不进
// agent 配置。
func applyParameterOverrides(in UpdateAgentInput) (float32, int, string) {
	temperature, maxTokens := in.Temperature, in.MaxTokens
	reasoningEffort := in.ReasoningEffort
	if len(in.Parameters) == 0 {
		return temperature, maxTokens, reasoningEffort
	}
	if v, ok := numericSampleValue(in.Parameters["temperature"]); ok {
		temperature = float32(v)
	}
	if v, ok := numericSampleValue(in.Parameters["max_tokens"]); ok {
		maxTokens = int(v)
	}
	if v, ok := in.Parameters["reasoning_effort"].(string); ok {
		reasoningEffort = v
	}
	return temperature, maxTokens, reasoningEffort
}

// validateAndExtractMemoryParameters pulls the memory.* resource-scope keys
// (dotted form) out of a flat parameters map and validates each present value
// against the registry. Explicit null or numeric 0 values are deletion markers
// for old per-agent overrides. Unknown memory.* keys fail closed so garbage
// never lands in the opaque JSONB. A nil provider (db unavailable) skips
// validation but still extracts, matching the sampling degrade convention.
// Returns nil when no memory keys are present.
func (s *AgentService) validateAndExtractMemoryParameters(ctx context.Context, parameters map[string]any) (map[string]any, error) {
	var out map[string]any
	for k, v := range parameters {
		if !strings.HasPrefix(k, "memory.") {
			continue
		}
		value, err := s.normalizeMemoryParameter(ctx, k, v)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = value
	}
	return out, nil
}

func (s *AgentService) normalizeMemoryParameter(ctx context.Context, key string, value any) (any, error) {
	if value == nil || isZeroMemoryParameter(value) {
		return nil, nil
	}
	if s.deps.ParametersProvider == nil {
		return value, nil
	}
	if err := s.deps.ParametersProvider.ValidateResourceKey(ctx, key, value); err != nil {
		return nil, fmt.Errorf("%w: agent service: validate memory parameter %s: %v",
			domain.ErrInvalidSamplingParameters, key, err)
	}
	return value, nil
}

func isZeroMemoryParameter(v any) bool {
	n, ok := numericSampleValue(v)
	return ok && n == 0
}

// mergeMemoryParameters overlays explicit memory.* keys onto a base set. A
// promote (full-replace) write only carries evaluation sampling keys, so the
// base preserves existing resource params that would otherwise be wiped by
// the JSONB replace; present overlay keys win over the base.
func mergeMemoryParameters(base, overlay map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// mergeParamsForReplace preserves existing memory.* resource params across a
// promote write: the optimizer write-back patch carries only evaluation
// sampling keys, so a full JSONB replace without this merge would wipe
// per-agent memory configuration. Merge-path writes pass through unchanged.
func mergeParamsForReplace(existingMemory, params map[string]any, replace bool) map[string]any {
	if !replace {
		return params
	}
	return mergeMemoryParameters(existingMemory, params)
}

// numericSampleValue coerces a decoded JSON scalar (float64/int) to float64.
// A present but non-numeric value is treated as absent rather than an error:
// the merge path only overwrites keys it can interpret.
func numericSampleValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// resolveUpdateEditorActor decides whether the actor may act as an editor:
// the base matrix pass yields no editor actor; a foreign admin granted as
// editor yields the actor id, re-validated inside the write transaction
// (editorActor) to close check-then-write TOCTOU.
func (s *AgentService) resolveUpdateEditorActor(ctx context.Context, actorID, resourceID, createdBy string) (string, error) {
	baseErr := s.checkOwnership(ctx, actorID, createdBy, nil)
	if baseErr == nil {
		return "", nil
	}
	if s.deps.ResourceEditorRepo == nil {
		return "", baseErr
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	editors, err := s.deps.ResourceEditorRepo.ListEditors(ctx, tenantID, resourceID)
	if err != nil {
		return "", fmt.Errorf("agent service update: list editors: %w", err)
	}
	if err := s.checkOwnership(ctx, actorID, createdBy, editors); err != nil {
		return "", err
	}
	return actorID, nil
}

func (s *AgentService) updateSystemAssistant(ctx context.Context, cfg *domain.AgentConfig, in UpdateAgentInput) (AgentDTO, error) {
	tenantID := reqctx.TenantIDFromContext(ctx)
	if tenantID == "" {
		return AgentDTO{}, fmt.Errorf("update system assistant: tenant id required")
	}
	// 平台助手不参与思考强度配置:前端以 !isSystem 守卫隐藏,这里 fail closed
	// 拒绝任何直调 API 携带的非空 effort,防止经网关对严格端点打 400。
	if in.ReasoningEffort != "" {
		return AgentDTO{}, domain.ErrInvalidSamplingParameters
	}
	model, err := s.resolveSystemAssistantModel(ctx, tenantID, cfg.LLMModel, in.LLMModel)
	if err != nil {
		return AgentDTO{}, err
	}
	maxTokens, err := s.mergeSystemAssistantMaxTokens(ctx, in.MaxTokens, cfg.MaxTokens)
	if err != nil {
		return AgentDTO{}, err
	}
	// 只校验显式传入的非零 in.MaxIterations（B2）：0 = 保留原值，不校验
	// cfg.MaxIterations——防止 PUT-0 对 DB 遗留超界值的系统助手误拒，破坏
	// ComposeSystemAssistantProfile 保留租户预算的契约。
	if err := validateAgentMaxIterations(in.MaxIterations); err != nil {
		return AgentDTO{}, err
	}
	memoryScope := in.MemoryScope
	maxIterations := in.MaxIterations
	if maxIterations <= 0 {
		maxIterations = cfg.MaxIterations
	}
	maxContextTokens := in.MaxContextTokens
	if maxContextTokens <= 0 {
		maxContextTokens = cfg.MaxContextTokens
	}
	memoryParameters, err := s.validateAndExtractMemoryParameters(ctx, in.Parameters)
	if err != nil {
		return AgentDTO{}, err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindAgent, cfg.ID, auditdomain.ChangeOpUpdate, in.ActorID,
		AgentSafeProjection(cfg), nil)
	if err != nil {
		return AgentDTO{}, err
	}
	updated, err := s.deps.Registry.UpdateSystemAssistantAll(
		ctx, model, memoryScope, maxIterations, maxContextTokens, maxTokens, memoryParameters, audit,
	)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("update system assistant: %w", err)
	}
	s.deps.Logger.Info("system assistant updated", zap.String("id", cfg.ID))
	return cfgToDTO(updated.GetConfig()), nil
}

// resolveSystemAssistantModel merges the requested model with the persisted
// value (unset keeps current) and validates only genuine changes, keeping the
// settings-channel sentinel mapping.
func (s *AgentService) resolveSystemAssistantModel(ctx context.Context, tenantID, persisted, requested string) (string, error) {
	model := requested
	if model == "" {
		model = persisted
	}
	if model == persisted {
		return model, nil
	}
	if s.deps.TenantModelValidator == nil {
		return model, nil
	}
	if err := s.deps.TenantModelValidator.ValidateTenantChatModel(ctx, tenantID, model); err != nil {
		if errors.Is(err, domain.ErrAssistantModelUnavailable) ||
			errors.Is(err, domain.ErrInvalidSystemAssistantModel) {
			return "", domain.ErrInvalidSystemAssistantModel
		}
		return "", fmt.Errorf("update system assistant model: %w", err)
	}
	return model, nil
}

// mergeSystemAssistantMaxTokens merges the requested max_tokens with the
// persisted value (0 = keep current) and validates both input and merged
// result. The pre-merge check rejects out-of-bounds PUT values; the post-merge
// check stops a legacy out-of-bounds persisted value from being silently
// rewritten by PUT 0. Both fail closed with ErrInvalidSamplingParameters and
// never persist.
func (s *AgentService) mergeSystemAssistantMaxTokens(ctx context.Context, requested, persisted int) (int, error) {
	if err := s.validateSamplingParams(ctx, 0, requested, ""); err != nil {
		return 0, err
	}
	maxTokens := requested
	if maxTokens <= 0 {
		maxTokens = persisted
	}
	if maxTokens != 0 {
		if err := s.validateSamplingParams(ctx, 0, maxTokens, ""); err != nil {
			return 0, err
		}
	}
	return maxTokens, nil
}

// Delete removes an agent and cascades deletion to conversations and memories.
func (s *AgentService) Delete(ctx context.Context, tenantID, id, actorID string) error {
	existing, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("delete agent: load managed identity: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	if existing.GetConfig().SystemKey != "" {
		return domain.ErrSystemAssistantManaged
	}
	// Delete stays creator/owner-only: editors do not grant delete rights.
	if err := s.checkOwnership(ctx, actorID, existing.GetConfig().CreatedBy, nil); err != nil {
		return err
	}
	if err := s.deleteSideEffects(ctx, tenantID, id); err != nil {
		return err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindAgent, id, auditdomain.ChangeOpDelete, actorID,
		AgentSafeProjection(existing.GetConfig()), nil)
	if err != nil {
		return err
	}
	if err := s.deps.Registry.Remove(ctx, id, audit); err != nil {
		return err
	}
	s.deps.Logger.Info("agent deleted", zap.String("id", id))
	return nil
}

// SetEditors replaces the granted editor set of an agent resource. Only the
// creator or an owner may manage editors (an editor cannot delegate their own
// right); each editor must hold role admin/owner at write time, enforced
// inside the repository transaction (fail closed). The change is audited in
// the same transaction with before/after projections.
func (s *AgentService) SetEditors(ctx context.Context, id, actorID string, editorIDs []string) error {
	if s.deps.ResourceEditorRepo == nil {
		return fmt.Errorf("agent service set editors: editor repo not wired")
	}
	existing, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("agent service set editors: %w", err)
	}
	if !ok {
		return ErrNotFound
	}
	cfg := existing.GetConfig()
	if cfg.SystemKey != "" {
		return domain.ErrSystemAssistantManaged
	}
	// Editors can never grant delete rights, so SetEditors reuses the
	// creator/owner-only base matrix.
	if err := s.checkOwnership(ctx, actorID, cfg.CreatedBy, nil); err != nil {
		return err
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	before, err := s.deps.ResourceEditorRepo.ListEditors(ctx, tenantID, id)
	if err != nil {
		return fmt.Errorf("agent service set editors: list editors: %w", err)
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindAgent, id, auditdomain.ChangeOpUpdate, actorID,
		AgentSafeProjectionWithEditors(cfg, before), AgentSafeProjectionWithEditors(cfg, editorIDs))
	if err != nil {
		return err
	}
	if err := s.deps.ResourceEditorRepo.ReplaceEditors(ctx, tenantID, id, editorIDs, actorID, audit); err != nil {
		return err
	}
	s.deps.Logger.Info("agent editors updated", zap.String("id", id), zap.Int("count", len(editorIDs)))
	return nil
}

// deleteSideEffects removes per-agent side data before the row deletion.
// Both stores are optional; their failures abort the delete.
func (s *AgentService) deleteSideEffects(ctx context.Context, tenantID, id string) error {
	if s.deps.MemoryCleaner != nil {
		if err := s.deps.MemoryCleaner.ClearAgentMemories(ctx, tenantID, id); err != nil {
			return fmt.Errorf("clear agent memories: %w", err)
		}
	}
	if s.deps.ChatStore != nil {
		if err := s.deps.ChatStore.DeleteByAgent(ctx, tenantID, id); err != nil {
			return fmt.Errorf("delete agent chats: %w", err)
		}
	}
	return nil
}

// parseAgentTypeWire maps the wire-format agent type to the domain enum,
// defaulting to ReActAgent.
func parseAgentTypeWire(t string) domain.AgentType {
	_ = t
	return domain.ReActAgent
}

func cfgToDTO(cfg *domain.AgentConfig) AgentDTO {
	return AgentDTO{
		ID:                      cfg.ID,
		Name:                    cfg.Name,
		Type:                    string(domain.ReActAgent),
		Description:             cfg.Description,
		SystemPrompt:            cfg.SystemPrompt,
		LLMModel:                cfg.LLMModel,
		MaxIterations:           cfg.MaxIterations,
		MaxContextTokens:        cfg.MaxContextTokens,
		Temperature:             cfg.Temperature,
		ReasoningEffort:         cfg.ReasoningEffort,
		MaxTokens:               cfg.MaxTokens,
		AllowedSkills:           cfg.AllowedSkills,
		MCPToolIDs:              cfg.MCPToolIDs,
		KnowledgeWorkspaceIDs:   cfg.KnowledgeWorkspaceIDs,
		CreatedAt:               time.Now().Format(time.RFC3339),
		MemoryScope:             cfg.MemoryScope,
		DelegateEnabled:         cfg.DelegateEnabled,
		DelegateMaxDepth:        cfg.DelegateMaxDepth,
		DelegateDefaultMaxSteps: cfg.DelegateDefaultMaxSteps,
		SystemKey:               cfg.SystemKey,
		IsSystem:                cfg.IsSystem,
		ManagementMode:          cfg.ManagementMode,
		Parameters:              samplingParameterMap(cfg),
	}
}

// samplingParameterMap renders the persisted sampling parameters back to the
// wire object; zero fields are omitted (0 = unset), symmetric with the merge
// pack in the persistence layer.
func samplingParameterMap(cfg *domain.AgentConfig) map[string]any {
	params := map[string]any{}
	if cfg.Temperature != 0 {
		params["temperature"] = cfg.Temperature
	}
	if cfg.MaxTokens != 0 {
		params["max_tokens"] = cfg.MaxTokens
	}
	if cfg.ReasoningEffort != "" {
		params["reasoning_effort"] = cfg.ReasoningEffort
	}
	// memory.* dotted keys round-trip verbatim so the edit form can prefill.
	for k, v := range cfg.MemoryParameters {
		params[k] = v
	}
	return params
}

// ExecRequest is the wire-agnostic execute payload AgentService accepts
// from transport layers.
type ExecRequest struct {
	Query          string
	ConversationID string
	UserID         string
	MaxSteps       int
	Timeout        time.Duration
}

// ExecMeta carries per-call routing metadata sourced from middleware
// (tenant, trace) — never inferred from request body.
type ExecMeta struct {
	TenantID                   string
	TraceID                    string
	Stream                     bool
	ExecutionID                string // optional; generated if empty, used for resume
	EvolutionTrace             EvolutionTraceMetadata
	KnowledgeAssignmentsPinned bool
	PinnedKnowledgeRevisions   map[string]port.KnowledgeRevisionPin
	// DelegateEventCb 在委托子 agent 进入/结束时回调（SSE delegate_status 帧
	// 出口）。仅流式路径由 handler 注入；nil = 不推送委托进度。
	DelegateEventCb func(agentgraph.DelegateEvent)
}

// ExecutionRowDTO is the wire shape emitted by ListExecutions.
type ExecutionRowDTO struct {
	ID            string
	TraceID       string
	AgentID       string
	AgentName     string
	UserID        string
	Status        string
	InputPreview  string
	OutputPreview string
	ErrorMessage  string
	TotalTokens   int
	DurationMs    int
	CreatedAt     string
}

// Execute runs an agent synchronously, persisting an execution record
// on completion. The returned context is for streaming callers — it is
// nil here. Callers receive (*AgentResult, durationMs, error) so the
// transport can render Duration uniformly.
func (s *AgentService) ensureConversation(ctx context.Context, tenantID, agentID, userID string, req *ExecRequest) {
	if req.ConversationID != "" || s.deps.ChatStore == nil {
		return
	}
	createCtx, createCancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	conv, err := s.deps.ChatStore.CreateConversation(createCtx, tenantID, agentID, userID, "新会话")
	createCancel()
	if err != nil {
		s.deps.Logger.Warn("agent: auto-create conversation failed", zap.Error(err))
		return
	}
	req.ConversationID = conv.ID
}

func executionSubject(req ExecRequest, meta ExecMeta) string {
	if req.ConversationID != "" {
		return req.ConversationID
	}
	return meta.TraceID
}

func (s *AgentService) resolveExecutionAgent(
	ctx context.Context,
	current Agent,
	tenantID, agentID, subjectID string,
) (Agent, port.AgentRevisionAssignment, error) {
	if current.GetConfig().SystemKey == domain.SystemAssistantKey || s.deps.AgentRevisionResolver == nil {
		return current, port.AgentRevisionAssignment{}, nil
	}
	assignment, found, err := s.deps.AgentRevisionResolver.ResolveAgentRevision(
		ctx, tenantID, agentID, subjectID,
	)
	if err != nil {
		return nil, port.AgentRevisionAssignment{}, fmt.Errorf("resolve Agent experiment assignment: %w", err)
	}
	if !found {
		return current, port.AgentRevisionAssignment{}, nil
	}
	if assignment.Revision.AgentID != agentID || assignment.RevisionID == "" {
		return nil, port.AgentRevisionAssignment{}, errors.New("resolve Agent experiment assignment: invalid revision")
	}
	resolved, err := s.buildRevisionAgent(assignment.Revision)
	if err != nil {
		return nil, port.AgentRevisionAssignment{}, fmt.Errorf("resolve Agent experiment revision: %w", err)
	}
	if s.deps.Metrics != nil {
		resolved = resolved.WithMetrics(s.deps.Metrics)
	}
	resolved.Name = current.GetConfig().Name
	return resolved, assignment, nil
}

func applyAgentAssignment(meta *ExecMeta, agentID string, assignment port.AgentRevisionAssignment) {
	if assignment.RevisionID == "" {
		return
	}
	if meta.EvolutionTrace.ResourceManifest == nil {
		meta.EvolutionTrace.ResourceManifest = make(map[string]string)
	}
	key := "agent:" + agentID
	meta.EvolutionTrace.ResourceManifest[key] = assignment.RevisionID
	if assignment.ExperimentID == "" {
		return
	}
	if meta.EvolutionTrace.ExperimentAssignments == nil {
		meta.EvolutionTrace.ExperimentAssignments = make(map[string]ExperimentAssignment)
	}
	meta.EvolutionTrace.ExperimentAssignments[key] = ExperimentAssignment{
		ExperimentID: assignment.ExperimentID,
		Variant:      assignment.Variant,
	}
	if meta.EvolutionTrace.ExperimentID == "" {
		meta.EvolutionTrace.ExperimentID = assignment.ExperimentID
		meta.EvolutionTrace.Variant = assignment.Variant
	}
}

func recordExecutionPreparation(
	ctx context.Context, a Agent, req ExecRequest, meta ExecMeta, executionID string,
) {
	cfg := ExecutionConfig{
		TenantID:       meta.TenantID,
		TraceID:        meta.TraceID,
		ExecutionID:    executionID,
		ConversationID: req.ConversationID,
		UserID:         req.UserID,
		EvolutionTrace: meta.EvolutionTrace,
	}
	config := a.GetConfig()
	oteltrace.SpanFromContext(ctx).SetAttributes(
		agentExecutionAttributes(config.ID, config.Name, domain.ReActAgent, cfg, config.MaxContextTokens)...,
	)
}

func recordExecutionPreparationFailure(ctx context.Context, start time.Time, stage string) {
	span := oteltrace.SpanFromContext(ctx)
	span.SetAttributes(
		attribute.String("stratum.error.category", "resource_preparation_failed"),
		attribute.String("stratum.failure.stage", stage),
		attribute.String("opik.metadata.stratum.status", domain.ExecStatusError),
		attribute.String("opik.metadata.stratum.error_category", "resource_preparation_failed"),
		attribute.String("opik.metadata.stratum.failure_stage", stage),
		attribute.Int64("opik.metadata.stratum.duration_ms", time.Since(start).Milliseconds()),
	)
	span.SetStatus(codes.Error, "agent resource preparation failed")
}

func (s *AgentService) Execute(ctx context.Context, agentID string, req ExecRequest, meta ExecMeta) (*AgentResult, int, error) {
	executionID := executionIDOrNew(meta.ExecutionID)
	a, req, meta, _, options, cfg, resuming, terminal, consumedApproval, err := s.prepareAgentExecution(ctx, agentID, req, meta, executionID)
	if err != nil {
		return nil, 0, err
	}
	s.logAgentExecutionDebug("agent.execute", agentID, meta, req)
	execCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	start := time.Now()
	result, err := a.Execute(execCtx, req.Query, options...)
	durationMs := int(time.Since(start).Milliseconds())
	s.recordSystemAssistantExecution(cfg, result, err)
	s.logAgentExecution("agent.execute", agentID, meta, req, durationMs, err)
	if err == nil && result != nil && !cfg.SystemAssistantMode {
		// MemoryBuffer 是执行成功后的旁路异步摄取（Redis buffer，供后台记忆
		// 提取）。答案已交付，缓冲失败不阻断响应——降级决策，但错误必须显式
		// 处理并记录，禁止静默吞掉。
		scope := a.GetConfig().MemoryScope
		s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "user", req.Query)
		s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "assistant", result.Output)
	}
	// 任务结束轨迹反思：与 fact 提取并列的链路，fail-open 显式降级。
	s.enqueueTrajectoryReflection(ctx, meta, req, agentID, a.GetConfig().MemoryScope, executionID, result, cfg.SystemAssistantMode)
	if resuming {
		err = s.finishApprovalResume(ctx, meta.TenantID, executionID, consumedApproval, terminal, err)
	}
	return result, durationMs, err
}

// ExecuteStream runs an agent with token streaming. tokenCb is invoked
// per LLM token; it must be safe for concurrent use with this call's
// goroutine. The returned context carries the per-tenant LLM completer
// (for inner streaming RAG / tool calls) — transport must use it for
// the SSE write loop. cancel() releases the per-call deadline.
func (s *AgentService) ExecuteStream(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, tokenCb func(string),
) (execCtx context.Context, cancel context.CancelFunc, run func() (*AgentResult, int, error), executionID string, err error) {
	// 复用调用方传入的 execution_id（断线续接的恢复键）：非空则沿用同一执行
	// 供 resumeFromCheckpoint 定位 checkpoint，空则生成新 ID（此前无条件新建，
	// 导致流式路径即使带 execution_id 也永远无法续接）。
	executionID = executionIDOrNew(meta.ExecutionID)
	a, req, meta, streamCtx, options, cfg, resuming, terminal, consumedApproval, err := s.prepareAgentExecution(ctx, agentID, req, meta, executionID)
	if err != nil {
		return nil, nil, nil, "", err
	}
	var firstToken sync.Once
	var streamStarted time.Time
	wrappedTokenCb := tokenCb
	if cfg.SystemAssistantMode && s.deps.Metrics != nil {
		wrappedTokenCb = func(token string) {
			firstToken.Do(func() {
				s.deps.Metrics.RecordSystemAssistantTTFT(cfg.SystemAssistantRoleClass,
					cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"], time.Since(streamStarted).Seconds())
			})
			if tokenCb != nil {
				tokenCb(token)
			}
		}
	}
	// 总是追加 token callback：run 闭包延迟执行，回调捕获 cfg 在调用期已确定。
	options = append(options, WithTokenCallback(wrappedTokenCb), WithDelegateEventCallback(meta.DelegateEventCb), WithExecutionID(executionID))

	execCtx, cancel = context.WithCancel(context.WithoutCancel(streamCtx))
	run = func() (*AgentResult, int, error) {
		s.logAgentExecutionDebug("agent.execute_stream", agentID, meta, req)
		start := time.Now()
		streamStarted = start
		res, runErr := a.Execute(execCtx, req.Query, options...)
		durationMs := int(time.Since(start).Milliseconds())
		s.recordSystemAssistantExecution(cfg, res, runErr)
		s.logAgentExecution("agent.execute_stream", agentID, meta, req, durationMs, runErr)
		if runErr == nil && res != nil && !cfg.SystemAssistantMode {
			// 降级决策与 Execute 路径一致：答案已交付，旁路记忆缓冲失败只记日志。
			scope := a.GetConfig().MemoryScope
			s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "user", req.Query)
			s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "assistant", res.Output)
		}
		s.enqueueTrajectoryReflection(ctx, meta, req, agentID, a.GetConfig().MemoryScope, executionID, res, cfg.SystemAssistantMode)
		if resuming {
			// 审批续跑收尾：成功/消费标记推进 checkpoint；失败且未消费批准时
			// 回滚 running→waiting_approval，让 member 可重试同一批准。
			runErr = s.finishApprovalResume(ctx, meta.TenantID, executionID, consumedApproval, terminal, runErr)
		}
		return res, durationMs, runErr
	}
	return execCtx, cancel, run, executionID, nil
}

// prepareAgentExecution 是 Execute/ExecuteStream 的公共准备链：Registry 解析
// Agent → ensure 会话与 init checkpoint → 实验 revision 解析 → 审批续跑抢占并
// 重写 req/meta → assembleOptions → 追加恢复选项。返回重写后的 req/meta、
// streamCtx（仅流式使用，供携带 per-tenant LLM completer）、options、cfg、
// 续跑标记与 consumed 判定。错误已在链内 wrap 到统一语义，调用方原样上抛。
func (s *AgentService) prepareAgentExecution(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, executionID string,
) (a Agent, outReq ExecRequest, outMeta ExecMeta, streamCtx context.Context, options []ExecutionOption, cfg *ExecutionConfig, resuming bool, terminal bool, consumed func() bool, err error) {
	a, ok, err := s.deps.Registry.Get(ctx, agentID)
	if err != nil {
		return nil, req, meta, nil, nil, nil, false, false, nil, fmt.Errorf("get agent: %w", err)
	}
	if !ok {
		return nil, req, meta, nil, nil, nil, false, false, nil, ErrNotFound
	}
	s.ensureConversation(ctx, meta.TenantID, agentID, req.UserID, &req)
	s.ensureInitialCheckpoint(ctx, meta, req, agentID, executionID)
	preparationStart := time.Now()
	recordExecutionPreparation(ctx, a, req, meta, executionID)
	a, assignment, err := s.resolveExecutionAgent(ctx, a, meta.TenantID, agentID, executionSubject(req, meta))
	if err != nil {
		recordExecutionPreparationFailure(ctx, preparationStart, "resolve_agent_revision")
		return nil, req, meta, nil, nil, nil, false, false, nil, fmt.Errorf("resolve revision: %w", err)
	}
	applyAgentAssignment(&meta, agentID, assignment)
	recordExecutionPreparation(ctx, a, req, meta, executionID)
	// 审批续跑：命中 waiting_approval checkpoint 则抢占并把 req/meta 重写为
	// 批准载荷快照；buildApprovalResumeOptions 追加在 assembleOptions 之后。
	approvalPayload, approvalID, resuming, terminal, req, meta, err := s.maybeResumeApproval(ctx, agentID, req, meta, executionID)
	if err != nil {
		recordExecutionPreparationFailure(ctx, preparationStart, "assemble_options")
		return nil, req, meta, nil, nil, nil, false, false, nil, fmt.Errorf("resume approval: %w", err)
	}
	streamCtx, options, err = s.assembleOptions(ctx, a, req, meta, executionID)
	if err != nil {
		s.recordSystemAssistantRequest(a, "unknown", "error")
		recordExecutionPreparationFailure(ctx, preparationStart, "assemble_options")
		return nil, req, meta, nil, nil, nil, false, false, nil, fmt.Errorf("assemble options: %w", err)
	}
	if resuming {
		var resumeOpts []ExecutionOption
		resumeOpts, consumed, err = s.buildApprovalResumeOptions(ctx, meta.TenantID, a, approvalPayload, approvalID, terminal)
		if err != nil {
			recordExecutionPreparationFailure(ctx, preparationStart, "assemble_options")
			return nil, req, meta, nil, nil, nil, false, false, nil, fmt.Errorf("resume approval options: %w", err)
		}
		options = append(options, resumeOpts...)
	}
	options = append(options, WithExecutionID(executionID))
	cfg = &ExecutionConfig{}
	cfg.ApplyOptions(options)
	return a, req, meta, streamCtx, options, cfg, resuming, terminal, consumed, nil
}

// logAgentExecutionDebug 记录执行前 Debug 日志（含会话维度，供恢复排查）。
func (s *AgentService) logAgentExecutionDebug(operation, agentID string, meta ExecMeta, req ExecRequest) {
	s.deps.Logger.Debug(operation,
		zap.String("agent_id", agentID),
		zap.String("trace_id", meta.TraceID),
		zap.String("tenant_id", meta.TenantID),
		zap.String("user_id", req.UserID),
		zap.String("conversation_id", req.ConversationID),
	)
}

// recordSystemAssistantExecution 记录系统助手执行结果指标（fail-open 侧通道）。
func (s *AgentService) recordSystemAssistantExecution(cfg *ExecutionConfig, result *AgentResult, err error) {
	if !cfg.SystemAssistantMode || s.deps.Metrics == nil {
		return
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
	} else if hasFailedAssistantArtifact(result) {
		outcome = "evidence_error"
	}
	s.deps.Metrics.IncSystemAssistantRequest(cfg.SystemAssistantRoleClass,
		cfg.EvolutionTrace.ResourceManifest["system-assistant-profile"], outcome)
}

// logAgentExecution 统一记录执行结果日志（错误 ERROR / 成功 INFO）。
func (s *AgentService) logAgentExecution(operation, agentID string, meta ExecMeta, req ExecRequest, durationMs int, err error) {
	if err != nil {
		s.deps.Logger.Error(operation,
			zap.String("agent_id", agentID),
			zap.String("trace_id", meta.TraceID),
			zap.String("tenant_id", meta.TenantID),
			zap.String("user_id", req.UserID),
			zap.Int("duration_ms", durationMs),
			zap.Error(err),
		)
		return
	}
	s.deps.Logger.Info(operation,
		zap.String("agent_id", agentID),
		zap.String("trace_id", meta.TraceID),
		zap.String("tenant_id", meta.TenantID),
		zap.String("user_id", req.UserID),
		zap.Int("duration_ms", durationMs),
	)
}

// bufferMemoryTurn feeds one turn into the async memory-extraction buffer.
// The answer is already delivered, so a buffering failure is a degradable
// side channel and must never fail the response — but the error is handled
// explicitly and logged instead of being swallowed (no `_ =`).
func (s *AgentService) bufferMemoryTurn(ctx context.Context, meta ExecMeta, req ExecRequest, agentID, scope, role, content string) {
	if s.deps.MemoryBuffer == nil {
		return
	}
	if err := s.deps.MemoryBuffer(ctx, meta.TenantID, req.UserID, agentID, req.ConversationID, scope, role, content); err != nil {
		s.deps.Logger.Warn("agent.memory_buffer_failed",
			zap.String("tenant_id", meta.TenantID),
			zap.String("conversation_id", req.ConversationID),
			zap.String("role", role),
			zap.Error(err))
	}
}

// enqueueTrajectoryReflection 任务结束时把工具调用摘要异步入队轨迹反思
// （与 fact 提取并列的链路 B）。原始 tool steps 不进入记忆：只传截断/脱敏
// 后的参数摘要与错误指纹。失败 fail-open 显式降级，不阻断已交付响应。
func (s *AgentService) enqueueTrajectoryReflection(
	ctx context.Context,
	meta ExecMeta,
	req ExecRequest,
	agentID, scope, executionID string,
	result *domain.AgentResult,
	systemAssistant bool,
) {
	if systemAssistant || s.deps.TrajectoryReflection == nil || result == nil || executionID == "" {
		return
	}
	if len(result.ToolCalls) == 0 {
		return
	}
	calls := make([]port.TrajectoryToolCallVO, 0, len(result.ToolCalls))
	for _, tc := range result.ToolCalls {
		status := domain.ToolTraceStatusSuccess
		var errMsg string
		if tc.Error != nil {
			status = domain.ToolTraceStatusError
			errMsg = tc.Error.Error()
		}
		var argsPreview string
		if tc.Input != nil {
			argsPreview = observability.SafeTracePayload(tc.Input, constants.MemoryReflectionArgsSummaryMaxRunes).Preview
		}
		calls = append(calls, port.TrajectoryToolCallVO{
			ToolName:    tc.ToolName,
			ArgsSummary: argsPreview,
			Status:      status,
			ErrorMsg:    errMsg,
			DurationMS:  tc.Duration.Milliseconds(),
		})
	}
	explicit := containsRememberKeyword(req.Query)
	if err := s.deps.TrajectoryReflection(
		ctx,
		meta.TenantID, req.UserID, agentID, req.ConversationID, scope, executionID,
		req.Query, result.Output, result.TerminatedBy, calls, explicit,
	); err != nil {
		s.deps.Logger.Warn("agent.trajectory_reflection_failed",
			zap.String("tenant_id", meta.TenantID),
			zap.String("conversation_id", req.ConversationID),
			zap.String("execution_id", executionID),
			zap.Error(err))
	}
}

// containsRememberKeyword 检测用户显式"记住"指令，作为反思触发 gate 的
// 显式档位（关键词常量集中管理）。
func containsRememberKeyword(query string) bool {
	for _, kw := range constants.MemoryExplicitRememberKeywords {
		if strings.Contains(query, kw) {
			return true
		}
	}
	return false
}

func (s *AgentService) recordSystemAssistantRequest(a Agent, roleClass, outcome string) {
	if a == nil || a.GetConfig().SystemKey != domain.SystemAssistantKey || s.deps.Metrics == nil {
		return
	}
	version := domain.CurrentSystemAssistantProfileVersion
	if s.deps.Registry != nil {
		if resolved, err := s.deps.Registry.systemAssistantProfileVersion(); err == nil {
			version = resolved
		}
	}
	s.deps.Metrics.IncSystemAssistantRequest(roleClass, version, outcome)
}

func hasFailedAssistantArtifact(result *AgentResult) bool {
	if result == nil {
		return false
	}
	for _, artifact := range result.AssistantToolArtifacts {
		if artifact.Outcome != "success" {
			return true
		}
	}
	return false
}

func boundedAssistantRoleClass(role string) string {
	switch role {
	case "member", "admin", "owner":
		return role
	default:
		return "unknown"
	}
}

func boundedAssistantOutcome(outcome string) string {
	switch outcome {
	case "success", "gap", "error", "evidence_error", "matched":
		return outcome
	default:
		return "unknown"
	}
}

// ListExecutions paginates the per-tenant execution history.
func (s *AgentService) ListExecutions(
	ctx context.Context, tenantID, userID string, page, pageSize int,
) ([]ExecutionRowDTO, int64, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, 0, domain.ErrEvidenceUnavailable
	}
	records, total, err := s.deps.EvidenceProvider.ListExecutions(
		ctx, tenantID, ListOptions{Page: page, PageSize: pageSize, UserID: userID},
	)
	if err != nil {
		return nil, 0, err
	}
	out := make([]ExecutionRowDTO, 0, len(records))
	for _, r := range records {
		out = append(out, ExecutionRowDTO{
			ID:            r.ID,
			TraceID:       r.TraceID,
			AgentID:       r.AgentID,
			AgentName:     r.AgentName,
			UserID:        r.UserID,
			Status:        r.Status,
			InputPreview:  r.InputPreview,
			OutputPreview: r.OutputPreview,
			ErrorMessage:  r.ErrorMessage,
			TotalTokens:   r.TotalTokens,
			DurationMs:    r.DurationMs,
			CreatedAt:     r.CreatedAt.Format(time.RFC3339),
		})
	}
	return out, total, nil
}

func (s *AgentService) ListPendingApprovals(ctx context.Context, tenantID, actorID string) ([]domain.ToolApproval, error) {
	if s.deps.ApprovalService == nil {
		return nil, errors.New("tool approval service not configured")
	}
	// F2：审批列表含 approved 待执行态（pending + approved）；配额计数仍 pending-only。
	return s.deps.ApprovalService.ListActionable(ctx, tenantID, actorID)
}

func (s *AgentService) DecideToolApproval(ctx context.Context, tenantID, id, decision, actor, reason string) error {
	if s.deps.ApprovalService == nil {
		return errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.Decide(ctx, tenantID, id, decision, actor, reason)
}

// CancelToolApproval 取消待批审批：仅发起人本人（或 admin/owner 代撤）可取消，
// pending→cancelled。越权/不存在统一 ErrApprovalNotFound（关闭存在性 oracle，与
// ApprovalDetail 同构）；已决定/已过期返回 ErrApprovalAlreadyDecided/ErrApprovalExpired。
func (s *AgentService) CancelToolApproval(ctx context.Context, tenantID, actor, approvalID string) error {
	if s.deps.ApprovalService == nil {
		return errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.CancelApproval(ctx, tenantID, approvalID, actor)
}

// ListApprovalHistory 分页查询租户审批历史（actor 用于内部角色现查，空值放行返回全部）。
func (s *AgentService) ListApprovalHistory(ctx context.Context, tenantID string, page, pageSize int, actor string) ([]domain.ToolApproval, int, error) {
	if s.deps.ApprovalService == nil {
		return nil, 0, errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.ListHistory(ctx, tenantID, page, pageSize, actor)
}

// ApprovalDetail 返回单个审批的脱敏详情（actor 用于内部角色现查）。
func (s *AgentService) ApprovalDetail(ctx context.Context, tenantID, id, actor string) (ApprovalDetail, error) {
	if s.deps.ApprovalService == nil {
		return ApprovalDetail{}, errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.ApprovalDetail(ctx, tenantID, id, actor)
}

// ExecuteApprovedAction 单次消费已批准审批并把动作交给执行器（D4/D5）。
// actor 现查角色：仅 admin/owner 可执行（fail closed）。
func (s *AgentService) ExecuteApprovedAction(ctx context.Context, tenantID, id, actor string, executor port.ApprovalActionExecutor) (map[string]any, error) {
	if s.deps.ApprovalService == nil {
		return nil, errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.ExecuteApprovedAction(ctx, tenantID, id, actor, executor)
}

// SetApprovalAssignee 指定审批人（actor 需具备 admin/owner 角色，内部现查）。
func (s *AgentService) SetApprovalAssignee(ctx context.Context, tenantID, id, assignee, actor string) error {
	if s.deps.ApprovalService == nil {
		return errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.SetAssignee(ctx, tenantID, id, assignee, actor)
}

func (s *AgentService) ResumeToolApproval(ctx context.Context, tenantID, actor, approvalID string) (*AgentResult, int, error) {
	if s.deps.ApprovalService == nil || s.deps.MCPToolExecutor == nil {
		return nil, 0, errors.New("tool approval runtime not configured")
	}
	// 三段前置步骤（ApprovedPayload → 恢复层校验 → Registry.Get）提成 resumeContext，
	// 控制 ResumeToolApproval 复杂度并固化"D9 校验先于 Registry.Get"的顺序不变量。
	payload, a, err := s.resumeContext(ctx, tenantID, approvalID)
	if err != nil {
		return nil, 0, err
	}
	// 归属 + 抢占（SECURITY-HIGH + B3）：与 executionID 流式续跑共用同一栅栏——
	// 非发起人须 admin/owner 现查角色；AdvanceRunGeneration 分代 CAS + 状态 CAS
	// 只放一个窗口胜出，杜绝按 approvalID 与 executionID 两路并发双 run 覆写
	// checkpoint。checkpoint 已清（cp==nil）时不抢占、跳过归属双保险，仍按批准
	// 载荷重跑（ResumeToolApproval 不依赖 checkpoint 存在）。
	var cp *domain.AgentExecutionCheckpoint
	if s.deps.CheckpointStore != nil {
		cp, err = s.deps.CheckpointStore.GetLatest(ctx, tenantID, payload.ExecutionID)
		if err != nil {
			return nil, 0, fmt.Errorf("resume tool approval: get checkpoint: %w", err)
		}
	}
	if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, cp); err != nil {
		return nil, 0, err
	}
	if cp != nil {
		if err := s.claimApprovalResume(ctx, tenantID, payload.ExecutionID, cp); err != nil {
			return nil, 0, err
		}
	}
	req := ExecRequest{Query: payload.Query, ConversationID: payload.ConversationID, UserID: payload.UserID}
	meta := ExecMeta{TenantID: tenantID, TraceID: payload.TraceID,
		KnowledgeAssignmentsPinned: true, PinnedKnowledgeRevisions: payload.PinnedKnowledgeRevisions}
	_, options, err := s.assembleOptions(ctx, a, req, meta, payload.ExecutionID)
	if err != nil {
		return nil, 0, fmt.Errorf("resume tool approval: assemble options: %w", err)
	}
	// 复用 buildApprovalResumeOptions：恢复键（WithExecutionID + WithApprovalResume）
	// + PinnedSkillRevisions 目录 + 覆盖式 guard（首个与批准一致的调用走 ExecuteApproved
	// CAS 单次消费）。与流式续跑共用，消除重复。
	resumeOpts, consumed, err := s.buildApprovalResumeOptions(ctx, tenantID, a, payload, approvalID, false)
	if err != nil {
		return nil, 0, err
	}
	options = append(options, resumeOpts...)
	start := time.Now()
	result, runErr := a.Execute(context.WithoutCancel(ctx), payload.Query, options...)
	runErr = approvedToolResumeError(consumed(), runErr)
	duration := int(time.Since(start).Milliseconds())
	runErr = completeApprovalResume(ctx, s.deps.CheckpointStore, tenantID, payload.ExecutionID, runErr)
	return result, duration, runErr
}

// resumeContext 组合 ResumeToolApproval 的三段前置步骤：ApprovedPayload →
// 恢复层校验（D9，先于 Registry.Get）→ Registry.Get。not-found 折叠为
// ErrNotFound（与调用方原语义一致）。
func (s *AgentService) resumeContext(ctx context.Context, tenantID, approvalID string) (ToolApprovalPayload, Agent, error) {
	payload, err := s.resumeApprovalPayload(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, nil, err
	}
	if err := s.validateApprovalResume(ctx, tenantID, approvalID, payload); err != nil {
		return ToolApprovalPayload{}, nil, err
	}
	a, ok, err := s.deps.Registry.Get(ctx, payload.AgentID)
	if err != nil {
		return ToolApprovalPayload{}, nil, fmt.Errorf("resume tool approval: get agent: %w", err)
	}
	if !ok {
		return ToolApprovalPayload{}, nil, ErrNotFound
	}
	return payload, a, nil
}

// resumeApprovalPayload 解出可恢复审批载荷并统一失败处理（见 handleApprovedPayloadError）。
func (s *AgentService) resumeApprovalPayload(ctx context.Context, tenantID, approvalID string) (ToolApprovalPayload, error) {
	payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, s.handleApprovedPayloadError(ctx, tenantID, approvalID, err)
	}
	return payload, nil
}

// validateApprovalResume 是断点恢复的恢复层校验（D9）：复用自己的旧授权前，重新
// 核验外部状态。任一校验失败都 fail closed——确认的状态变更终结审批（保留可对账
// 历史），瞬态读取失败拒绝恢复但不销毁审批（避免 DB 抖动永久作废有效授权并写入
// 假审计 reason）。拆成会话/策略两个单职责子校验以控制复杂度。
func (s *AgentService) validateApprovalResume(ctx context.Context, tenantID, approvalID string, payload ToolApprovalPayload) error {
	if err := s.validateApprovalConversation(ctx, tenantID, approvalID, payload); err != nil {
		return err
	}
	return s.validateApprovalPolicy(ctx, tenantID, approvalID, payload)
}

// handleApprovedPayloadError 统一 ApprovedPayload 失败处理：过期是不可逆终态，
// Invalidate(expired) 只是规范化 reason 标记——CAS 失败（已决定/已执行）忽略，
// 真实持久化失败 Join 暴露（不吞错）；其他错误原样传播。
func (s *AgentService) handleApprovedPayloadError(ctx context.Context, tenantID, approvalID string, err error) error {
	if !errors.Is(err, ErrApprovalExpired) {
		return err
	}
	if err := approvalTransitionErr(err, s.deps.ApprovalService.Invalidate(ctx, tenantID, approvalID, "expired")); err != nil {
		return err
	}
	return err
}

// validateApprovalConversation 会话存在性校验：会话确认不存在（ErrNotFound）才
// Void(conversation_deleted)；其他读取错误 fail closed 返回原始错误，不 Void。
func (s *AgentService) validateApprovalConversation(ctx context.Context, tenantID, approvalID string, payload ToolApprovalPayload) error {
	if payload.ConversationID == "" || s.deps.ChatStore == nil {
		return nil
	}
	if _, err := s.deps.ChatStore.GetConversation(ctx, tenantID, payload.ConversationID); err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("resume tool approval: check conversation: %w", err)
		}
		if err := approvalTransitionErr(err, s.deps.ApprovalService.Void(ctx, tenantID, approvalID, "conversation_deleted")); err != nil {
			return err
		}
		return domain.ErrApprovalConversationGone
	}
	return nil
}

// validateApprovalPolicy 策略重查：无法解析（resolver 错误或空风险，镜像
// resolveMCPToolRisk 的 unresolved 语义）→ fail closed 返回错误不 Invalidate；
// 已解析但等级不一致 → Invalidate(policy_changed)。
func (s *AgentService) validateApprovalPolicy(ctx context.Context, tenantID, approvalID string, payload ToolApprovalPayload) error {
	// 策略重查是 MCP-tool 语义：空值视为 mcp_tool（存量兼容），显式非 MCP 审批
	// （evaluation_action/mcp_policy/mcp_server）无 MCP tool risk——用无关的
	// server/tool 名重查可能解析出偶然不同的等级，误 Invalidate 有效审批并写误导
	// 审计 reason。门控镜像 createApprovalCheckpoint。
	if s.deps.MCPToolPolicy == nil || (payload.SubjectKind != "" && payload.SubjectKind != domain.SubjectKindMCPTool) {
		return nil
	}
	risk, riskErr := s.deps.MCPToolPolicy.ResolveMCPToolRisk(ctx, tenantID, payload.ServerID, payload.ToolName)
	if riskErr != nil || risk == "" {
		if riskErr == nil {
			riskErr = errors.New("tool risk unresolved")
		}
		return fmt.Errorf("resume tool approval: resolve policy: %w", riskErr)
	}
	if risk != payload.RiskLevel {
		if err := approvalTransitionErr(fmt.Errorf("tool risk %q changed from %q", risk, payload.RiskLevel),
			s.deps.ApprovalService.Invalidate(ctx, tenantID, approvalID, "policy_changed")); err != nil {
			return err
		}
		return domain.ErrApprovalPolicyChanged
	}
	return nil
}

// approvalTransitionErr 统一恢复层终结动作的 CAS 语义：终态 CAS 失败
// （已执行/已决定，ErrApprovalAlreadyExecuted）按不可逆终态忽略返回 nil；
// 其他错误 Join 保留——恢复层任何终结动作失败都必须暴露，禁止吞错。
func approvalTransitionErr(cause, transitionErr error) error {
	if transitionErr == nil || errors.Is(transitionErr, domain.ErrApprovalAlreadyExecuted) {
		return nil
	}
	return errors.Join(cause, transitionErr)
}

func completeApprovalResume(
	ctx context.Context,
	checkpoints CheckpointStore,
	tenantID, executionID string,
	runErr error,
) error {
	if runErr != nil || checkpoints == nil {
		return runErr
	}
	if err := checkpoints.MarkCompleted(ctx, tenantID, executionID); err != nil {
		return fmt.Errorf("complete approved tool checkpoint: %w", err)
	}
	return nil
}

// ActiveExecution is the refresh-resume view of a conversation's in-flight
// execution, returned by GetActiveExecution for the frontend to reconnect a
// streamed run after a hard refresh (session continuity).
type ActiveExecution struct {
	ExecutionID string `json:"execution_id"`
	AgentID     string `json:"agent_id"`
	Status      string `json:"status"`
	ApprovalID  string `json:"approval_id,omitempty"`
	// ApprovalStatus 仅 waiting_approval 时填充审批行当前状态
	// （pending/approved/rejected/expired/...）：批准后 checkpoint 仍停在
	// waiting_approval，发起人前端需区分"已批准待续跑"与"仍等待"才能自动续跑；
	// 只透出状态字符串，不含任何敏感字段。
	ApprovalStatus string    `json:"approval_status,omitempty"`
	UserQuery      string    `json:"user_query,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GetActiveExecution returns the conversation's fresh in-flight execution for
// the actor, or (nil, nil) when none exists (404-none). Fail-closed ownership
// gate: a member must own the conversation; only admin/owner may read a
// conversation they do not own. Any DB read failure is returned as an error
// (→ 500) and is never folded into the 404-none sentinel — a transient read
// failure must not masquerade as "no active execution" and trigger a fresh
// run (duplicate execution / duplicate approval).
func (s *AgentService) GetActiveExecution(ctx context.Context, tenantID, conversationID, actor string) (*ActiveExecution, error) {
	if s.deps.ChatStore == nil || s.deps.CheckpointStore == nil {
		return nil, nil
	}
	allowed, err := s.resolveActiveExecutionAccess(ctx, tenantID, conversationID, actor)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, nil
	}
	checkpoint, err := s.deps.CheckpointStore.GetLatestActiveByConversation(ctx, tenantID, conversationID)
	if err != nil {
		return nil, fmt.Errorf("get active execution: get checkpoint: %w", err)
	}
	if checkpoint == nil {
		return nil, nil
	}
	out := &ActiveExecution{
		ExecutionID: checkpoint.ExecutionID,
		AgentID:     checkpoint.AgentID,
		Status:      checkpoint.Status,
		UserQuery:   checkpoint.UserQuery,
		UpdatedAt:   checkpoint.UpdatedAt,
	}
	if checkpoint.Status == domain.ExecStatusWaitingApproval {
		s.populateApprovalStatus(ctx, tenantID, approvalIDFromRuntimeState(checkpoint.RuntimeStateJSON), out)
	}
	return out, nil
}

// resolveActiveExecutionAccess 会话归属校验（fail-closed）：member 必须拥有会话；
// admin/owner 可读他人会话。会话不存在或非归属 member 一律返回不放行（404-none
// 哨兵），关闭存在性 oracle——非归属成员无法探测会话是否存在。角色现查（resolver
// 缺失/失败原样上抛，不折叠成 404，防 DB 抖动被误判为"无活跃执行"）。
func (s *AgentService) resolveActiveExecutionAccess(ctx context.Context, tenantID, conversationID, actor string) (bool, error) {
	conv, err := s.deps.ChatStore.GetConversation(ctx, tenantID, conversationID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("get active execution: get conversation: %w", err)
	}
	if conv.UserID == actor {
		return true, nil
	}
	role, err := s.deps.TenantRoleResolver.ResolveTenantRole(ctx, tenantID, actor)
	if err != nil {
		return false, fmt.Errorf("get active execution: resolve role: %w", err)
	}
	return role == "admin" || role == "owner", nil
}

// populateApprovalStatus 读取 waiting_approval 审批行状态字符串供前端轮询，只透出
// 状态字符串、不泄露任何敏感字段。读取失败仅记录（fail-open）：前端保持等待态下轮
// 重试，不影响 active-execution 本身（会话归属校验在调用方已完成）。
func (s *AgentService) populateApprovalStatus(ctx context.Context, tenantID, approvalID string, out *ActiveExecution) {
	if approvalID == "" {
		return
	}
	// ApprovalID 是恢复键的关联标识，ApprovalService 缺位时也照常透出；
	// 状态查询失败仅记录（fail-open）：前端保持等待态下轮重试。
	out.ApprovalID = approvalID
	if s.deps.ApprovalService == nil {
		return
	}
	if st, err := s.deps.ApprovalService.ApprovalStatus(ctx, tenantID, approvalID); err == nil {
		out.ApprovalStatus = st
	} else {
		s.deps.Logger.Warn("agent: read approval status for active execution failed",
			zap.String("approval_id", approvalID),
			zap.Error(err))
	}
}

// ensureInitialCheckpoint records the "running(init)" checkpoint of a brand-new
// execution so the conversation has a discoverable in-flight state before the
// first per-step checkpoint is written. It stores the round's user_query
// (written once, retained by later Upsert ON CONFLICT) so a session whose run
// just started can be resumed verbatim after a refresh. Continuation entries
// (meta.ExecutionID != "") never touch the existing checkpoint. Init-checkpoint
// failure is logged and does not abort the run (fail-open, explicit): the row
// simply does not exist, so nothing can be mis-resumed.
func (s *AgentService) ensureInitialCheckpoint(ctx context.Context, meta ExecMeta, req ExecRequest, agentID, executionID string) {
	if meta.ExecutionID != "" || s.deps.CheckpointStore == nil {
		return
	}
	markCtx, markCancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
	defer markCancel()
	checkpoint := domain.AgentExecutionCheckpoint{
		ExecutionID:    executionID,
		TraceID:        meta.TraceID,
		ConversationID: req.ConversationID,
		AgentID:        agentID,
		UserID:         req.UserID,
		Status:         "running",
		ResumeReason:   "init",
		ExpiresAt:      time.Now().Add(constants.PlanCheckpointTTL),
		UserQuery:      req.Query,
		RunGeneration:  1,
	}
	if err := s.deps.CheckpointStore.Upsert(markCtx, meta.TenantID, checkpoint); err != nil {
		s.deps.Logger.Warn("agent: initial checkpoint failed",
			zap.String("execution_id", executionID),
			zap.Error(err))
	}
}

// approvalIDFromRuntimeState extracts the approval_id stored by
// createApprovalCheckpoint into a waiting_approval checkpoint's runtime state.
func approvalIDFromRuntimeState(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var state struct {
		ApprovalID string `json:"approval_id"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return ""
	}
	return state.ApprovalID
}

// resolveApprovalResume 解析 executionID 对应的 waiting_approval 或 running
// checkpoint 并校验审批续跑资格。返回 (payload, approvalID, checkpoint)；
// checkpoint 为 nil 表示非审批续跑（无恢复键 / 无 checkpoint / 非
// waiting_approval/running / 无审批 ID）。
// SECURITY-HIGH：非发起人（payload.UserID != actor）须为 admin/owner 现查角色，
// 否则 fail-closed 拒绝；审批过期 Invalidate、会话删除 Void、策略变更 Invalidate
// 复用 validateApprovalResume 的恢复层校验；未批准/已作废错误原样上抛（transport
// 据此幂等恢复"等待审批"卡片，不销毁审批）。
func (s *AgentService) resolveApprovalResume(
	ctx context.Context, tenantID, actor, executionID, agentID string,
) (ToolApprovalPayload, string, *domain.AgentExecutionCheckpoint, bool, error) {
	cp, approvalID, err := s.approvalResumeCheckpoint(ctx, tenantID, executionID)
	if err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	if cp == nil {
		return ToolApprovalPayload{}, "", nil, false, nil
	}
	payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
	if err != nil {
		// 终态审批（已拒绝/已取消）：放行续跑，把"审批未通过"当作一次工具执行失败
		// 交给 LLM 收尾——工具不会执行（guard 对终态返回拒绝错误），主链路继续而非卡死。
		return s.resolveApprovalResumeApprovedError(ctx, tenantID, actor, approvalID, agentID, cp, err)
	}
	if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, cp); err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	if err := s.validateApprovalBinding(agentID, payload); err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	if err := s.validateApprovalResume(ctx, tenantID, approvalID, payload); err != nil {
		return ToolApprovalPayload{}, "", nil, false, err
	}
	return payload, approvalID, cp, false, nil
}

// resolveApprovalResumeApprovedError ApprovedPayload 失败分支：先试终态放行
// （cancelled/rejected → terminal=true，主链路继续），再按哨兵映射非终态错误
// （ErrApprovalNotApproved→202 等待、ErrApprovalExpired→410、invalidated→409）。
// 抽离使 resolveApprovalResume 主流程仅保留 checkpoint 定位与 approved 校验，
// 复杂度回到棘轮目标内。
func (s *AgentService) resolveApprovalResumeApprovedError(
	ctx context.Context, tenantID, actor, approvalID, agentID string, cp *domain.AgentExecutionCheckpoint, err error,
) (ToolApprovalPayload, string, *domain.AgentExecutionCheckpoint, bool, error) {
	if terminalPayload, terminal, terr := s.resolveTerminalApprovalResume(ctx, tenantID, actor, approvalID, agentID, cp); terr != nil {
		return ToolApprovalPayload{}, "", nil, false, terr
	} else if terminal {
		return terminalPayload, approvalID, cp, true, nil
	}
	if errors.Is(err, ErrApprovalNotApproved) || errors.Is(err, domain.ErrApprovalInvalidated) {
		return ToolApprovalPayload{}, approvalID, cp, false, err
	}
	return ToolApprovalPayload{}, "", nil, false, s.handleApprovedPayloadError(ctx, tenantID, approvalID, err)
}

// resolveTerminalApprovalResume 终态审批（已拒绝/已取消）续跑判定。TerminalResumePayload
// 按 row.Status 显式枚举——仅 cancelled/rejected 放行，绝不放行 pending（误吞 pending
// 会绕过"审批未通过前必须等待"的门控）；终态行不做过期门控（已过 expires_at 的
// rejected/cancelled 仍放行，否则轮询到终态恰逢过期的竞态会断链）。非终态返回
// (payload, false, nil)，上层照旧走 ApprovedPayload 错误分支（ErrApprovalNotApproved
// →202 等待、ErrApprovalExpired→410、invalidated→409）。校验错误（越权/会话删除/
// binding mismatch）原样上抛。
func (s *AgentService) resolveTerminalApprovalResume(
	ctx context.Context, tenantID, actor, approvalID, agentID string, cp *domain.AgentExecutionCheckpoint,
) (ToolApprovalPayload, bool, error) {
	payload, _, err := s.deps.ApprovalService.TerminalResumePayload(ctx, tenantID, approvalID)
	if err != nil {
		return ToolApprovalPayload{}, false, nil
	}
	if err := s.authorizeApprovalActor(ctx, tenantID, actor, payload, cp); err != nil {
		return ToolApprovalPayload{}, true, err
	}
	if err := s.validateApprovalBinding(agentID, payload); err != nil {
		return ToolApprovalPayload{}, true, err
	}
	// 保留会话校验：终态续跑同样避免向已删除会话写入。
	if err := s.validateApprovalConversation(ctx, tenantID, approvalID, payload); err != nil {
		return ToolApprovalPayload{}, true, err
	}
	return payload, true, nil
}

// approvalResumeCheckpoint 定位 executionID 对应的 waiting_approval 或 running
// checkpoint 并提取 approval_id。H2①：首个续跑抢占后 checkpoint 转 running、但
// 批准尚未消费时刷新，软续跑仍需命中注入批准载荷——running 亦视为审批续跑候选；
// 正常 running 执行无 approval_id（approvalIDFromRuntimeState 为空）不受影响。
// 非审批续跑（无恢复键 / 无 checkpoint / 非 waiting_approval/running / 无审批
// ID）返回 (nil, "", nil)；DB 读取失败原样上抛。
func (s *AgentService) approvalResumeCheckpoint(
	ctx context.Context, tenantID, executionID string,
) (*domain.AgentExecutionCheckpoint, string, error) {
	if executionID == "" || s.deps.CheckpointStore == nil ||
		s.deps.ApprovalService == nil || s.deps.MCPToolExecutor == nil {
		return nil, "", nil
	}
	cp, err := s.deps.CheckpointStore.GetLatest(ctx, tenantID, executionID)
	if err != nil {
		return nil, "", fmt.Errorf("resolve approval resume: get checkpoint: %w", err)
	}
	if cp == nil || (cp.Status != domain.ExecStatusWaitingApproval && cp.Status != "running") {
		return nil, "", nil
	}
	approvalID := approvalIDFromRuntimeState(cp.RuntimeStateJSON)
	if approvalID == "" {
		return nil, "", nil
	}
	return cp, approvalID, nil
}

// validateApprovalBinding URL 上的 agentID 必须与审批载荷 AgentID 一致，防止拿
// A 的审批续跑 B 的执行。
func (s *AgentService) validateApprovalBinding(agentID string, payload ToolApprovalPayload) error {
	// SECURITY-HIGH：URL 指定 agentID 时须与载荷 AgentID 一致；载荷缺 agent_id
	// 视为不匹配（fail-closed），防拿 A 的审批续跑 B 的执行——不再容忍双侧为空放行。
	if agentID != "" && agentID != payload.AgentID {
		return ErrApprovalBindingMismatch
	}
	return nil
}

// authorizeApprovalActor 审批续跑的归属校验（SECURITY-HIGH）：发起人放行且
// checkpoint 归属须与发起人一致（双保险）；非发起人须为 admin/owner（角色现查，
// resolver 缺失/失败 fail-closed，不读 JWT role claim）。
func (s *AgentService) authorizeApprovalActor(
	ctx context.Context, tenantID, actor string, payload ToolApprovalPayload, cp *domain.AgentExecutionCheckpoint,
) error {
	if payload.UserID == actor {
		// cp 为 nil（ResumeToolApproval 且 checkpoint 已清）时无 checkpoint 归属可
		// 校验，跳过该双保险；非 nil 时仍须一致（双保险保持）。
		if cp != nil && cp.UserID != "" && cp.UserID != actor {
			return domain.ErrApprovalRoleDenied
		}
		return nil
	}
	role, err := s.deps.TenantRoleResolver.ResolveTenantRole(ctx, tenantID, actor)
	if err != nil {
		return fmt.Errorf("resolve approval resume: resolve role: %w", err)
	}
	if role != "admin" && role != "owner" {
		return domain.ErrApprovalRoleDenied
	}
	return nil
}

// claimApprovalResume 抢占 waiting_approval checkpoint：先分代 CAS
// （AdvanceRunGeneration，双 tab/双设备只放一个胜出），再把状态 CAS 为 running
// （B3 + SECURITY-MEDIUM-2 的抢占）。任一失败 = 并发续跑已胜出，返回错误由
// transport 映射 409"已在其他窗口执行"。
func (s *AgentService) claimApprovalResume(ctx context.Context, tenantID, executionID string, cp *domain.AgentExecutionCheckpoint) error {
	if err := s.deps.CheckpointStore.AdvanceRunGeneration(ctx, tenantID, executionID, cp.RunGeneration); err != nil {
		return fmt.Errorf("resume approval: %w: another window already resumed execution", err)
	}
	if err := s.deps.CheckpointStore.UpdateStatusFrom(ctx, tenantID, executionID, domain.ExecStatusWaitingApproval, "running"); err != nil {
		return fmt.Errorf("resume approval: claim checkpoint: %w", err)
	}
	return nil
}

// maybeResumeApproval 是 Execute/ExecuteStream 的统一审批续跑入口：命中
// waiting_approval checkpoint 时抢占并把 req/meta 重写为批准载荷快照（query/
// 发起人/会话/知识 pin），供 assembleOptions 使用。调用方须在 assembleOptions
// 后追加 buildApprovalResumeOptions，并在收尾调用 finishApprovalResume。错误
// 原样上抛（越权/过期/策略变/并发抢占失败）。
//
// H2① 软续跑：checkpoint 已为 running（首个续跑抢占后、批准消费前刷新）时不再
// 抢占（分代/状态 CAS 已由首个续跑完成，重复抢占会误报 409），仅注入批准载荷合成
// P1 直接执行；并发窗口由 ExecuteApproved 的 ClaimExecution CAS 保证单次消费。
func (s *AgentService) maybeResumeApproval(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, executionID string,
) (payload ToolApprovalPayload, approvalID string, resuming bool, terminal bool, outReq ExecRequest, outMeta ExecMeta, err error) {
	payload, approvalID, cp, terminal, err := s.resolveApprovalResume(ctx, meta.TenantID, req.UserID, executionID, agentID)
	if err != nil {
		return payload, approvalID, false, false, req, meta, err
	}
	if cp == nil {
		return ToolApprovalPayload{}, "", false, false, req, meta, nil
	}
	if cp.Status == domain.ExecStatusWaitingApproval {
		if err := s.claimApprovalResume(ctx, meta.TenantID, executionID, cp); err != nil {
			return ToolApprovalPayload{}, "", false, false, req, meta, err
		}
	}
	// 重跑以批准载荷为准：发起人/会话/query 必须是审批时快照，否则续跑会写到
	// 别的会话或以错误身份执行。
	req.Query = payload.Query
	req.UserID = payload.UserID
	req.ConversationID = payload.ConversationID
	meta.KnowledgeAssignmentsPinned = true
	meta.PinnedKnowledgeRevisions = payload.PinnedKnowledgeRevisions
	return payload, approvalID, true, terminal, req, meta, nil
}

// buildApprovalResumeOptions 构造审批续跑的执行选项：恢复键（WithExecutionID +
// WithApprovalResume）与覆盖式 guard。guard 对首个与批准一致的调用
// （server/capability/arguments 匹配）注入 approvalID 走 ExecuteApproved 的
// CAS 单次消费；后续不一致调用回退正常授权/审批路径。返回 consumed 判定函数
// （本轮重跑是否真的消费了批准），供收尾判定回滚条件。ResumeToolApproval 与
// 流式续跑共用，消除重复。
func (s *AgentService) buildApprovalResumeOptions(
	ctx context.Context, tenantID string, a Agent, payload ToolApprovalPayload, approvalID string, terminal bool,
) ([]ExecutionOption, func() bool, error) {
	options := make([]ExecutionOption, 0, 3)
	if len(payload.PinnedSkillRevisions) > 0 && s.deps.SkillActivationResolver != nil {
		refs := make([]port.SkillRevisionRef, 0, len(payload.PinnedSkillRevisions))
		for skillID, revisionID := range payload.PinnedSkillRevisions {
			refs = append(refs, port.SkillRevisionRef{SkillID: skillID, RevisionID: revisionID})
		}
		catalog, err := s.deps.SkillActivationResolver.ResolveSkills(ctx, tenantID, refs)
		if err != nil {
			return nil, nil, err
		}
		options = append(options, WithSkillCatalog(catalog))
	}
	consumed := false
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: s.deps.ToolAuthorizer,
		Executor:   s.deps.MCPToolExecutor,
		ExecuteApproved: func(callCtx context.Context, request ToolExecutionRequest) (port.MCPToolResult, error) {
			return s.executeApprovedForResume(callCtx, tenantID, approvalID, request, terminal, &consumed)
		},
	})
	resumeKeyOpts := []ExecutionOption{WithExecutionID(payload.ExecutionID)}
	// 终态模式（已拒绝/已取消）不追加 WithApprovalResume：finalizeReActCheckpoint 按
	// 普通执行收尾写终态，否则 resumeFromCheckpoint 走"恢复 running"路径导致重复
	// P1 注入。approved 模式保留恢复键。
	if !terminal {
		resumeKeyOpts = append(resumeKeyOpts, WithApprovalResume(approvalID))
	}
	resumeKeyOpts = append(resumeKeyOpts,
		// C2a：注入已批准载荷，executeReAct 据此合成 P1 直接执行，不再经 LLM
		// 重新生成参数（修复审批续跑无限循环）。
		WithApprovalResumePayload(payload),
		WithToolExecutionFn(func(callCtx context.Context, request port.ToolExecutionRequest) (any, error) {
			request.TenantID = tenantID
			request.UserID = payload.UserID
			request.AgentID = payload.AgentID
			request.TraceID = payload.TraceID
			request.ExecutionID = payload.ExecutionID
			request.AgentToolIDs = slices.Clone(a.GetConfig().MCPToolIDs)
			request.AgentToolIDs = append(request.AgentToolIDs, agentgraph.StratumDelegateToolName)
			if !consumed && request.Tool.ServerID == payload.ServerID &&
				request.Tool.CapabilityID == payload.ToolName {
				// C2d 加固：canonical digest 比较（与 ApprovedPayload binding 校验同源），
				// 容忍 int/float 表示差异；C2c 合成路径参数同引用平凡命中。
				if d, dErr := CanonicalToolArgumentsDigest(request.Arguments); dErr == nil && d == payload.ArgumentsDigest {
					request.ApprovalID = approvalID
				}
			}
			return guard.Execute(callCtx, request)
		}))
	options = append(options, resumeKeyOpts...)
	return options, func() bool { return consumed }, nil
}

// executeApprovedForResume 审批续跑的 ExecuteApproved 封装：C2d 原子消费判定 +
// 终态模式友好文案。consumed 仅在决定被原子消费（ExecuteApproved 内部 ClaimExecution
// CAS 成功）后置位；claim 失败（并发已消费/过期）与工具未发送/必然失败（行已
// ReleaseExecution 回滚 approved）都不算消费——收尾回滚 waiting_approval，批准仍可
// 再次消费。终态模式（已拒绝/已取消）ExecuteApproved 必然返回 ErrApprovalNotApproved，
// 包装为友好文案（%w 保留哨兵 + 行为约束），LLM 感知后自行收尾。approved 模式保持
// 原样，不影响 C2 幂等/恢复卡片。
func (s *AgentService) executeApprovedForResume(
	callCtx context.Context, tenantID, approvalID string, request ToolExecutionRequest, terminal bool, consumed *bool,
) (port.MCPToolResult, error) {
	result, err := s.deps.ApprovalService.ExecuteApproved(
		callCtx, tenantID, approvalID, request.Tool.ServerID,
		request.Tool.CapabilityID, request.Arguments, s.deps.MCPToolExecutor,
	)
	switch {
	case err == nil:
		*consumed = true
	case errors.Is(err, domain.ErrApprovalAlreadyExecuted):
		*consumed = false
	default:
		var execErr *port.MCPToolExecutionError
		if errors.As(err, &execErr) &&
			(execErr.Outcome == port.ToolExecutionOutcomeNotSent || execErr.Outcome == port.ToolExecutionOutcomeDefiniteFailure) {
			*consumed = false
		} else {
			*consumed = true
		}
	}
	if terminal && err != nil {
		err = fmt.Errorf("%w：工具审批未通过（已被拒绝或取消），工具未执行，请勿重试该工具", err)
	}
	return result, err
}

// finishApprovalResume 审批续跑收尾（SECURITY-MEDIUM-2）：成功 → MarkCompleted；
// 审批等待/断线/并发已消费类错误保留 checkpoint 现状（断线可刷新恢复、新审批已把
// 状态写回 waiting_approval、ErrApprovalAlreadyExecuted 表示并发窗口已有胜者接管，
// 保留 running 交给胜者 MarkCompleted，不回滚不销毁）；真实失败且批准未被本轮消费
// → 回滚 waiting_approval，发起人可重试同一批准；已消费（ExecuteApproved CAS 已把
// approved→executed）→ 写终态 failed 保留对账历史。
func (s *AgentService) finishApprovalResume(ctx context.Context, tenantID, executionID string, consumed func() bool, terminal bool, runErr error) error {
	if s.deps.CheckpointStore == nil {
		return runErr
	}
	// 终态模式（已拒绝/已取消）：checkpoint 终态已由 finalizeReActCheckpoint 写入。
	// 收尾抽到 finishTerminalApprovalResume，见其注释（不回滚/不二次 Terminate）。
	if terminal {
		return finishTerminalApprovalResume(ctx, s.deps.CheckpointStore, tenantID, executionID, runErr)
	}
	if runErr == nil {
		return completeApprovalResume(ctx, s.deps.CheckpointStore, tenantID, executionID, nil)
	}
	if retainRunningError(runErr) || errors.Is(runErr, domain.ErrApprovalAlreadyExecuted) {
		return runErr
	}
	if consumed == nil || !consumed() {
		if rbErr := s.deps.CheckpointStore.UpdateStatusFrom(ctx, tenantID, executionID, "running", domain.ExecStatusWaitingApproval); rbErr != nil {
			return errors.Join(runErr, fmt.Errorf("rollback approval checkpoint: %w", rbErr))
		}
		return runErr
	}
	if termErr := s.deps.CheckpointStore.Terminate(ctx, tenantID, executionID, "failed"); termErr != nil {
		return errors.Join(runErr, fmt.Errorf("terminate approval checkpoint: %w", termErr))
	}
	return runErr
}

// finishTerminalApprovalResume 终态审批（已拒绝/已取消）续跑收尾：checkpoint 终态已由
// finalizeReActCheckpoint 写入（runErr==nil→MarkCompleted、runErr!=nil→Terminate failed）。
// runErr==nil 时 MarkCompleted 幂等（仅对 running 行生效，不查 RowsAffected）；runErr!=nil
// 直接 return——绝不再回滚 waiting_approval（否则前端轮询 cancelled→再续跑→再回滚，死
// 循环），也绝不二次 Terminate（finalizeReActCheckpoint 已写终态，双写报错）。
func finishTerminalApprovalResume(
	ctx context.Context, checkpoints CheckpointStore, tenantID, executionID string, runErr error,
) error {
	if runErr == nil {
		return completeApprovalResume(ctx, checkpoints, tenantID, executionID, nil)
	}
	return runErr
}

// PauseExecution marks a running execution's checkpoint as paused so it can be
// resumed later. No-op when the checkpoint store is not configured.
func (s *AgentService) PauseExecution(ctx context.Context, tenantID, executionID string) error {
	if s.deps.CheckpointStore == nil {
		return fmt.Errorf("pause execution: checkpoint store not configured")
	}
	return s.deps.CheckpointStore.UpdateStatus(ctx, tenantID, executionID, "paused")
}

// ResumeExecution restarts a paused execution from its last checkpoint.
// The executionID must refer to a paused checkpoint.
func (s *AgentService) ResumeExecution(ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, executionID string) (*AgentResult, int, error) {
	if s.deps.CheckpointStore != nil {
		if err := s.deps.CheckpointStore.UpdateStatus(ctx, meta.TenantID, executionID, "running"); err != nil {
			return nil, 0, fmt.Errorf("resume execution: %w", err)
		}
	}
	meta.ExecutionID = executionID
	return s.Execute(ctx, agentID, req, meta)
}

func (s *AgentService) ExecuteSkillScenario(ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, activations []port.SkillActivation) (*AgentResult, int, error) {
	a, ok, err := s.deps.Registry.Get(ctx, agentID)
	if err != nil {
		return nil, 0, fmt.Errorf("execute skill scenario: get agent: %w", err)
	}
	if !ok {
		return nil, 0, ErrNotFound
	}
	executionID := uuid.Must(uuid.NewV7()).String()
	_, options, err := s.assembleOptions(ctx, a, req, meta, executionID)
	if err != nil {
		return nil, 0, fmt.Errorf("execute skill scenario: assemble options: %w", err)
	}
	options = append(options,
		WithExecutionID(executionID),
		WithSkillCatalog(catalogFromActivations(activations)),
		WithActiveSkills(activations),
	)
	start := time.Now()
	result, err := a.Execute(context.WithoutCancel(ctx), req.Query, options...)
	duration := int(time.Since(start).Milliseconds())
	return result, duration, err
}

// catalogFromActivations 从 scenario 固定激活列表构建 run 级 SkillCatalog。
// 空 SkillID 跳过，重复 SkillID 后者覆盖；返回 map 供 WithSkillCatalog 使用。
func catalogFromActivations(activations []port.SkillActivation) map[string]port.SkillActivation {
	catalog := make(map[string]port.SkillActivation, len(activations))
	for _, activation := range activations {
		if activation.SkillID == "" {
			continue
		}
		catalog[activation.SkillID] = activation
	}
	return catalog
}

// assembleOptions builds the ExecutionOption slice and resolves the
// per-tenant CapabilityGateway. When meta.Stream is true, the returned
// ctx carries the per-tenant LLM completer for streaming inner calls.
func (s *AgentService) assembleOptions(
	ctx context.Context, a Agent, req ExecRequest, meta ExecMeta, executionID string,
) (context.Context, []ExecutionOption, error) {
	// 执行时两阶段解析窗口：MaxContextTokens 只存显式值（0 = 未配置），
	// 解析结果随选项进入 agentExecContext 并回填 WindowSource 供 trace。
	window, windowSrc := s.resolveExecutionWindow(
		ctx, meta.TenantID, a.GetConfig().LLMModel, a.GetConfig().MaxContextTokens,
	)
	options := []ExecutionOption{
		WithMaxSteps(a.GetConfig().MaxIterations),
		WithMaxContextTokens(window),
		WithWindowSource(string(windowSrc)),
		// outputReserve 与窗口同一来源链：显式 max_tokens > DB 模型权威 > vendor maxOut > 常量。
		WithOutputReserve(s.resolveOutputReserve(ctx, meta.TenantID, a.GetConfig().LLMModel, a.GetConfig().MaxTokens)),
	}
	if req.MaxSteps > 0 {
		options = append(options, WithMaxSteps(req.MaxSteps))
	}
	if req.Timeout > 0 {
		options = append(options, WithTimeout(req.Timeout))
	}
	isSystemAssistant := a.GetConfig().SystemKey == domain.SystemAssistantKey
	if isSystemAssistant {
		model := strings.TrimSpace(a.GetConfig().LLMModel)
		if model == "" || s.deps.TenantModelValidator == nil {
			return ctx, nil, domain.ErrAssistantModelUnavailable
		}
		if err := s.deps.TenantModelValidator.ValidateTenantChatModel(ctx, meta.TenantID, model); err != nil {
			if errors.Is(err, domain.ErrInvalidSystemAssistantModel) {
				return ctx, nil, domain.ErrAssistantModelUnavailable
			}
			return ctx, nil, fmt.Errorf("assemble system assistant model: %w", err)
		}
	}
	// 平台内置资源仅系统助手可挂载:普通 agent 在装配前清除 builtin 技能、
	// platform MCP server 工具与 platform workspace 绑定(in-place mutate
	// AgentConfig,与 snapshotExecutionConfig 同锁串行,覆盖下方全部读点)。
	// 返回被剔除的 workspace name 集,供 RAG 闭包再交集。系统助手保留全部。
	var removedWSNames []string
	if !isSystemAssistant {
		var sanitizeErr error
		removedWSNames, sanitizeErr = s.sanitizeRuntimeBindings(ctx, meta.TenantID, a)
		if sanitizeErr != nil {
			return ctx, nil, sanitizeErr
		}
	}
	if s.deps.TenantResolver != nil {
		if capGW, ok := s.deps.TenantResolver.Resolve(ctx, meta.TenantID); ok {
			ctx = s.deps.TenantResolver.InjectCompleter(ctx, meta.TenantID)
			type capGWSetter interface {
				SetCapGateway(port.CapabilityGateway)
			}
			if setter, ok := a.(capGWSetter); ok {
				setter.SetCapGateway(capGW)
			}
			if s.deps.HistoryCompactorFactory != nil {
				// 压缩输出预算基于执行时解析窗口，与 agentExecContext 同一来源。
				// 压缩三值（提示词/温度/模型）由 compactor 内部从平台参数
				// 统一解析（唯一来源，所有 agent 一致，无 per-agent 副本）。
				compactionMaxTokens := constants.DynamicCompactionMaxTokens(window)
				if compactor := s.deps.HistoryCompactorFactory(capGW, s.deps.Logger, compactionMaxTokens); compactor != nil {
					type historyCompactorSetter interface {
						SetHistoryCompactor(port.HistoryCompactor)
					}
					if setter, ok := a.(historyCompactorSetter); ok {
						setter.SetHistoryCompactor(compactor)
					}
				}
			}
		}
	}
	s.attachChatStore(a)
	s.attachCheckpointStore(a)
	s.attachCompactionStore(a)

	options = append(options,
		WithTenantID(meta.TenantID),
		WithTraceID(meta.TraceID),
		WithExecutionID(executionID),
		WithUserID(req.UserID),
		WithTracePayloadStore(s.deps.TracePayloadStore),
	)
	if req.ConversationID != "" {
		options = append(options,
			WithConversationID(req.ConversationID),
			WithHistoryWindow(constants.DefaultInitHistoryWindow),
		)
	}
	subjectID := req.ConversationID
	if subjectID == "" {
		subjectID = meta.TraceID
	}
	var extraTools []port.ToolDefinition
	var skillCatalog map[string]port.SkillActivation
	mcpAssignments := make(map[string]port.MCPRevisionAssignment)
	knowledgeAssignments := make(map[string]port.KnowledgeRevisionAssignment)
	var roleClass string
	var authorization domain.DiagnosticAuthorization
	var toolingErr error
	extraTools, skillCatalog, roleClass, toolingErr = s.resolveTooling(
		ctx, meta, req, a, subjectID, isSystemAssistant, &authorization,
	)
	if toolingErr != nil {
		return ctx, nil, toolingErr
	}
	evolutionTrace := meta.EvolutionTrace
	if evolutionTrace.ResourceManifest == nil {
		evolutionTrace.ResourceManifest = make(map[string]string)
	}
	if a.GetConfig().SystemKey == domain.SystemAssistantKey {
		profileVersion, err := s.deps.Registry.systemAssistantProfileVersion()
		if err != nil {
			return ctx, nil, fmt.Errorf("assemble system assistant profile trace: %w", err)
		}
		evolutionTrace.ResourceManifest["system-assistant-profile"] = profileVersion
	}
	if evolutionTrace.ExperimentAssignments == nil {
		evolutionTrace.ExperimentAssignments = make(map[string]ExperimentAssignment)
	}
	if s.deps.MCPRevisionResolver != nil {
		for _, tool := range extraTools {
			if tool.ProviderType != domain.ProviderTypeMCP || tool.ServerID == "" {
				continue
			}
			if _, resolved := mcpAssignments[tool.ServerID]; resolved {
				continue
			}
			assignment, found, err := s.deps.MCPRevisionResolver.ResolveMCPRevision(
				ctx, meta.TenantID, tool.ServerID, subjectID,
			)
			if err != nil {
				return ctx, nil, fmt.Errorf("resolve MCP %s experiment assignment: %w", tool.ServerID, err)
			}
			if !found {
				continue
			}
			if assignment.RevisionID == "" {
				return ctx, nil, fmt.Errorf("resolve MCP %s experiment assignment: revision required", tool.ServerID)
			}
			mcpAssignments[tool.ServerID] = assignment
			key := "mcp:" + tool.ServerID
			evolutionTrace.ResourceManifest[key] = assignment.RevisionID
			if assignment.ExperimentID == "" {
				continue
			}
			evolutionTrace.ExperimentAssignments[key] = ExperimentAssignment{
				ExperimentID: assignment.ExperimentID, Variant: assignment.Variant,
			}
			if evolutionTrace.ExperimentID == "" {
				evolutionTrace.ExperimentID, evolutionTrace.Variant = assignment.ExperimentID, assignment.Variant
			}
		}
	}
	if s.deps.KnowledgeRevisionResolver != nil {
		config := a.GetConfig()
		for index, workspaceID := range config.KnowledgeWorkspaceIDs {
			workspaceName := workspaceID
			if index < len(config.KnowledgeWorkspaceNames) && config.KnowledgeWorkspaceNames[index] != "" {
				workspaceName = config.KnowledgeWorkspaceNames[index]
			}
			var assignment port.KnowledgeRevisionAssignment
			var found bool
			var err error
			if meta.KnowledgeAssignmentsPinned {
				pin, pinned := meta.PinnedKnowledgeRevisions[workspaceName]
				if !pinned {
					continue
				}
				assignment.Revision, err = s.deps.KnowledgeRevisionResolver.LoadKnowledgeRevision(
					ctx, meta.TenantID, workspaceName, pin.RevisionID,
				)
				assignment.ExperimentID, assignment.Variant, found = pin.ExperimentID, pin.Variant, true
			} else {
				assignment, found, err = s.deps.KnowledgeRevisionResolver.ResolveKnowledgeRevision(
					ctx, meta.TenantID, workspaceName, subjectID,
				)
			}
			if err != nil {
				return ctx, nil, fmt.Errorf("resolve Knowledge %s experiment assignment: %w", workspaceName, err)
			}
			if !found {
				continue
			}
			if assignment.Revision.RevisionID == "" || assignment.Revision.WorkspaceName != workspaceName ||
				assignment.ExperimentID == "" || (assignment.Variant != "stable" && assignment.Variant != "canary") {
				return ctx, nil, fmt.Errorf("resolve Knowledge %s experiment assignment: invalid assignment", workspaceName)
			}
			knowledgeAssignments[workspaceName] = assignment
			key := "knowledge:" + workspaceName
			evolutionTrace.ResourceManifest[key] = assignment.Revision.RevisionID
			evolutionTrace.ExperimentAssignments[key] = ExperimentAssignment{
				ExperimentID: assignment.ExperimentID, Variant: assignment.Variant,
			}
			if evolutionTrace.ExperimentID == "" {
				evolutionTrace.ExperimentID, evolutionTrace.Variant = assignment.ExperimentID, assignment.Variant
			}
		}
	}
	for _, skillID := range a.GetConfig().AllowedSkills {
		activation, ok := skillCatalog[skillID]
		if !ok {
			continue
		}
		evolutionTrace.ResourceManifest["skill:"+skillID] = activation.RevisionID
		if activation.ExperimentID == "" {
			continue
		}
		evolutionTrace.ExperimentAssignments["skill:"+skillID] = ExperimentAssignment{
			ExperimentID: activation.ExperimentID,
			Variant:      activation.Variant,
		}
		if evolutionTrace.ExperimentID == "" {
			evolutionTrace.ExperimentID, evolutionTrace.Variant = activation.ExperimentID, activation.Variant
		}
	}
	options = append(options,
		WithExtraTools(extraTools),
		WithSkillCatalog(skillCatalog),
		WithEvolutionTraceMetadata(evolutionTrace),
	)
	if isSystemAssistant {
		profileVersion := evolutionTrace.ResourceManifest["system-assistant-profile"]
		options = append(options, s.systemAssistantExecutionOptions(ctx, meta, req, roleClass, authorization, profileVersion)...)
		return ctx, options, nil
	}
	if s.deps.ToolAuthorizer != nil {
		agentID, userID, conversationID, query := a.GetConfig().ID, req.UserID, req.ConversationID, req.Query
		pinned := make(map[string]string, len(skillCatalog))
		for skillID, activation := range skillCatalog {
			pinned[skillID] = activation.RevisionID
		}
		pinnedKnowledge := make(map[string]port.KnowledgeRevisionPin, len(knowledgeAssignments))
		for workspaceName, assignment := range knowledgeAssignments {
			pinnedKnowledge[workspaceName] = port.KnowledgeRevisionPin{
				RevisionID:   assignment.Revision.RevisionID,
				ExperimentID: assignment.ExperimentID,
				Variant:      assignment.Variant,
			}
		}
		var requestApproval port.ToolApprovalRequester
		if s.deps.ApprovalService != nil {
			approvalService := s.deps.ApprovalService
			requestApproval = func(actx context.Context, request port.ToolApprovalRequest) (string, error) {
				return approvalService.Request(actx, ToolApprovalPayload{
					TenantID: meta.TenantID, ExecutionID: executionID, TraceID: meta.TraceID,
					AgentID: agentID, UserID: userID, ConversationID: conversationID,
					ToolCallID: request.ToolCallID, ServerID: request.ServerID,
					ToolName: request.ToolName, RiskLevel: request.RiskLevel,
					Query: query, Arguments: request.Arguments, PinnedSkillRevisions: pinned,
					PinnedMCPRevisions:       map[string]string{request.ServerID: mcpAssignments[request.ServerID].RevisionID},
					PinnedKnowledgeRevisions: pinnedKnowledge,
				})
			}
		}
		guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
			Authorizer: s.deps.ToolAuthorizer, Executor: s.deps.MCPToolExecutor, RequestApproval: requestApproval,
		})
		options = append(options, WithToolExecutionFn(func(
			callCtx context.Context, request port.ToolExecutionRequest,
		) (any, error) {
			request.TenantID = meta.TenantID
			request.UserID = userID
			request.AgentID = agentID
			request.TraceID = meta.TraceID
			request.ExecutionID = executionID
			request.AgentToolIDs = slices.Clone(a.GetConfig().MCPToolIDs)
			request.AgentToolIDs = append(request.AgentToolIDs, agentgraph.StratumDelegateToolName)
			request.MCPRevisionID = mcpAssignments[request.Tool.ServerID].RevisionID
			return guard.Execute(callCtx, request)
		}))
	}
	if s.deps.RAGSearch != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		options = appendRAGSearchOptions(options, meta.TenantID, s.deps.RAGSearch, knowledgeAssignments, removedWSNames)
	}
	options = applyFactCheckOption(options, s.deps.FactCheck)
	// 普通 agent 同样装配内部工具结果 guard：RAG/recall 工具结果的
	// <untrusted_tool_result> 标记依赖 InternalToolResultGuardFn，漏装配会让
	// 这些工具在 guard 上 fail-closed 报错。无条件装配，对无 RAG agent 无害。
	options = append(options, withInternalToolResultGuard(makeInternalToolResultGuard(NewToolResultGuard())))
	return ctx, s.resolveEffectiveParameters(ctx, a, options), nil
}

// applyFactCheckOption 透传幻觉校验 option（fail-closed：nil/disabled 不注入）。
// judge 与 TopK 等由 wiring 装配；EvidenceFn 在 collectGraphResult 填充。
func applyFactCheckOption(options []ExecutionOption, settings *factcheck.Settings) []ExecutionOption {
	if settings == nil || !settings.Enabled {
		return options
	}
	return append(options, WithFactCheck(settings))
}

// appendRAGSearchOptions wires the plain and (when supported) evidence-capable
// knowledge search variants. Both share the revision/mutable split: revision
// snapshots contribute content only, mutable workspaces fan out through the
// live search provider. platformWorkspaces (runtime-sanitized platform-managed
// workspace names) are intersected out of mutable as a last-resort guard so a
// platform workspace can never reach the live search provider even if some
// path bypassed the assembly-time sanitize.
func appendRAGSearchOptions(
	options []ExecutionOption,
	tenantID string,
	search port.RAGSearchProvider,
	knowledgeAssignments map[string]port.KnowledgeRevisionAssignment,
	platformWorkspaces []string,
) []ExecutionOption {
	platformSet := make(map[string]struct{}, len(platformWorkspaces))
	for _, workspace := range platformWorkspaces {
		platformSet[workspace] = struct{}{}
	}
	options = append(options, WithRAGSearchFn(func(rctx context.Context, workspaces []string, query string, topK int, viewerID string) (string, error) {
		var combined strings.Builder
		mutable := make([]string, 0, len(workspaces))
		for _, workspace := range workspaces {
			if _, hit := platformSet[workspace]; hit {
				continue
			}
			assignment, found := knowledgeAssignments[workspace]
			if !found {
				mutable = append(mutable, workspace)
				continue
			}
			revisionSearch, ok := search.(port.KnowledgeRevisionSearchProvider)
			if !ok {
				return "", errors.New("Knowledge revision search provider not configured")
			}
			content, err := revisionSearch.SearchKnowledgeRevision(rctx, tenantID, assignment.Revision, query, viewerID)
			if err != nil {
				return "", fmt.Errorf("%w: %w", domain.ErrKnowledgeRevisionUnavailable, err)
			}
			combined.WriteString(content)
		}
		if len(mutable) > 0 {
			content, err := search.SearchKnowledge(rctx, tenantID, mutable, query, topK, viewerID)
			if err != nil {
				return "", err
			}
			combined.WriteString(content)
		}
		return combined.String(), nil
	}))
	return appendEvidenceRAGOption(options, search, tenantID, knowledgeAssignments, platformWorkspaces)
}

// appendEvidenceRAGOption wires the evidence-capable search variant when the
// provider supports chunk-level provenance. Same revision/mutable split as
// the plain variant; revision snapshots have no provenance path, so they
// contribute content only. The options slice is returned unchanged when the
// provider lacks evidence support (existing behavior preserved).
// platformWorkspaces are intersected out of mutable (same guard as the plain
// variant, see appendRAGSearchOptions).
func appendEvidenceRAGOption(
	options []ExecutionOption,
	search port.RAGSearchProvider,
	tenantID string,
	knowledgeAssignments map[string]port.KnowledgeRevisionAssignment,
	platformWorkspaces []string,
) []ExecutionOption {
	evidenceProvider, ok := search.(port.RAGSearchEvidenceProvider)
	if !ok {
		return options
	}
	platformSet := make(map[string]struct{}, len(platformWorkspaces))
	for _, workspace := range platformWorkspaces {
		platformSet[workspace] = struct{}{}
	}
	return append(options, WithRAGSearchFnWithEvidence(func(rctx context.Context, workspaces []string, query string, topK int, viewerID string) (port.RAGSearchEvidence, error) {
		var combined strings.Builder
		var sources []port.RAGSearchSource
		var noAnswer *domain.NoAnswerInfo
		mutable := make([]string, 0, len(workspaces))
		for _, workspace := range workspaces {
			if _, hit := platformSet[workspace]; hit {
				continue
			}
			assignment, found := knowledgeAssignments[workspace]
			if !found {
				mutable = append(mutable, workspace)
				continue
			}
			revisionSearch, ok := search.(port.KnowledgeRevisionSearchProvider)
			if !ok {
				return port.RAGSearchEvidence{}, errors.New("Knowledge revision search provider not configured")
			}
			content, err := revisionSearch.SearchKnowledgeRevision(rctx, tenantID, assignment.Revision, query, viewerID)
			if err != nil {
				return port.RAGSearchEvidence{}, fmt.Errorf("%w: %w", domain.ErrKnowledgeRevisionUnavailable, err)
			}
			combined.WriteString(content)
		}
		if len(mutable) > 0 {
			ev, err := evidenceProvider.SearchKnowledgeWithEvidence(rctx, tenantID, mutable, query, topK, viewerID)
			if err != nil {
				return port.RAGSearchEvidence{}, err
			}
			combined.WriteString(ev.Content)
			sources = append(sources, ev.Sources...)
			// 聚合无答案信号：revision 部分无信号，证据部分信号在
			// sources 仍为空时透传（revision 有内容即视为有答案）。
			if len(sources) == 0 && ev.NoAnswer != nil {
				noAnswer = ev.NoAnswer
			}
		}
		return port.RAGSearchEvidence{Content: combined.String(), Sources: sources, NoAnswer: noAnswer}, nil
	}))
}

// resolveTooling resolves the skill/tool catalog for an agent: the system
// assistant profile for system agents (populating authorization), or the
// per-tenant buildExtraToolsChecked for ordinary agents. This mirrors the
// isSystemAssistant branch of assembleOptions so the branch count stays flat.
func (s *AgentService) resolveTooling(
	ctx context.Context, meta ExecMeta, req ExecRequest, a Agent, subjectID string, isSystemAssistant bool,
	authorization *domain.DiagnosticAuthorization,
) (extraTools []port.ToolDefinition, skillCatalog map[string]port.SkillActivation, roleClass string, err error) {
	if isSystemAssistant {
		return s.resolveSystemAssistantTooling(ctx, meta, req, a, subjectID, authorization)
	}
	extraTools, skillCatalog, err = s.buildExtraToolsChecked(
		ctx, meta.TenantID, subjectID, a.GetConfig().MCPToolIDs, a.GetConfig().AllowedSkills,
	)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve experiment resources: %w", err)
	}
	return extraTools, skillCatalog, "unknown", nil
}

// resolveEffectiveParameters resolves the resource-declared execution
// parameters at the assemble point (no caching). Agent-config values flow
// into execution through the snapshotExecutionConfig backfill and the
// provider returns only declared non-unset values — there is no platform
// default fallback. Keys the resource left unset are surfaced with a WARN
// (log + trace attribute) and execution keeps each key's documented default
// (gateway/provider/constant). Resolution errors degrade to unset:
// parameters are an optimization input, not an execution gate.
func (s *AgentService) resolveEffectiveParameters(
	ctx context.Context,
	a Agent,
	options []ExecutionOption,
) []ExecutionOption {
	if s.deps.ParametersProvider == nil {
		return options
	}
	cfg := a.GetConfig()
	declared := map[string]any{
		"agent.temperature":              cfg.Temperature,
		"agent.max_tokens":               cfg.MaxTokens,
		"agent.max_tokens_per_execution": cfg.MaxTokensPerExecution,
	}
	// ReasoningEffort 用 "" 作 unset 哨兵,但 resolver.isUnset 只认零值:空串放
	// 进 declared 会遮蔽平台默认。只有非空才声明,与全局 isUnset 语义解耦。
	if cfg.ReasoningEffort != "" {
		declared["agent.reasoning_effort"] = cfg.ReasoningEffort
	}
	effective, err := s.deps.ParametersProvider.ResolveForResource(ctx, declared)
	if err != nil {
		s.deps.Logger.Warn("agent execute: resolve effective parameters, keeping defaults", zap.Error(err))
		return options
	}
	if unset := unsetResourceKeys(declared, effective); len(unset) > 0 {
		s.deps.Logger.Warn("agent execute: resource parameters unset, documented defaults apply",
			zap.String("agent_id", cfg.ID),
			zap.Strings("unset_keys", unset))
		if span := oteltrace.SpanFromContext(ctx); span.IsRecording() {
			span.SetAttributes(attribute.String("agent.parameters.unset_keys", strings.Join(unset, ",")))
		}
	}
	opts := []ExecutionOption{}
	opts = appendFloatOption(opts, effective, "agent.temperature", WithTemperature)
	opts = appendIntOption(opts, effective, "agent.max_tokens", WithMaxTokens)
	opts = appendStringOption(opts, effective, "agent.reasoning_effort", WithReasoningEffort)
	opts = appendIntOption(opts, effective, "agent.max_tokens_per_execution", WithMaxTokensPerExecution)
	options = append(options, opts...)
	// Platform-scope execution toggles are resolved individually; they are
	// not resource keys so ResolveForResource never returns them.
	if opt := captureParametersOption(ctx, s.deps.ParametersProvider); opt != nil {
		options = append(options, opt)
	}
	return options
}

// unsetResourceKeys returns the declared resource keys that resolved to
// unset (absent from the effective map). Explicit 0 = unset, so a declared
// zero key is reported too — the resource did not configure it and no
// platform/default fallback applies. Sorted for stable log/trace output.
func unsetResourceKeys(declared, effective map[string]any) []string {
	var keys []string
	for key := range declared {
		if _, ok := effective[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// appendIntOption appends the ExecutionOption produced by set when the resolved
// value for key is an int64. One type-assert + build per resolved resource key,
// keeping resolveEffectiveParameters within the complexity budget.
func appendIntOption(opts []ExecutionOption, effective map[string]any, key string, set func(int) ExecutionOption) []ExecutionOption {
	if v, ok := effective[key].(int64); ok {
		opts = append(opts, set(int(v)))
	}
	return opts
}

// appendFloatOption appends the ExecutionOption produced by set when the
// resolved value for key is a float64.
func appendFloatOption(opts []ExecutionOption, effective map[string]any, key string, set func(float32) ExecutionOption) []ExecutionOption {
	if v, ok := effective[key].(float64); ok {
		opts = append(opts, set(float32(v)))
	}
	return opts
}

// appendStringOption appends the ExecutionOption produced by set when the
// resolved value for key is a non-empty string. Empty strings stay unset so a
// ""-keyed resource value never masks the platform default.
func appendStringOption(opts []ExecutionOption, effective map[string]any, key string, set func(string) ExecutionOption) []ExecutionOption {
	if v, ok := effective[key].(string); ok && v != "" {
		opts = append(opts, set(v))
	}
	return opts
}

// captureParametersOption reads the platform-scope execution toggle
// trace.capture_parameters and returns the option recording raw parameter
// values when enabled. Unset, non-bool or resolution errors degrade to
// fingerprint-only traces (parameters are an optimization input, not an
// execution gate).
func captureParametersOption(ctx context.Context, provider port.ParametersProvider) ExecutionOption {
	v, ok, err := provider.Resolve(ctx, "trace.capture_parameters", nil)
	if err != nil || !ok {
		return nil
	}
	enabled, isBool := v.(bool)
	if !isBool || !enabled {
		return nil
	}
	return WithCaptureParameters(true)
}

// attachChatStore wires the configured ChatStore onto the running agent
// when the agent type supports it.
func (s *AgentService) attachChatStore(a Agent) {
	if s.deps.ChatStore == nil {
		return
	}
	type chatStoreSetter interface {
		SetChatStore(ChatStore)
	}
	if setter, ok := a.(chatStoreSetter); ok {
		setter.SetChatStore(s.deps.ChatStore)
	}
}

func (s *AgentService) attachCheckpointStore(a Agent) {
	if s.deps.CheckpointStore != nil {
		type checkpointStoreSetter interface {
			SetCheckpointStore(CheckpointStore)
		}
		if setter, ok := a.(checkpointStoreSetter); ok {
			setter.SetCheckpointStore(s.deps.CheckpointStore)
		}
	}
}

// attachCompactionStore wires the shared compaction summary store onto the
// running agent when the agent type supports it. nil store keeps assembly
// side in the legacy no-reuse behavior.
func (s *AgentService) attachCompactionStore(a Agent) {
	if s.deps.CompactionStore == nil {
		return
	}
	type compactionStoreSetter interface {
		SetCompactionStore(port.CompactionStore)
	}
	if setter, ok := a.(compactionStoreSetter); ok {
		setter.SetCompactionStore(s.deps.CompactionStore)
	}
}

// resolveSystemAssistantTooling authorizes the current member, derives the
// bounded role class, and builds the in-process platform tools plus the skill
// catalog for the system assistant.
func (s *AgentService) resolveSystemAssistantTooling(
	ctx context.Context,
	meta ExecMeta,
	req ExecRequest,
	a Agent,
	subjectID string,
	authorization *domain.DiagnosticAuthorization,
) ([]port.ToolDefinition, map[string]port.SkillActivation, string, error) {
	roleClass := "unknown"
	if s.deps.DiagnosticProvider != nil {
		var authorizeErr error
		*authorization, authorizeErr = s.deps.DiagnosticProvider.Authorize(ctx, domain.DiagnosticRequest{
			TenantID: meta.TenantID, UserID: req.UserID,
			Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent, domain.DiagnosticAreaSkill, domain.DiagnosticAreaMCP, domain.DiagnosticAreaKnowledge, domain.DiagnosticAreaModel},
		})
		if authorizeErr != nil {
			return nil, nil, "", authorizeErr
		}
		roleClass = boundedAssistantRoleClass(authorization.RoleClass)
	}
	extraTools := SystemAssistantToolDefinitions()
	skillCatalog, catalogErr := s.buildSkillCatalog(ctx, meta.TenantID, subjectID, a.GetConfig().AllowedSkills)
	if catalogErr != nil {
		return nil, nil, "", fmt.Errorf("resolve experiment resources: %w", catalogErr)
	}
	return extraTools, skillCatalog, roleClass, nil
}

// systemAssistantExecutionOptions attaches the in-process capability callbacks
// (official docs search, tenant diagnostics, governed proposals) plus the
// bounded result guard for the system assistant execution.
func (s *AgentService) systemAssistantExecutionOptions(
	ctx context.Context,
	meta ExecMeta,
	req ExecRequest,
	roleClass string,
	authorization domain.DiagnosticAuthorization,
	profileVersion string,
) []ExecutionOption {
	var options []ExecutionOption
	if s.deps.OfficialDocsSearch != nil {
		search := s.deps.OfficialDocsSearch
		options = append(options, WithOfficialDocsSearchFn(func(callCtx context.Context, query string) ([]domain.Citation, error) {
			citations, searchErr := search(callCtx, query)
			if s.deps.Metrics != nil {
				outcome := "matched"
				if searchErr != nil {
					outcome = "error"
				}
				s.deps.Metrics.RecordOfficialDocsSearchResults(profileVersion, outcome, len(citations))
			}
			return citations, searchErr
		}))
	}
	if s.deps.DiagnosticProvider != nil {
		provider, authorized := s.deps.DiagnosticProvider, authorization.Request
		options = append(options, WithDiagnosticFn(func(callCtx context.Context, areas []domain.DiagnosticArea) (domain.DiagnosticEvidence, error) {
			request := authorized
			request.Areas = append([]domain.DiagnosticArea(nil), areas...)
			evidence, diagnosticErr := provider.CollectAuthorized(callCtx, request)
			if s.deps.Metrics != nil {
				for _, result := range evidence.AreaResults {
					s.deps.Metrics.RecordSystemAssistantDiagnosticArea(roleClass, string(result.Area), boundedAssistantOutcome(result.Outcome), float64(result.DurationMs)/1000)
				}
				s.deps.Metrics.RecordSystemAssistantEvidenceGaps(roleClass, profileVersion, len(evidence.Gaps))
			}
			return evidence, diagnosticErr
		}))
	}
	guard := NewToolResultGuard()
	if s.deps.ProposalService != nil {
		proposalService := s.deps.ProposalService
		tenantID, actorID, conversationID := meta.TenantID, req.UserID, req.ConversationID
		// D6：admin/owner 提案创建后立即自动确认并应用，一气呵成；member 保持待审提案流。
		autoApply := roleClass == "admin" || roleClass == "owner"
		options = append(options, withProposalCreateFn(func(callCtx context.Context, args map[string]any) (domain.ResourceChangeProposalArtifact, error) {
			kind, operation, resourceID, payload, parseErr := ParseResourceChangeToolArguments(args)
			if parseErr != nil {
				return domain.ResourceChangeProposalArtifact{}, parseErr
			}
			proposal, createErr := proposalService.CreateProposal(callCtx, CreateProposalInput{
				TenantID: tenantID, ConversationID: conversationID, ActorID: actorID,
				Kind: kind, Operation: operation, ResourceID: resourceID, Payload: payload,
			})
			if createErr != nil {
				return domain.ResourceChangeProposalArtifact{}, createErr
			}
			if autoApply {
				applied, applyErr := proposalService.ConfirmAndApply(callCtx, tenantID, proposal.ID, actorID)
				if applyErr != nil {
					// 保留已创建的 proposal artifact：graph 错误分支据此记录
					// proposal ID，避免模型重复创建提案。ConfirmAndApply 失败前
					// 可能已推进状态机（stale/failed/unknown_outcome），回读当前
					// DB 状态避免用创建时的 ready_for_review 快照误导模型；回读
					// 失败（如上下文超时）则退回创建快照。
					current, getErr := proposalService.Get(callCtx, tenantID, actorID, proposal.ID)
					if getErr == nil {
						proposal = current
					}
					created := domain.ResourceChangeProposalArtifact{
						ID: proposal.ID, ResourceKind: proposal.ResourceKind, Operation: proposal.Operation,
						Status: proposal.Status, Summary: proposal.Summary, ExpiresAt: proposal.ExpiresAt,
					}
					return created, fmt.Errorf("auto apply proposal %s: %w", proposal.ID, applyErr)
				}
				proposal = applied
			}
			artifact := domain.ResourceChangeProposalArtifact{
				ID: proposal.ID, ResourceKind: proposal.ResourceKind, Operation: proposal.Operation,
				Status: proposal.Status, Summary: proposal.Summary, ExpiresAt: proposal.ExpiresAt,
			}
			return artifact, nil
		}))
	}
	if s.deps.ResourceChangeApplier != nil {
		applier := s.deps.ResourceChangeApplier
		actorID := req.UserID
		// apply 工具全角色可见（D6）；member 闭包内 fail closed 明确拒绝，
		// 不触达 applier，与 update_system_model 的写路径模式一致。
		options = append(options, withResourceChangeApplyFn(func(callCtx context.Context, args map[string]any) (domain.ApplyResult, error) {
			if roleClass != "admin" && roleClass != "owner" {
				return domain.ApplyResult{}, fmt.Errorf("%w: 需要管理员权限，member 请改用提案工具", domain.ErrProposalForbidden)
			}
			return applier(callCtx, actorID, args)
		}))
	}
	options = s.appendSystemModelToolOptions(options, meta, req, roleClass)
	options = append(options, WithSystemAssistantMode(), withSystemAssistantRoleClass(roleClass),
		withInternalToolResultGuard(makeInternalToolResultGuard(guard)))
	return options
}

// appendSystemModelToolOptions 装配模型工具闭包：list_models 全角色可见；
// update_system_model 写路径在闭包内按 roleClass fail closed，member 明确
// 拒绝且不触达 Registry。提取为独立方法以控制主函数圈复杂度。
func (s *AgentService) appendSystemModelToolOptions(
	options []ExecutionOption, meta ExecMeta, req ExecRequest, roleClass string,
) []ExecutionOption {
	if s.deps.ModelDetailsProvider != nil {
		details := s.deps.ModelDetailsProvider
		options = append(options, WithListModelsFn(func(callCtx context.Context) (map[string]any, error) {
			models, listErr := details.ListTenantModelDetails(callCtx, meta.TenantID)
			if listErr != nil {
				return nil, fmt.Errorf("list tenant models: %w", listErr)
			}
			return map[string]any{"models": models}, nil
		}))
	}
	if s.deps.Registry != nil {
		actorID := req.UserID
		updateModel := func(callCtx context.Context, model string) (map[string]any, error) {
			if roleClass != "admin" && roleClass != "owner" {
				// 写路径 fail closed：member 明确拒绝，不触达 Registry。
				return nil, errors.New("更新平台助手模型需要管理员权限")
			}
			settings, updateErr := s.UpdateSystemAssistantModel(callCtx, model, actorID)
			if updateErr != nil {
				return nil, updateErr
			}
			return map[string]any{
				"model":           settings.Model,
				"ready":           settings.Ready,
				"availableModels": settings.AvailableModels,
			}, nil
		}
		options = append(options, WithUpdateSystemModelFn(updateModel))
	}
	if s.deps.Registry != nil {
		options = append(options, WithListAgentsFn(func(callCtx context.Context) (map[string]any, error) {
			agents, listErr := s.List(callCtx)
			if listErr != nil {
				return nil, fmt.Errorf("list agents: %w", listErr)
			}
			items := make([]map[string]any, 0, len(agents))
			for _, dto := range agents {
				// 复用安全投影：不携带 systemPrompt/systemKey 等敏感字段。
				items = append(items, AgentDTOSafeProjection(dto))
			}
			return map[string]any{"agents": items}, nil
		}))
	}
	if s.deps.MCPServerLister != nil {
		lister := s.deps.MCPServerLister
		options = append(options, WithListMCPServersFn(func(callCtx context.Context) (map[string]any, error) {
			servers, listErr := lister.ListMCPServers(callCtx)
			if listErr != nil {
				return nil, fmt.Errorf("list mcp servers: %w", listErr)
			}
			return map[string]any{"servers": servers}, nil
		}))
	}
	return options
}

// buildExtraTools converts MCPToolIDs and AllowedSkills into ToolDefinitions
// for the ReAct loop. Published skills use their tool contract names; legacy
// skills fall back to tenant-scoped names. The returned index maps tool names
// back to skill/version refs for execution routing.
func (s *AgentService) buildExtraTools(
	ctx context.Context,
	tenantID, subjectID string,
	mcpToolIDs, allowedSkills []string,
) ([]port.ToolDefinition, map[string]port.SkillActivation) {
	tools, catalog, _ := s.buildExtraToolsChecked(ctx, tenantID, subjectID, mcpToolIDs, allowedSkills)
	return tools, catalog
}

func (s *AgentService) buildExtraToolsChecked(
	ctx context.Context,
	tenantID, subjectID string,
	mcpToolIDs, allowedSkills []string,
) ([]port.ToolDefinition, map[string]port.SkillActivation, error) {
	tools := s.buildMCPTools(ctx, tenantID, mcpToolIDs)
	catalog, err := s.buildSkillCatalog(ctx, tenantID, subjectID, allowedSkills)
	if err != nil {
		return nil, nil, err
	}
	return tools, catalog, nil
}

func (s *AgentService) buildMCPTools(
	ctx context.Context, tenantID string, mcpToolIDs []string,
) []port.ToolDefinition {
	var tools []port.ToolDefinition
	allowedTools := make(map[string]struct{}, len(mcpToolIDs))
	servers := map[string]struct{}{}
	for _, toolID := range mcpToolIDs {
		parts := strings.Split(toolID, ":")
		if len(parts) == 3 && parts[0] == "mcp" {
			allowedTools[toolID] = struct{}{}
			servers[parts[1]] = struct{}{}
		}
	}
	for serverID := range servers {
		if s.deps.MCPTools == nil {
			continue
		}
		for _, tool := range s.deps.MCPTools.ToolsForServer(ctx, tenantID, serverID) {
			if _, ok := allowedTools[tool.Name]; !ok {
				continue
			}
			tool = normalizeMCPTool(tool, serverID)
			risk, policyResolved := s.resolveMCPToolRisk(ctx, tenantID, serverID, tool.CapabilityID)
			tool.Metadata["risk_level"] = string(risk)
			tool.Metadata["policy_resolved"] = policyResolved
			tools = append(tools, tool)
		}
	}
	if len(mcpToolIDs) > 0 && len(tools) == 0 {
		// 显式暴露工具缺失：agent 绑定了 MCP 工具但最终一个都没暴露。
		// 此前静默 drop，远端故障表现为"模型无 MCP 工具可调用"却无任何日志，
		// 排查困难。这里显式告警（含绑定 ID，便于定位是 catalog 空还是
		// 服务端未返回对应工具）。
		s.deps.Logger.Warn("agent bound MCP tools but none exposed",
			zap.String("tenant_id", tenantID),
			zap.Strings("bound_tool_ids", mcpToolIDs))
	}
	return tools
}

func normalizeMCPTool(tool port.ToolDefinition, serverID string) port.ToolDefinition {
	if tool.ProviderType == "" {
		tool.ProviderType = domain.ProviderTypeMCP
	}
	if tool.ProviderID == "" {
		tool.ProviderID = serverID
	}
	if tool.ServerID == "" {
		tool.ServerID = serverID
	}
	if tool.CapabilityID == "" {
		tool.CapabilityID = tool.Name
	}
	if tool.NodeType == "" {
		tool.NodeType = domain.ObservationTypeMCP
	}
	if tool.Metadata == nil {
		tool.Metadata = make(map[string]any)
	}
	return tool
}

func (s *AgentService) resolveMCPToolRisk(
	ctx context.Context, tenantID, serverID, capabilityID string,
) (port.ToolRiskLevel, bool) {
	risk := port.ToolRiskUnclassified
	resolved := false
	if s.deps.MCPToolPolicy == nil {
		return risk, resolved
	}
	policyRisk, err := s.deps.MCPToolPolicy.ResolveMCPToolRisk(ctx, tenantID, serverID, capabilityID)
	if err != nil || policyRisk == "" {
		return risk, resolved
	}
	return stricterToolRisk(risk, policyRisk), true
}

func (s *AgentService) buildSkillCatalog(
	ctx context.Context, tenantID, subjectID string, allowedSkills []string,
) (map[string]port.SkillActivation, error) {
	refs, assignments, err := s.resolveSkillRevisionRefs(ctx, tenantID, subjectID, allowedSkills)
	if err != nil {
		return nil, err
	}
	catalog := make(map[string]port.SkillActivation)
	if s.deps.SkillActivationResolver != nil && len(refs) > 0 {
		resolved, err := s.deps.SkillActivationResolver.ResolveSkills(ctx, tenantID, refs)
		if err != nil {
			return nil, fmt.Errorf("resolve Skill experiment revisions: %w", err)
		}
		catalog = resolved
	}
	applySkillAssignments(catalog, assignments)
	if err := validateSkillCatalogNames(catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

// validateSkillCatalogNames 校验绑定集合内 skill 解析名（contract Name 回退
// SkillID）唯一且不命中平台内置工具保留名（Spec D1）。stratum_skill 统一工具
// 按参数名分发，解析名歧义或与内置工具名冲突时 fail-closed，禁止静默截胡或
// 双义定位。
func validateSkillCatalogNames(catalog map[string]port.SkillActivation) error {
	seen := make(map[string]string, len(catalog))
	for skillID, a := range catalog {
		name := a.Name
		if name == "" {
			name = skillID
		}
		if agentgraph.IsReservedToolName(name) {
			return fmt.Errorf("skill %q: activation name %q collides with reserved platform tool name", skillID, name)
		}
		if other, exists := seen[name]; exists {
			return fmt.Errorf("skill activation name %q collides between %q and %q", name, other, skillID)
		}
		seen[name] = skillID
	}
	return nil
}

func (s *AgentService) resolveSkillRevisionRefs(
	ctx context.Context, tenantID, subjectID string, allowedSkills []string,
) ([]port.SkillRevisionRef, map[string]port.SkillRevisionAssignment, error) {
	refs := make([]port.SkillRevisionRef, 0, len(allowedSkills))
	assignments := make(map[string]port.SkillRevisionAssignment)
	for _, skillID := range allowedSkills {
		ref := port.SkillRevisionRef{SkillID: skillID}
		var assignment port.SkillRevisionAssignment
		if s.deps.SkillRevisionResolver != nil {
			resolved, found, err := s.deps.SkillRevisionResolver.ResolveSkillRevision(ctx, tenantID, skillID, subjectID)
			if err != nil {
				return nil, nil, fmt.Errorf("resolve Skill %s experiment assignment: %w", skillID, err)
			}
			if found {
				assignment = resolved
				ref.RevisionID = resolved.RevisionID
			}
		}
		refs = append(refs, ref)
		if assignment.RevisionID != "" {
			assignments[skillID] = assignment
		}
	}
	return refs, assignments, nil
}

func applySkillAssignments(
	catalog map[string]port.SkillActivation, assignments map[string]port.SkillRevisionAssignment,
) {
	for skillID, assignment := range assignments {
		activation := catalog[skillID]
		activation.SkillID = skillID
		activation.RevisionID = assignment.RevisionID
		activation.ExperimentID = assignment.ExperimentID
		activation.Variant = assignment.Variant
		catalog[skillID] = activation
	}
}

func stricterToolRisk(left, right port.ToolRiskLevel) port.ToolRiskLevel {
	if toolRiskRank(right) > toolRiskRank(left) {
		return right
	}
	return left
}

func toolRiskRank(risk port.ToolRiskLevel) int {
	switch risk {
	case port.ToolRiskDestructive:
		return 3
	case port.ToolRiskWriteReversible:
		return 2
	case port.ToolRiskRead:
		return 1
	default:
		return 0
	}
}

func (s *AgentService) ListToolTraces(
	ctx context.Context, tenantID, userID, traceID string,
) ([]ToolObservation, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, domain.ErrEvidenceUnavailable
	}
	if err := s.authorizeTraceOwner(ctx, tenantID, userID, traceID); err != nil {
		return nil, err
	}
	return s.deps.EvidenceProvider.ToolObservations(ctx, tenantID, traceID)
}

func (s *AgentService) ListTraceEvents(
	ctx context.Context, tenantID, userID, traceID string,
) ([]AgentTraceEvent, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, domain.ErrEvidenceUnavailable
	}
	if err := s.authorizeTraceOwner(ctx, tenantID, userID, traceID); err != nil {
		return nil, err
	}
	return s.deps.EvidenceProvider.TraceEvents(ctx, tenantID, traceID)
}

func (s *AgentService) authorizeTraceOwner(ctx context.Context, tenantID, userID, traceID string) error {
	evidence, err := s.deps.EvidenceProvider.Resolve(ctx, tenantID, traceID)
	if err != nil {
		return err
	}
	if userID == "" || evidence.UserID != userID {
		return domain.ErrEvidenceNotFound
	}
	return nil
}

// truncateRunes returns s truncated to maxRunes runes (not bytes).
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}

// executionIDOrNew returns id if non-empty, otherwise generates a new v7 UUID.
func executionIDOrNew(id string) string {
	if id == "" {
		return uuid.Must(uuid.NewV7()).String()
	}
	return id
}

// resolveExecutionWindow 执行时解析 agent 窗口（Spec 第 1 节两阶段），
// 替代 Create/Update 的一次性固化：管理员后补配置下次执行立即生效。
// 返回 (解析窗口, 来源)；模型窗口来源为 vendor_table/fallback 时 WARN。
func (s *AgentService) resolveExecutionWindow(
	ctx context.Context,
	tenantID, model string,
	explicit int,
) (int, agentgraph.WindowSource) {
	modelWin, src := agentgraph.ResolveModelWindow(
		ctx, model, s.deps.ModelContextProvider, s.deps.VendorWindowLookup,
	)
	if modelWin > constants.MaxContextWindowTokens {
		modelWin = constants.MaxContextWindowTokens
	}
	window, agentSrc := agentgraph.ResolveAgentWindow(modelWin, explicit)
	if src == agentgraph.WindowVendorTable || src == agentgraph.WindowFallback {
		s.deps.Logger.Warn("agent: model window resolved from fallback source",
			zap.String("model", model), zap.String("source", string(src)),
			zap.Int("model_window", modelWin), zap.Int("window", window))
	}
	return window, agentSrc
}

// resolveOutputReserve 解析主模型输出预留（Spec 第 2 节 outputReserve 来源链）：
// 显式 cfg.MaxTokens（>0）> DB 模型 max_tokens（模型管理权威）> vendor 表
// maxOut > DefaultOutputReserveTokens。DB 权威插在链头：预留 < 实际发送
// max_tokens 时发送值会溢出上下文窗口被 provider 400 永久中止，预留必须
// 与 llmgateway L1 注入一致。局限：execution 级 effective-parameter 覆写
// 对 max_tokens 的调整在此不可见（service 层解析时以 agent 配置为准），
// 保守方向一致，不放大可用窗口。
func (s *AgentService) resolveOutputReserve(
	ctx context.Context, tenantID, model string, explicitMaxTokens int,
) int {
	if explicitMaxTokens > 0 {
		return explicitMaxTokens
	}
	if reserve, ok := s.modelOutputReserve(ctx, tenantID, model); ok {
		return reserve
	}
	if s.deps.VendorWindowLookup != nil {
		if _, maxOut := s.deps.VendorWindowLookup(model); maxOut > 0 {
			return maxOut
		}
	}
	return constants.DefaultOutputReserveTokens
}

func (s *AgentService) modelOutputReserve(ctx context.Context, tenantID, model string) (int, bool) {
	if s.deps.ModelDetailsProvider == nil {
		return 0, false
	}
	details, err := s.deps.ModelDetailsProvider.ListTenantModelDetails(ctx, tenantID)
	if err != nil {
		return 0, false
	}
	for _, detail := range details {
		if detail.Model != model {
			continue
		}
		switch {
		case detail.EffectiveMaxTokens > 0:
			return detail.EffectiveMaxTokens, true
		case detail.MaxTokens > 0:
			return detail.MaxTokens, true
		case detail.DefaultOutputTokens > 0:
			return detail.DefaultOutputTokens, true
		default:
			return 0, false
		}
	}
	return 0, false
}
