// Package application — agent_service.go.
//
// AgentService is the orchestration façade handlers consume for agent
// CRUD + execution. It aggregates Registry / TenantSettings / repos so
// HTTP handlers degrade to pure transport. SQL/HTTP/IO never appear in
// this file — every persistence call goes through a domain port.

package application

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
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
	// VendorWindowLookup 解析内置厂商静态能力表（窗口 + 最大输出）。
	// 由 wiring 注入 llmgateway.LookupModelSpec；nil 时回退链跳过 vendor 层。
	VendorWindowLookup        func(string) (int, int)
	HistoryCompactorFactory   func(port.CapabilityGateway, string, *zap.Logger, int) port.HistoryCompactor
	MCPTools                  port.MCPToolProvider
	MCPToolExecutor           port.MCPToolExecutor
	MCPToolPolicy             port.MCPToolPolicyResolver
	ToolAuthorizer            *ToolAuthorizer
	ApprovalService           *ToolApprovalService
	ChatStore                 ChatStore
	EvidenceProvider          port.TraceEvidenceProvider
	TracePayloadStore         port.TracePayloadStore
	CheckpointStore           CheckpointStore
	MemoryCleaner             port.AgentMemoryCleaner
	MemoryBuffer              port.BufferMemoryFn
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
	ParametersProvider        port.ParametersProvider
	Logger                    *zap.Logger
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
	TenantID               string
	ActorID                string
	Name                   string
	Type                   string
	Description            string
	SystemPrompt           string
	LLMModel               string
	MaxIterations          int
	MaxContextTokens       int
	Temperature            float32
	MaxTokens              int
	CompactionRecentGroups int
	CompactionSafetyRatio  float32
	AllowedSkills          []string
	MCPToolIDs             []string
	KnowledgeWorkspaceIDs  []string
	MemoryScope            string
	CheckpointEnabled      bool
	Editors                []string
}

type UpdateAgentInput struct {
	ActorID                string
	Name                   string
	Type                   string
	Description            string
	SystemPrompt           string
	LLMModel               string
	MaxIterations          int
	MaxContextTokens       int
	Temperature            float32
	MaxTokens              int
	CompactionRecentGroups int
	CompactionSafetyRatio  float32
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
	CheckpointEnabled     bool
}

