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

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AgentServiceDeps groups the consumer-side dependencies of AgentService.
// Everything is an interface or value type — no concrete infrastructure
// imports allowed.
type AgentServiceDeps struct {
	Registry                *Registry
	TenantSettings          port.TenantSettings
	SkillLookup             port.SkillLookup
	SkillActivationResolver port.SkillActivationResolver
	SkillRevisionResolver   port.SkillRevisionResolver
	RAGSearch               port.RAGSearchProvider
	TenantResolver          port.TenantCapabilityResolver
	HistoryCompactorFactory func(port.CapabilityGateway, string, *zap.Logger) port.HistoryCompactor
	MCPTools                port.MCPToolProvider
	MCPToolExecutor         port.MCPToolExecutor
	MCPToolPolicy           port.MCPToolPolicyResolver
	ToolAuthorizer          *ToolAuthorizer
	ApprovalService         *ToolApprovalService
	ChatStore               ChatStore
	EvidenceProvider        port.TraceEvidenceProvider
	TracePayloadStore       port.TracePayloadStore
	CheckpointStore         CheckpointStore
	MemoryCleaner           port.AgentMemoryCleaner
	MemoryBuffer            port.BufferMemoryFn
	MemoryInjector          port.MemoryInjector
	RecallMemory            port.RecallMemoryFn
	Metrics                 observability.MetricsProvider
	OfficialDocsSearch      func(context.Context, string) ([]domain.Citation, error)
	DiagnosticProvider      port.DiagnosticEvidenceProvider
	Logger                  *zap.Logger
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

// CreateAgentInput is the create-agent payload application receives from
// transport.
type CreateAgentInput struct {
	TenantID              string
	Name                  string
	Type                  string
	Description           string
	SystemPrompt          string
	LLMModel              string
	EmbedModel            string
	MaxIterations         int
	MaxContextTokens      int
	AllowedSkills         []string
	MCPToolIDs            []string
	KnowledgeWorkspaceIDs []string
	MemoryScope           string
}

// UpdateAgentInput mirrors CreateAgentInput minus immutable EmbedModel.
type UpdateAgentInput struct {
	Name                  string
	Type                  string
	Description           string
	SystemPrompt          string
	LLMModel              string
	MaxIterations         int
	MaxContextTokens      int
	AllowedSkills         []string
	MCPToolIDs            []string
	KnowledgeWorkspaceIDs []string
	MemoryScope           string
}

// AgentDTO is the wire shape returned by AgentService for transport
// rendering. Strings only — handler reuses field-for-field.
type AgentDTO struct {
	ID                    string
	Name                  string
	Type                  string
	Description           string
	SystemPrompt          string
	LLMModel              string
	EmbedModel            string
	MaxIterations         int
	MaxContextTokens      int
	AllowedSkills         []string
	MCPToolIDs            []string
	KnowledgeWorkspaceIDs []string
	CreatedAt             string
	MemoryScope           string
	SystemKey             string
	IsSystem              bool
	ManagementMode        string
}

// Create persists a new agent for the tenant. Inherits embed_model from
// tenant defaults when caller omits it.
func (s *AgentService) Create(ctx context.Context, in CreateAgentInput) (AgentDTO, error) {
	embedModel := in.EmbedModel
	if embedModel == "" && s.deps.TenantSettings != nil {
		inherited, err := s.deps.TenantSettings.GetEmbedModel(ctx, in.TenantID)
		if err != nil {
			return AgentDTO{}, fmt.Errorf("agent service: get embed_model: %w", err)
		}
		embedModel = inherited
	}

	id := uuid.Must(uuid.NewV7()).String()
	cfg := &domain.AgentConfig{
		ID:                    id,
		Name:                  in.Name,
		Type:                  parseAgentTypeWire(in.Type),
		Description:           in.Description,
		SystemPrompt:          in.SystemPrompt,
		LLMModel:              in.LLMModel,
		EmbedModel:            embedModel,
		MaxIterations:         in.MaxIterations,
		MaxContextTokens:      in.MaxContextTokens,
		AllowedSkills:         in.AllowedSkills,
		MCPToolIDs:            in.MCPToolIDs,
		KnowledgeWorkspaceIDs: in.KnowledgeWorkspaceIDs,
		MemoryScope:           in.MemoryScope,
		Capabilities:          []domain.AgentCapability{},
	}

	a := NewBaseAgent(cfg, s.deps.Logger)
	if s.deps.Metrics != nil {
		a = a.WithMetrics(s.deps.Metrics)
	}
	if err := s.deps.Registry.Register(ctx, a); err != nil {
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
	return cfgToDTO(a.GetConfig()), nil
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
		EmbedModel: cfg.EmbedModel, MaxIterations: cfg.MaxIterations, MemoryScope: cfg.MemoryScope,
		CheckpointEnabled: cfg.CheckpointEnabled,
		StuckThreshold:    cfg.StuckThreshold,
		ModelParameters:   domain.ModelParameters{MaxContextTokens: cfg.MaxContextTokens},
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
	return context.WithTimeout(context.WithoutCancel(ctx), constants.AgentExecTimeout)
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
		LLMModel: revision.Model, EmbedModel: revision.EmbedModel, MaxIterations: revision.MaxIterations,
		MaxContextTokens: revision.ModelParameters.MaxContextTokens, MemoryScope: revision.MemoryScope,
		CheckpointEnabled: revision.CheckpointEnabled,
		StuckThreshold:    revision.StuckThreshold,
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

// Update replaces mutable fields on an existing agent. EmbedModel is
// immutable post-create — callers cannot change it through Update.
func (s *AgentService) Update(ctx context.Context, id string, in UpdateAgentInput) (AgentDTO, error) {
	existing, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return AgentDTO{}, fmt.Errorf("agent service update: %w", err)
	}
	if !ok {
		return AgentDTO{}, ErrNotFound
	}
	skills := in.AllowedSkills
	if skills == nil {
		skills = []string{}
	}
	cfg := &domain.AgentConfig{
		ID:                    id,
		Name:                  in.Name,
		Type:                  parseAgentTypeWire(in.Type),
		Description:           in.Description,
		SystemPrompt:          in.SystemPrompt,
		LLMModel:              in.LLMModel,
		EmbedModel:            existing.GetConfig().EmbedModel,
		MaxIterations:         in.MaxIterations,
		MaxContextTokens:      in.MaxContextTokens,
		AllowedSkills:         skills,
		MCPToolIDs:            in.MCPToolIDs,
		KnowledgeWorkspaceIDs: in.KnowledgeWorkspaceIDs,
		MemoryScope:           in.MemoryScope,
	}
	if err := s.deps.Registry.Update(ctx, cfg); err != nil {
		return AgentDTO{}, err
	}
	s.deps.Logger.Info("agent updated", zap.String("id", id), zap.String("name", in.Name))
	return cfgToDTO(cfg), nil
}

// Delete removes an agent and cascades deletion to conversations and memories.
func (s *AgentService) Delete(ctx context.Context, tenantID, id string) error {
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
	if err := s.deps.Registry.Remove(ctx, id); err != nil {
		return err
	}
	s.deps.Logger.Info("agent deleted", zap.String("id", id))
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
		ID:                    cfg.ID,
		Name:                  cfg.Name,
		Type:                  string(domain.ReActAgent),
		Description:           cfg.Description,
		SystemPrompt:          cfg.SystemPrompt,
		LLMModel:              cfg.LLMModel,
		EmbedModel:            cfg.EmbedModel,
		MaxIterations:         cfg.MaxIterations,
		MaxContextTokens:      cfg.MaxContextTokens,
		AllowedSkills:         cfg.AllowedSkills,
		MCPToolIDs:            cfg.MCPToolIDs,
		KnowledgeWorkspaceIDs: cfg.KnowledgeWorkspaceIDs,
		CreatedAt:             time.Now().Format(time.RFC3339),
		MemoryScope:           cfg.MemoryScope,
		SystemKey:             cfg.SystemKey,
		IsSystem:              cfg.IsSystem,
		ManagementMode:        cfg.ManagementMode,
	}
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
	TenantID       string
	TraceID        string
	Stream         bool
	EvolutionTrace EvolutionTraceMetadata
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

func (s *AgentService) Execute(ctx context.Context, agentID string, req ExecRequest, meta ExecMeta) (*AgentResult, int, error) {
	a, ok, err := s.deps.Registry.Get(ctx, agentID)
	if err != nil {
		return nil, 0, fmt.Errorf("execute agent: get agent: %w", err)
	}
	if !ok {
		return nil, 0, ErrNotFound
	}
	s.ensureConversation(ctx, meta.TenantID, agentID, req.UserID, &req)
	executionID := uuid.Must(uuid.NewV7()).String()
	_, options, err := s.assembleOptions(ctx, a, req, meta, executionID)
	if err != nil {
		s.recordSystemAssistantRequest(a, "unknown", "error")
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

	execCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), constants.AgentExecTimeout)
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
		scope := a.GetConfig().MemoryScope
		_ = s.deps.MemoryBuffer(ctx, meta.TenantID, req.UserID, agentID, req.ConversationID, scope, "user", req.Query)
		_ = s.deps.MemoryBuffer(ctx, meta.TenantID, req.UserID, agentID, req.ConversationID, scope, "assistant", result.Output)
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
	streamCtx, options, err := s.assembleOptions(ctx, a, req, meta, executionID)
	if err != nil {
		s.recordSystemAssistantRequest(a, "unknown", "error")
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

	execCtx, cancel = context.WithTimeout(context.WithoutCancel(streamCtx), constants.AgentExecTimeout)
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
		if runErr == nil && res != nil && s.deps.MemoryBuffer != nil && !assistantCfg.SystemAssistantMode {
			scope := a.GetConfig().MemoryScope
			_ = s.deps.MemoryBuffer(ctx, meta.TenantID, req.UserID, agentID, req.ConversationID, scope, "user", req.Query)
			_ = s.deps.MemoryBuffer(ctx, meta.TenantID, req.UserID, agentID, req.ConversationID, scope, "assistant", res.Output)
		}
		return res, durationMs, runErr
	}
	return execCtx, cancel, run, nil
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
	if role == "admin" || role == "member" {
		return role
	}
	return "unknown"
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
	ctx context.Context, tenantID string, page, pageSize int,
) ([]ExecutionRowDTO, int64, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, 0, domain.ErrEvidenceUnavailable
	}
	records, total, err := s.deps.EvidenceProvider.ListExecutions(
		ctx, tenantID, ListOptions{Page: page, PageSize: pageSize},
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
	meta := ExecMeta{TenantID: tenantID, TraceID: payload.TraceID}
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

func (s *AgentService) ExecuteSkillScenario(ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, activation port.SkillActivation) (*AgentResult, int, error) {
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
	options = append(options, WithExecutionID(executionID), WithSkillCatalog(map[string]port.SkillActivation{activation.SkillID: activation}), WithActiveSkill(activation))
	start := time.Now()
	result, err := a.Execute(context.WithoutCancel(ctx), req.Query, options...)
	duration := int(time.Since(start).Milliseconds())
	return result, duration, err
}

// assembleOptions builds the ExecutionOption slice and resolves the
// per-tenant CapabilityGateway. When meta.Stream is true, the returned
// ctx carries the per-tenant LLM completer for streaming inner calls.
func (s *AgentService) assembleOptions(
	ctx context.Context, a Agent, req ExecRequest, meta ExecMeta, executionID string,
) (context.Context, []ExecutionOption, error) {
	options := []ExecutionOption{WithMaxSteps(a.GetConfig().MaxIterations)}
	if req.MaxSteps > 0 {
		options = append(options, WithMaxSteps(req.MaxSteps))
	}
	isSystemAssistant := a.GetConfig().SystemKey == domain.SystemAssistantKey
	if isSystemAssistant && strings.TrimSpace(a.GetConfig().LLMModel) == "" {
		return ctx, nil, domain.ErrAssistantModelUnavailable
	}
	if s.deps.TenantResolver != nil {
		if capGW, apiKeys, ok := s.deps.TenantResolver.Resolve(ctx, meta.TenantID); ok {
			ctx = s.deps.TenantResolver.InjectCompleter(ctx, meta.TenantID)
			type capGWSetter interface {
				SetCapGateway(port.CapabilityGateway)
			}
			if setter, ok := a.(capGWSetter); ok {
				setter.SetCapGateway(capGW)
			}
			if s.deps.HistoryCompactorFactory != nil {
				if compactor := s.deps.HistoryCompactorFactory(capGW, a.GetConfig().LLMModel, s.deps.Logger); compactor != nil {
					type historyCompactorSetter interface {
						SetHistoryCompactor(port.HistoryCompactor)
					}
					if setter, ok := a.(historyCompactorSetter); ok {
						setter.SetHistoryCompactor(compactor)
					}
				}
			}
			if len(apiKeys) > 0 {
				options = append(options, WithLLMAPIKeys(apiKeys))
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
	if isSystemAssistant {
		extraTools = SystemAssistantToolDefinitions()
		skillCatalog = map[string]port.SkillActivation{}
	} else {
		extraTools, skillCatalog = s.buildExtraTools(
			ctx, meta.TenantID, subjectID, a.GetConfig().MCPToolIDs, a.GetConfig().AllowedSkills,
		)
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
		roleClass := "unknown"
		var authorization domain.DiagnosticAuthorization
		if s.deps.DiagnosticProvider != nil {
			var authorizeErr error
			authorization, authorizeErr = s.deps.DiagnosticProvider.Authorize(ctx, domain.DiagnosticRequest{
				TenantID: meta.TenantID, UserID: req.UserID,
				Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent, domain.DiagnosticAreaSkill, domain.DiagnosticAreaMCP, domain.DiagnosticAreaKnowledge, domain.DiagnosticAreaModel},
			})
			if authorizeErr != nil {
				return ctx, nil, authorizeErr
			}
			roleClass = boundedAssistantRoleClass(authorization.RoleClass)
		}
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
		options = append(options, WithSystemAssistantMode(), withSystemAssistantRoleClass(roleClass),
			withInternalToolResultGuard(func(value any) (port.GuardedToolResult, error) {
				structured, ok := value.(map[string]any)
				if !ok {
					return port.GuardedToolResult{}, ErrMCPToolResultSchema
				}
				return guard.Validate(port.MCPToolResult{StructuredContent: structured}, nil)
			}))
		return ctx, options, nil
	}
	if s.deps.ToolAuthorizer != nil {
		agentID, userID, conversationID, query := a.GetConfig().ID, req.UserID, req.ConversationID, req.Query
		pinned := make(map[string]string, len(skillCatalog))
		for skillID, activation := range skillCatalog {
			pinned[skillID] = activation.RevisionID
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
			return guard.Execute(callCtx, request)
		}))
	}
	if s.deps.RAGSearch != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		tenantID := meta.TenantID
		options = append(options, WithRAGSearchFn(func(rctx context.Context, workspaces []string, query string, topK int) (string, error) {
			return s.deps.RAGSearch.SearchKnowledge(rctx, tenantID, workspaces, query, topK)
		}))
	}
	return ctx, options, nil
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

// buildExtraTools converts MCPToolIDs and AllowedSkills into ToolDefinitions
// for the ReAct loop. Published skills use their tool contract names; legacy
// skills fall back to tenant-scoped names. The returned index maps tool names
// back to skill/version refs for execution routing.
func (s *AgentService) buildExtraTools(
	ctx context.Context,
	tenantID, subjectID string,
	mcpToolIDs, allowedSkills []string,
) ([]port.ToolDefinition, map[string]port.SkillActivation) {
	var tools []port.ToolDefinition
	refs := make([]port.SkillRevisionRef, 0, len(allowedSkills))

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
			risk := port.ToolRiskUnclassified
			policyResolved := false
			if s.deps.MCPToolPolicy != nil {
				resolved, err := s.deps.MCPToolPolicy.ResolveMCPToolRisk(ctx, tenantID, serverID, tool.CapabilityID)
				if err == nil && resolved != "" {
					risk = resolved
					policyResolved = true
				}
			}
			tool.Metadata["risk_level"] = string(risk)
			tool.Metadata["policy_resolved"] = policyResolved
			tools = append(tools, tool)
		}
	}

	assignments := make(map[string]port.SkillRevisionAssignment)
	for _, skillID := range allowedSkills {
		ref := port.SkillRevisionRef{SkillID: skillID}
		var assignment port.SkillRevisionAssignment
		if s.deps.SkillRevisionResolver != nil {
			if resolved, found, err := s.deps.SkillRevisionResolver.ResolveSkillRevision(ctx, tenantID, skillID, subjectID); err == nil && found {
				assignment = resolved
				ref.RevisionID = resolved.RevisionID
			}
		}
		refs = append(refs, ref)
		if assignment.RevisionID != "" {
			assignments[skillID] = assignment
		}
	}
	catalog := make(map[string]port.SkillActivation)
	if s.deps.SkillActivationResolver != nil && len(refs) > 0 {
		if resolved, err := s.deps.SkillActivationResolver.ResolveSkills(ctx, tenantID, refs); err == nil {
			catalog = resolved
		}
	}
	for skillID, assignment := range assignments {
		activation := catalog[skillID]
		activation.SkillID = skillID
		activation.RevisionID = assignment.RevisionID
		activation.ExperimentID = assignment.ExperimentID
		activation.Variant = assignment.Variant
		catalog[skillID] = activation
	}
	return tools, catalog
}

func (s *AgentService) ListToolTraces(ctx context.Context, tenantID, traceID string) ([]ToolObservation, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, domain.ErrEvidenceUnavailable
	}
	return s.deps.EvidenceProvider.ToolObservations(ctx, tenantID, traceID)
}

func (s *AgentService) ListTraceEvents(ctx context.Context, tenantID, traceID string) ([]AgentTraceEvent, error) {
	if s.deps.EvidenceProvider == nil {
		return nil, domain.ErrEvidenceUnavailable
	}
	return s.deps.EvidenceProvider.TraceEvents(ctx, tenantID, traceID)
}

// truncateRunes returns s truncated to maxRunes runes (not bytes).
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