// AgentDTO is the wire shape returned by AgentService for transport
// rendering. Strings only — handler reuses field-for-field.
type AgentDTO struct {
	ID                     string
	Name                   string
	Type                   string
	Description            string
	SystemPrompt           string
	LLMModel               string
	MaxIterations          int
	MaxContextTokens       int
	Temperature            float32
	MaxTokens              int
	CompactionRecentGroups int
	CompactionSafetyRatio  float32
	AllowedSkills          []string
	MCPToolIDs             []string
	KnowledgeWorkspaceIDs  []string
	CreatedAt              string
	MemoryScope            string
	SystemKey              string
	IsSystem               bool
	ManagementMode         string
	CheckpointEnabled      bool
	Parameters             map[string]any
	Editors                []string
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
// (temperature / max_tokens / compaction×2) against the parameter registry
// before persist. Zero means unset (gateway default) and is skipped; a nil
// provider (db unavailable) degrades to no-op, matching resolve.
func (s *AgentService) validateSamplingParams(
	ctx context.Context, temperature float32, maxTokens, compactionRecentGroups int, compactionSafetyRatio float32,
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
	if compactionRecentGroups != 0 {
		declared["compaction_recent_groups"] = compactionRecentGroups
	}
	if compactionSafetyRatio != 0 {
		declared["compaction_safety_ratio"] = float64(compactionSafetyRatio)
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

func (s *AgentService) Create(ctx context.Context, in CreateAgentInput) (AgentDTO, error) {
	if err := s.checkOwnership(ctx, in.ActorID, in.ActorID, nil); err != nil {
		return AgentDTO{}, err
	}
	if err := s.validateSamplingParams(ctx, in.Temperature, in.MaxTokens,
		in.CompactionRecentGroups, in.CompactionSafetyRatio); err != nil {
		return AgentDTO{}, err
	}
	id := uuid.Must(uuid.NewV7()).String()
	cfg := &domain.AgentConfig{
		ID:                     id,
		Name:                   in.Name,
		Type:                   parseAgentTypeWire(in.Type),
		Description:            in.Description,
		SystemPrompt:           in.SystemPrompt,
		LLMModel:               in.LLMModel,
		MaxIterations:          in.MaxIterations,
		MaxContextTokens:       in.MaxContextTokens, // 0 = 未配置，执行时两阶段解析
		Temperature:            in.Temperature,
		MaxTokens:              in.MaxTokens,
		CompactionRecentGroups: in.CompactionRecentGroups,
		CompactionSafetyRatio:  in.CompactionSafetyRatio,
		AllowedSkills:          in.AllowedSkills,
		MCPToolIDs:             in.MCPToolIDs,
		KnowledgeWorkspaceIDs:  in.KnowledgeWorkspaceIDs,
		MemoryScope:            in.MemoryScope,
		CheckpointEnabled:      in.CheckpointEnabled,
		Capabilities:           []domain.AgentCapability{},
		CreatedBy:              in.ActorID,
	}

	if err := s.validateWorkspaceBindings(ctx, in.TenantID, in.KnowledgeWorkspaceIDs); err != nil {
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
		CheckpointEnabled: cfg.CheckpointEnabled,
		StuckThreshold:    cfg.StuckThreshold,
		ModelParameters: domain.ModelParameters{
			MaxContextTokens:       cfg.MaxContextTokens,
			Temperature:            cfg.Temperature,
			MaxTokens:              cfg.MaxTokens,
			CompactionRecentGroups: cfg.CompactionRecentGroups,
			CompactionSafetyRatio:  cfg.CompactionSafetyRatio,
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
		MaxContextTokens:       revision.ModelParameters.MaxContextTokens,
		Temperature:            revision.ModelParameters.Temperature,
		MaxTokens:              revision.ModelParameters.MaxTokens,
		CompactionRecentGroups: revision.ModelParameters.CompactionRecentGroups,
		CompactionSafetyRatio:  revision.ModelParameters.CompactionSafetyRatio,
		MemoryScope:            revision.MemoryScope,
		CheckpointEnabled:      revision.CheckpointEnabled,
		StuckThreshold:         revision.StuckThreshold,
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
	a, err := s.deps.Registry.UpdateSystemAssistantModel(ctx, model, existingCfg.MemoryScope, existingCfg.CheckpointEnabled, existingCfg.MaxIterations, existingCfg.MaxContextTokens, audit)
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
	editorActor, err := s.resolveUpdateEditorActor(ctx, in.ActorID, id, existing.GetConfig().CreatedBy)
	if err != nil {
		return AgentDTO{}, err
	}
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

// buildUpdateConfig validates the sampling parameters and assembles the
// domain config from the wire input, deriving max context tokens when unset.
func (s *AgentService) buildUpdateConfig(ctx context.Context, id string, in UpdateAgentInput) (*domain.AgentConfig, error) {
	// Parameters map keys take precedence over the top-level sampling fields
	// (only present keys overwrite); validation runs on the merged result.
	temperature, maxTokens, recentGroups, safetyRatio := applyParameterOverrides(in)
	if err := s.validateSamplingParams(ctx, temperature, maxTokens, recentGroups, safetyRatio); err != nil {
		return nil, err
	}
	skills := in.AllowedSkills
	if skills == nil {
		skills = []string{}
	}
	cfg := &domain.AgentConfig{
		ID:                     id,
		Name:                   in.Name,
		Type:                   parseAgentTypeWire(in.Type),
		Description:            in.Description,
		SystemPrompt:           in.SystemPrompt,
		LLMModel:               in.LLMModel,
		MaxIterations:          in.MaxIterations,
		MaxContextTokens:       in.MaxContextTokens, // 0 = 未配置，执行时两阶段解析
		Temperature:            temperature,
		MaxTokens:              maxTokens,
		CompactionRecentGroups: recentGroups,
		CompactionSafetyRatio:  safetyRatio,
		AllowedSkills:          skills,
		MCPToolIDs:             in.MCPToolIDs,
		KnowledgeWorkspaceIDs:  in.KnowledgeWorkspaceIDs,
		MemoryScope:            in.MemoryScope,
		CheckpointEnabled:      in.CheckpointEnabled,
	}
	if err := s.validateWorkspaceBindings(ctx, reqctx.TenantIDFromContext(ctx), in.KnowledgeWorkspaceIDs); err != nil {
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

// applyParameterOverrides merges the declared parameters map onto the
// top-level sampling fields. Only keys present in the map overwrite; map
// values win over the top-level fields. Zero values pass through unchanged
// (0 = unset, the merge pack skips them, so an explicit 0 never clears).
func applyParameterOverrides(in UpdateAgentInput) (float32, int, int, float32) {
	temperature, maxTokens := in.Temperature, in.MaxTokens
	recentGroups, safetyRatio := in.CompactionRecentGroups, in.CompactionSafetyRatio
	if len(in.Parameters) == 0 {
		return temperature, maxTokens, recentGroups, safetyRatio
	}
	if v, ok := numericSampleValue(in.Parameters["temperature"]); ok {
		temperature = float32(v)
	}
	if v, ok := numericSampleValue(in.Parameters["max_tokens"]); ok {
		maxTokens = int(v)
	}
	if v, ok := numericSampleValue(in.Parameters["compaction_recent_groups"]); ok {
		recentGroups = int(v)
	}
	if v, ok := numericSampleValue(in.Parameters["compaction_safety_ratio"]); ok {
		safetyRatio = float32(v)
	}
	return temperature, maxTokens, recentGroups, safetyRatio
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
	model := in.LLMModel
	if model == "" {
		model = cfg.LLMModel
	}
	if model != cfg.LLMModel {
		if s.deps.TenantModelValidator != nil {
			if err := s.deps.TenantModelValidator.ValidateTenantChatModel(ctx, tenantID, model); err != nil {
				if errors.Is(err, domain.ErrAssistantModelUnavailable) ||
					errors.Is(err, domain.ErrInvalidSystemAssistantModel) {
					return AgentDTO{}, domain.ErrInvalidSystemAssistantModel
				}
				return AgentDTO{}, fmt.Errorf("update system assistant model: %w", err)
			}
		}
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
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindAgent, cfg.ID, auditdomain.ChangeOpUpdate, in.ActorID,
		AgentSafeProjection(cfg), nil)
	if err != nil {
		return AgentDTO{}, err
	}
	updated, err := s.deps.Registry.UpdateSystemAssistantAll(ctx, model, memoryScope, in.CheckpointEnabled, maxIterations, maxContextTokens, audit)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("update system assistant: %w", err)
	}
	s.deps.Logger.Info("system assistant updated", zap.String("id", cfg.ID))
	return cfgToDTO(updated.GetConfig()), nil
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
		ID:                     cfg.ID,
		Name:                   cfg.Name,
		Type:                   string(domain.ReActAgent),
		Description:            cfg.Description,
		SystemPrompt:           cfg.SystemPrompt,
		LLMModel:               cfg.LLMModel,
		MaxIterations:          cfg.MaxIterations,
		MaxContextTokens:       cfg.MaxContextTokens,
		Temperature:            cfg.Temperature,
		MaxTokens:              cfg.MaxTokens,
		CompactionRecentGroups: cfg.CompactionRecentGroups,
		CompactionSafetyRatio:  cfg.CompactionSafetyRatio,
		AllowedSkills:          cfg.AllowedSkills,
		MCPToolIDs:             cfg.MCPToolIDs,
		KnowledgeWorkspaceIDs:  cfg.KnowledgeWorkspaceIDs,
		CreatedAt:              time.Now().Format(time.RFC3339),
		MemoryScope:            cfg.MemoryScope,
		SystemKey:              cfg.SystemKey,
		IsSystem:               cfg.IsSystem,
		CheckpointEnabled:      cfg.CheckpointEnabled,
		ManagementMode:         cfg.ManagementMode,
		Parameters:             samplingParameterMap(cfg),
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
	if cfg.CompactionRecentGroups != 0 {
		params["compaction_recent_groups"] = cfg.CompactionRecentGroups
	}
	if cfg.CompactionSafetyRatio != 0 {
		params["compaction_safety_ratio"] = cfg.CompactionSafetyRatio
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
	a, ok, err := s.deps.Registry.Get(ctx, agentID)
	if err != nil {
		return nil, 0, fmt.Errorf("execute agent: get agent: %w", err)
	}
	if !ok {
		return nil, 0, ErrNotFound
	}
	s.ensureConversation(ctx, meta.TenantID, agentID, req.UserID, &req)
	executionID := executionIDOrNew(meta.ExecutionID)
	preparationStart := time.Now()
	recordExecutionPreparation(ctx, a, req, meta, executionID)
	a, assignment, err := s.resolveExecutionAgent(ctx, a, meta.TenantID, agentID, executionSubject(req, meta))
	if err != nil {
		recordExecutionPreparationFailure(ctx, preparationStart, "resolve_agent_revision")
		return nil, 0, fmt.Errorf("execute agent: resolve revision: %w", err)
	}
	applyAgentAssignment(&meta, agentID, assignment)
	recordExecutionPreparation(ctx, a, req, meta, executionID)
	_, options, err := s.assembleOptions(ctx, a, req, meta, executionID)
	if err != nil {
		s.recordSystemAssistantRequest(a, "unknown", "error")
		recordExecutionPreparationFailure(ctx, preparationStart, "assemble_options")
		return nil, 0, fmt.Errorf("execute agent: assemble options: %w", err)
	}
	options = append(options, WithExecutionID(executionID))
	assistantCfg := &ExecutionConfig{}
	assistantCfg.ApplyOptions(options)

	s.deps.Logger.Debug("agent.execute",
		zap.String("agent_id", agentID),
		zap.String("trace_id", meta.TraceID),
		zap.String("tenant_id", meta.TenantID),
		zap.String("user_id", req.UserID),
		zap.String("conversation_id", req.ConversationID),
	)

	execCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()

	start := time.Now()
	result, err := a.Execute(execCtx, req.Query, options...)
	durationMs := int(time.Since(start).Milliseconds())
	if assistantCfg.SystemAssistantMode && s.deps.Metrics != nil {
		outcome := "success"
		if err != nil {
			outcome = "error"
		} else if hasFailedAssistantArtifact(result) {
			outcome = "evidence_error"
		}
		s.deps.Metrics.IncSystemAssistantRequest(assistantCfg.SystemAssistantRoleClass,
			assistantCfg.EvolutionTrace.ResourceManifest["system-assistant-profile"], outcome)
	}

	if err != nil {
		s.deps.Logger.Error("agent.execute",
			zap.String("agent_id", agentID),
			zap.String("trace_id", meta.TraceID),
			zap.String("tenant_id", meta.TenantID),
			zap.String("user_id", req.UserID),
			zap.Int("duration_ms", durationMs),
			zap.Error(err),
		)
	} else {
		s.deps.Logger.Info("agent.execute",
			zap.String("agent_id", agentID),
			zap.String("trace_id", meta.TraceID),
			zap.String("tenant_id", meta.TenantID),
			zap.String("user_id", req.UserID),
			zap.Int("duration_ms", durationMs),
		)
	}

	if err == nil && result != nil && s.deps.MemoryBuffer != nil && !assistantCfg.SystemAssistantMode {
		// MemoryBuffer 是执行成功后的旁路异步摄取（Redis buffer，供后台记忆
		// 提取）。答案已交付，缓冲失败不阻断响应——降级决策，但错误必须显式
		// 处理并记录，禁止静默吞掉。
		scope := a.GetConfig().MemoryScope
		s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "user", req.Query)
		s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "assistant", result.Output)
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
) (execCtx context.Context, cancel context.CancelFunc, run func() (*AgentResult, int, error), err error) {
	a, ok, err := s.deps.Registry.Get(ctx, agentID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("execute stream: get agent: %w", err)
	}
	if !ok {
		return nil, nil, nil, ErrNotFound
	}
	s.ensureConversation(ctx, meta.TenantID, agentID, req.UserID, &req)
	executionID := uuid.Must(uuid.NewV7()).String()
	preparationStart := time.Now()
	recordExecutionPreparation(ctx, a, req, meta, executionID)
	a, assignment, err := s.resolveExecutionAgent(ctx, a, meta.TenantID, agentID, executionSubject(req, meta))
	if err != nil {
		recordExecutionPreparationFailure(ctx, preparationStart, "resolve_agent_revision")
		return nil, nil, nil, fmt.Errorf("execute stream: resolve revision: %w", err)
	}
	applyAgentAssignment(&meta, agentID, assignment)
	recordExecutionPreparation(ctx, a, req, meta, executionID)
	streamCtx, options, err := s.assembleOptions(ctx, a, req, meta, executionID)
	if err != nil {
		s.recordSystemAssistantRequest(a, "unknown", "error")
		recordExecutionPreparationFailure(ctx, preparationStart, "assemble_options")
		return nil, nil, nil, fmt.Errorf("execute stream: assemble options: %w", err)
	}
	assistantCfg := &ExecutionConfig{}
	assistantCfg.ApplyOptions(options)
	var firstToken sync.Once
	var streamStarted time.Time
	wrappedTokenCb := tokenCb
	if assistantCfg.SystemAssistantMode && s.deps.Metrics != nil {
		wrappedTokenCb = func(token string) {
			firstToken.Do(func() {
				s.deps.Metrics.RecordSystemAssistantTTFT(assistantCfg.SystemAssistantRoleClass,
					assistantCfg.EvolutionTrace.ResourceManifest["system-assistant-profile"], time.Since(streamStarted).Seconds())
			})
			if tokenCb != nil {
				tokenCb(token)
			}
		}
	}
	options = append(options, WithTokenCallback(wrappedTokenCb))
	options = append(options, WithExecutionID(executionID))

	execCtx, cancel = context.WithCancel(context.WithoutCancel(streamCtx))
	run = func() (*AgentResult, int, error) {
		s.deps.Logger.Debug("agent.execute_stream",
			zap.String("agent_id", agentID),
			zap.String("trace_id", meta.TraceID),
			zap.String("tenant_id", meta.TenantID),
			zap.String("user_id", req.UserID),
			zap.String("conversation_id", req.ConversationID),
		)
		start := time.Now()
		streamStarted = start
		res, runErr := a.Execute(execCtx, req.Query, options...)
		durationMs := int(time.Since(start).Milliseconds())
		if assistantCfg.SystemAssistantMode && s.deps.Metrics != nil {
			outcome := "success"
			if runErr != nil {
				outcome = "error"
			} else if hasFailedAssistantArtifact(res) {
				outcome = "evidence_error"
			}
			s.deps.Metrics.IncSystemAssistantRequest(assistantCfg.SystemAssistantRoleClass,
				assistantCfg.EvolutionTrace.ResourceManifest["system-assistant-profile"], outcome)
		}
		if runErr != nil {
			s.deps.Logger.Error("agent.execute_stream",
				zap.String("agent_id", agentID),
				zap.String("trace_id", meta.TraceID),
				zap.String("tenant_id", meta.TenantID),
				zap.Int("duration_ms", durationMs),
				zap.Error(runErr),
			)
		} else {
			s.deps.Logger.Info("agent.execute_stream",
				zap.String("agent_id", agentID),
				zap.String("trace_id", meta.TraceID),
				zap.String("tenant_id", meta.TenantID),
				zap.Int("duration_ms", durationMs),
			)
		}
		if runErr == nil && res != nil && !assistantCfg.SystemAssistantMode {
			// 降级决策与 Execute 路径一致：答案已交付，旁路记忆缓冲失败只记日志。
			scope := a.GetConfig().MemoryScope
			s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "user", req.Query)
			s.bufferMemoryTurn(ctx, meta, req, agentID, scope, "assistant", res.Output)
		}
		return res, durationMs, runErr
	}
	return execCtx, cancel, run, nil
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

func (s *AgentService) ListPendingApprovals(ctx context.Context, tenantID string) ([]domain.ToolApproval, error) {
	if s.deps.ApprovalService == nil {
		return []domain.ToolApproval{}, nil
	}
	return s.deps.ApprovalService.ListPending(ctx, tenantID)
}

func (s *AgentService) DecideToolApproval(ctx context.Context, tenantID, id, decision, actor, reason string) error {
	if s.deps.ApprovalService == nil {
		return errors.New("tool approval service not configured")
	}
	return s.deps.ApprovalService.Decide(ctx, tenantID, id, decision, actor, reason)
}

func (s *AgentService) ResumeToolApproval(ctx context.Context, tenantID, approvalID string) (*AgentResult, int, error) {
	if s.deps.ApprovalService == nil || s.deps.MCPToolExecutor == nil {
		return nil, 0, errors.New("tool approval runtime not configured")
	}
	payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
	if err != nil {
		return nil, 0, err
	}
	a, ok, err := s.deps.Registry.Get(ctx, payload.AgentID)
	if err != nil {
		return nil, 0, fmt.Errorf("resume tool approval: get agent: %w", err)
	}
	if !ok {
		return nil, 0, ErrNotFound
	}
	req := ExecRequest{Query: payload.Query, ConversationID: payload.ConversationID, UserID: payload.UserID}
	meta := ExecMeta{TenantID: tenantID, TraceID: payload.TraceID,
		KnowledgeAssignmentsPinned: true, PinnedKnowledgeRevisions: payload.PinnedKnowledgeRevisions}
	_, options, err := s.assembleOptions(ctx, a, req, meta, payload.ExecutionID)
	if err != nil {
		return nil, 0, fmt.Errorf("resume tool approval: assemble options: %w", err)
	}
	if len(payload.PinnedSkillRevisions) > 0 && s.deps.SkillActivationResolver != nil {
		refs := make([]port.SkillRevisionRef, 0, len(payload.PinnedSkillRevisions))
		for skillID, revisionID := range payload.PinnedSkillRevisions {
			refs = append(refs, port.SkillRevisionRef{SkillID: skillID, RevisionID: revisionID})
		}
		catalog, resolveErr := s.deps.SkillActivationResolver.ResolveSkills(ctx, tenantID, refs)
		if resolveErr != nil {
			return nil, 0, resolveErr
		}
		options = append(options, WithSkillCatalog(catalog))
	}
	used := false
	guard := NewToolExecutionGuard(ToolExecutionGuardDeps{
		Authorizer: s.deps.ToolAuthorizer,
		Executor:   s.deps.MCPToolExecutor,
		ExecuteApproved: func(callCtx context.Context, request ToolExecutionRequest) (port.MCPToolResult, error) {
			used = true
			return s.deps.ApprovalService.ExecuteApproved(
				callCtx, tenantID, approvalID, request.Tool.ServerID,
				request.Tool.CapabilityID, request.Arguments, s.deps.MCPToolExecutor,
			)
		},
	})
	options = append(options, WithExecutionID(payload.ExecutionID), WithToolExecutionFn(func(
		callCtx context.Context, request port.ToolExecutionRequest,
	) (any, error) {
		request.TenantID = tenantID
		request.UserID = payload.UserID
		request.AgentID = payload.AgentID
		request.TraceID = payload.TraceID
		request.ExecutionID = payload.ExecutionID
		request.AgentToolIDs = a.GetConfig().MCPToolIDs
		if !used && request.Tool.ServerID == payload.ServerID &&
			request.Tool.CapabilityID == payload.ToolName && reflect.DeepEqual(request.Arguments, payload.Arguments) {
			request.ApprovalID = approvalID
		}
		return guard.Execute(callCtx, request)
	}))
	start := time.Now()
	result, runErr := a.Execute(context.WithoutCancel(ctx), payload.Query, options...)
	runErr = approvedToolResumeError(used, runErr)
	duration := int(time.Since(start).Milliseconds())
	runErr = completeApprovalResume(ctx, s.deps.CheckpointStore, tenantID, payload.ExecutionID, runErr)
	return result, duration, runErr
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
		// outputReserve 与窗口同一来源链：显式 max_tokens > vendor maxOut > 常量。
		WithOutputReserve(s.resolveOutputReserve(a.GetConfig().LLMModel, a.GetConfig().MaxTokens)),
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
				compactionMaxTokens := constants.DynamicCompactionMaxTokens(window)
				if compactor := s.deps.HistoryCompactorFactory(capGW, a.GetConfig().LLMModel, s.deps.Logger, compactionMaxTokens); compactor != nil {
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
	roleClass := "unknown"
	var authorization domain.DiagnosticAuthorization
	if isSystemAssistant {
		var toolingErr error
		extraTools, skillCatalog, roleClass, toolingErr = s.resolveSystemAssistantTooling(
			ctx, meta, req, a, subjectID, &authorization,
		)
		if toolingErr != nil {
			return ctx, nil, toolingErr
		}
	} else {
		var resolveErr error
		extraTools, skillCatalog, resolveErr = s.buildExtraToolsChecked(
			ctx, meta.TenantID, subjectID, a.GetConfig().MCPToolIDs, a.GetConfig().AllowedSkills,
		)
		if resolveErr != nil {
			return ctx, nil, fmt.Errorf("resolve experiment resources: %w", resolveErr)
		}
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
			request.AgentToolIDs = a.GetConfig().MCPToolIDs
			request.MCPRevisionID = mcpAssignments[request.Tool.ServerID].RevisionID
			return guard.Execute(callCtx, request)
		}))
	}
	if s.deps.RAGSearch != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		options = appendRAGSearchOptions(options, meta.TenantID, s.deps.RAGSearch, knowledgeAssignments)
	}
	return ctx, s.resolveEffectiveParameters(ctx, a, options), nil
}

// appendRAGSearchOptions wires the plain and (when supported) evidence-capable
// knowledge search variants. Both share the revision/mutable split: revision
// snapshots contribute content only, mutable workspaces fan out through the
// live search provider.
func appendRAGSearchOptions(
	options []ExecutionOption,
	tenantID string,
	search port.RAGSearchProvider,
	knowledgeAssignments map[string]port.KnowledgeRevisionAssignment,
) []ExecutionOption {
	options = append(options, WithRAGSearchFn(func(rctx context.Context, workspaces []string, query string, topK int, viewerID string) (string, error) {
		var combined strings.Builder
		mutable := make([]string, 0, len(workspaces))
		for _, workspace := range workspaces {
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
	return appendEvidenceRAGOption(options, search, tenantID, knowledgeAssignments)
}

// appendEvidenceRAGOption wires the evidence-capable search variant when the
// provider supports chunk-level provenance. Same revision/mutable split as
// the plain variant; revision snapshots have no provenance path, so they
// contribute content only. The options slice is returned unchanged when the
// provider lacks evidence support (existing behavior preserved).
func appendEvidenceRAGOption(
	options []ExecutionOption,
	search port.RAGSearchProvider,
	tenantID string,
	knowledgeAssignments map[string]port.KnowledgeRevisionAssignment,
) []ExecutionOption {
	evidenceProvider, ok := search.(port.RAGSearchEvidenceProvider)
	if !ok {
		return options
	}
	return append(options, WithRAGSearchFnWithEvidence(func(rctx context.Context, workspaces []string, query string, topK int, viewerID string) (port.RAGSearchEvidence, error) {
		var combined strings.Builder
		var sources []port.RAGSearchSource
		mutable := make([]string, 0, len(workspaces))
		for _, workspace := range workspaces {
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
		}
		return port.RAGSearchEvidence{Content: combined.String(), Sources: sources}, nil
	}))
}

// resolveEffectiveParameters merges platform defaults into the execution
// options at the assemble point (no caching). Agent-config values — the
// resource-declared layer — already flow into execution through the
// snapshotExecutionConfig backfill; the provider fills in platform defaults
// only where the resource left the key at 0=unset. Resolution errors degrade
// to unset (execution keeps gateway defaults): parameters are an
// optimization input, not an execution gate.
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
		"agent.compaction_recent_groups": cfg.CompactionRecentGroups,
		"agent.compaction_safety_ratio":  cfg.CompactionSafetyRatio,
		"agent.compaction_cooldown_sec":  cfg.CompactionCooldownSec,
		"agent.max_tokens_per_execution": cfg.MaxTokensPerExecution,
	}
	effective, err := s.deps.ParametersProvider.ResolveForResource(ctx, declared)
	if err != nil {
		s.deps.Logger.Warn("agent execute: resolve effective parameters, keeping defaults", zap.Error(err))
		return options
	}
	if v, ok := effective["agent.temperature"].(float64); ok {
		options = append(options, WithTemperature(float32(v)))
	}
	if v, ok := effective["agent.max_tokens"].(int64); ok {
		options = append(options, WithMaxTokens(int(v)))
	}
	if v, ok := effective["agent.compaction_recent_groups"].(int64); ok {
		options = append(options, WithCompactionRecentGroups(int(v)))
	}
	if v, ok := effective["agent.compaction_safety_ratio"].(float64); ok {
		options = append(options, WithCompactionSafetyRatio(float32(v)))
	}
	if v, ok := effective["agent.compaction_cooldown_sec"].(int64); ok {
		options = append(options, WithCompactionCooldownSec(int(v)))
	}
	if v, ok := effective["agent.max_tokens_per_execution"].(int64); ok {
		options = append(options, WithMaxTokensPerExecution(int(v)))
	}
	// Platform-scope execution toggles are resolved individually; they are
	// not resource keys so ResolveForResource never returns them.
	if opt := captureParametersOption(ctx, s.deps.ParametersProvider); opt != nil {
		options = append(options, opt)
	}
	return options
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
	if (roleClass == "admin" || roleClass == "owner") && s.deps.ProposalService != nil {
		extraTools = SystemAssistantToolDefinitionsForRole(roleClass)
	}
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
	if (roleClass == "admin" || roleClass == "owner") && s.deps.ProposalService != nil {
		proposalService := s.deps.ProposalService
		tenantID, actorID, conversationID := meta.TenantID, req.UserID, req.ConversationID
		options = append(options, withProposalCreateFn(func(callCtx context.Context, args map[string]any) (domain.ResourceChangeProposalArtifact, error) {
			kind, operation, resourceID, payload, parseErr := ParseResourceChangeToolArguments(args)
			if parseErr != nil {
				return domain.ResourceChangeProposalArtifact{}, parseErr
			}
			proposal, createErr := proposalService.CreateProposal(callCtx, CreateProposalInput{
				TenantID: tenantID, ConversationID: conversationID, ActorID: actorID,
				Kind: kind, Operation: operation, ResourceID: resourceID, Payload: payload,
			})
			artifact := domain.ResourceChangeProposalArtifact{
				ID: proposal.ID, ResourceKind: proposal.ResourceKind, Operation: proposal.Operation,
				Status: proposal.Status, Summary: proposal.Summary, ExpiresAt: proposal.ExpiresAt,
			}
			return artifact, createErr
		}))
	}
	if (roleClass == "admin" || roleClass == "owner") && s.deps.ResourceChangeApplier != nil {
		applier := s.deps.ResourceChangeApplier
		actorID := req.UserID
		options = append(options, withResourceChangeApplyFn(func(callCtx context.Context, args map[string]any) (domain.ApplyResult, error) {
			return applier(callCtx, actorID, args)
		}))
	}
	options = append(options, WithSystemAssistantMode(), withSystemAssistantRoleClass(roleClass),
		withInternalToolResultGuard(func(value any) (port.GuardedToolResult, error) {
			structured, ok := value.(map[string]any)
			if !ok {
				return port.GuardedToolResult{}, ErrMCPToolResultSchema
			}
			return guard.Validate(port.MCPToolResult{StructuredContent: structured}, nil)
		}))
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
		for _, tool := range s.deps.MCPTools.ToolsForServer(ctx, serverID) {
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
	return catalog, nil
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
		ctx, tenantID, model, s.deps.ModelContextProvider, s.deps.VendorWindowLookup,
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
// 显式 cfg.MaxTokens（>0）> vendor 表 maxOut > DefaultOutputReserveTokens。
// 局限：execution 级 effective-parameter 覆写对 max_tokens 的调整在此不可见
// （service 层解析时以 agent 配置为准），保守方向一致，不放大可用窗口。
func (s *AgentService) resolveOutputReserve(model string, explicitMaxTokens int) int {
	if explicitMaxTokens > 0 {
		return explicitMaxTokens
	}
	if s.deps.VendorWindowLookup != nil {
		if _, maxOut := s.deps.VendorWindowLookup(model); maxOut > 0 {
			return maxOut
		}
	}
	return constants.DefaultOutputReserveTokens
}
