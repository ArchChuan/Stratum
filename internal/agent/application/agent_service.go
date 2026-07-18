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
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const previewMaxChars = 50

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
	MCPTools                port.MCPToolProvider
	MCPToolExecutor         port.MCPToolExecutor
	MCPToolPolicy           port.MCPToolPolicyResolver
	ApprovalService         *ToolApprovalService
	ExecStore               ExecutionStore
	ChatStore               ChatStore
	ToolTraceStore          ToolTraceStore
	TraceEventStore         TraceEventStore
	CheckpointStore         CheckpointStore
	MemoryCleaner           port.AgentMemoryCleaner
	MemoryBuffer            port.BufferMemoryFn
	Metrics                 observability.MetricsProvider
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
	a, ok := s.deps.Registry.Get(ctx, id)
	if !ok {
		return AgentDTO{}, ErrNotFound
	}
	return cfgToDTO(a.GetConfig()), nil
}

// List returns all agents in the tenant schema.
func (s *AgentService) List(ctx context.Context) ([]AgentDTO, error) {
	agents := s.deps.Registry.GetAll(ctx)
	out := make([]AgentDTO, 0, len(agents))
	for _, a := range agents {
		out = append(out, cfgToDTO(a.GetConfig()))
	}
	return out, nil
}

// Update replaces mutable fields on an existing agent. EmbedModel is
// immutable post-create — callers cannot change it through Update.
func (s *AgentService) Update(ctx context.Context, id string, in UpdateAgentInput) (AgentDTO, error) {
	existing, ok := s.deps.Registry.Get(ctx, id)
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
	switch t {
	case "react":
		return domain.ReActAgent
	case "cot":
		return domain.CoTAgent
	case "planning":
		return domain.PlanningAgent
	case "tool_calling":
		return domain.ToolCallingAgent
	case "rag":
		return domain.RAGAgent
	case "swarm":
		return domain.SwarmAgent
	default:
		return domain.ReActAgent
	}
}

func cfgToDTO(cfg *domain.AgentConfig) AgentDTO {
	return AgentDTO{
		ID:                    cfg.ID,
		Name:                  cfg.Name,
		Type:                  string(cfg.Type),
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
	TenantID string
	TraceID  string
	Stream   bool
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
	a, ok := s.deps.Registry.Get(ctx, agentID)
	if !ok {
		return nil, 0, ErrNotFound
	}
	s.ensureConversation(ctx, meta.TenantID, agentID, req.UserID, &req)
	executionID := uuid.Must(uuid.NewV7()).String()
	_, options := s.assembleOptions(ctx, a, req, meta, executionID)
	options = append(options, WithExecutionID(executionID))

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

	s.recordExecution(ctx, executionID, agentID, req.UserID, a.GetConfig().Name, req.Query, result, err, durationMs)
	if err == nil && result != nil && s.deps.MemoryBuffer != nil {
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
	a, ok := s.deps.Registry.Get(ctx, agentID)
	if !ok {
		return nil, nil, nil, ErrNotFound
	}
	s.ensureConversation(ctx, meta.TenantID, agentID, req.UserID, &req)
	executionID := uuid.Must(uuid.NewV7()).String()
	streamCtx, options := s.assembleOptions(ctx, a, req, meta, executionID)
	options = append(options, WithTokenCallback(tokenCb))
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
		res, runErr := a.Execute(execCtx, req.Query, options...)
		durationMs := int(time.Since(start).Milliseconds())
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
		s.recordExecution(ctx, executionID, agentID, req.UserID, a.GetConfig().Name, req.Query, res, runErr, durationMs)
		if runErr == nil && res != nil && s.deps.MemoryBuffer != nil {
			scope := a.GetConfig().MemoryScope
			_ = s.deps.MemoryBuffer(ctx, meta.TenantID, req.UserID, agentID, req.ConversationID, scope, "user", req.Query)
			_ = s.deps.MemoryBuffer(ctx, meta.TenantID, req.UserID, agentID, req.ConversationID, scope, "assistant", res.Output)
		}
		return res, durationMs, runErr
	}
	return execCtx, cancel, run, nil
}

// ListExecutions paginates the per-tenant execution history.
func (s *AgentService) ListExecutions(ctx context.Context, page, pageSize int) ([]ExecutionRowDTO, int64, error) {
	if s.deps.ExecStore == nil {
		return []ExecutionRowDTO{}, 0, nil
	}
	records, total, err := s.deps.ExecStore.List(ctx, ListOptions{Page: page, PageSize: pageSize})
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
	a, ok := s.deps.Registry.Get(ctx, payload.AgentID)
	if !ok {
		return nil, 0, ErrNotFound
	}
	req := ExecRequest{Query: payload.Query, ConversationID: payload.ConversationID, UserID: payload.UserID}
	meta := ExecMeta{TenantID: tenantID, TraceID: payload.TraceID}
	_, options := s.assembleOptions(ctx, a, req, meta, payload.ExecutionID)
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
	options = append(options, WithExecutionID(payload.ExecutionID), WithApprovedToolCallFn(func(callCtx context.Context, serverID, toolName string, args map[string]any) (any, bool, error) {
		if used || serverID != payload.ServerID || toolName != payload.ToolName || !reflect.DeepEqual(args, payload.Arguments) {
			return nil, false, nil
		}
		used = true
		output, executeErr := s.deps.ApprovalService.ExecuteApproved(callCtx, tenantID, approvalID, serverID, toolName, args, s.deps.MCPToolExecutor)
		return output, true, executeErr
	}))
	start := time.Now()
	result, runErr := a.Execute(context.WithoutCancel(ctx), payload.Query, options...)
	runErr = approvedToolResumeError(used, runErr)
	duration := int(time.Since(start).Milliseconds())
	s.recordExecution(ctx, payload.ExecutionID, payload.AgentID, payload.UserID, a.GetConfig().Name, payload.Query, result, runErr, duration)
	if runErr == nil && s.deps.CheckpointStore != nil {
		_ = s.deps.CheckpointStore.MarkCompleted(ctx, tenantID, payload.ExecutionID)
	}
	return result, duration, runErr
}

func (s *AgentService) ExecuteSkillScenario(ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, activation port.SkillActivation) (*AgentResult, int, error) {
	a, ok := s.deps.Registry.Get(ctx, agentID)
	if !ok {
		return nil, 0, ErrNotFound
	}
	executionID := uuid.Must(uuid.NewV7()).String()
	_, options := s.assembleOptions(ctx, a, req, meta, executionID)
	options = append(options, WithExecutionID(executionID), WithSkillCatalog(map[string]port.SkillActivation{activation.SkillID: activation}), WithActiveSkill(activation))
	start := time.Now()
	result, err := a.Execute(context.WithoutCancel(ctx), req.Query, options...)
	duration := int(time.Since(start).Milliseconds())
	s.recordExecution(ctx, executionID, agentID, req.UserID, a.GetConfig().Name, req.Query, result, err, duration)
	return result, duration, err
}

// assembleOptions builds the ExecutionOption slice and resolves the
// per-tenant CapabilityGateway. When meta.Stream is true, the returned
// ctx carries the per-tenant LLM completer for streaming inner calls.
func (s *AgentService) assembleOptions(
	ctx context.Context, a Agent, req ExecRequest, meta ExecMeta, executionID string,
) (context.Context, []ExecutionOption) {
	options := []ExecutionOption{WithMaxSteps(a.GetConfig().MaxIterations)}
	if req.MaxSteps > 0 {
		options = append(options, WithMaxSteps(req.MaxSteps))
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
			if len(apiKeys) > 0 {
				options = append(options, WithLLMAPIKeys(apiKeys))
			}
		}
	}
	s.attachChatStore(a)
	s.attachTraceStores(a)

	options = append(options,
		WithTenantID(meta.TenantID),
		WithTraceID(meta.TraceID),
		WithUserID(req.UserID),
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
	extraTools, skillCatalog := s.buildExtraTools(
		ctx, meta.TenantID, subjectID, a.GetConfig().MCPToolIDs, a.GetConfig().AllowedSkills,
	)
	options = append(options,
		WithExtraTools(extraTools),
		WithSkillCatalog(skillCatalog),
	)
	if s.deps.ApprovalService != nil {
		approvalService := s.deps.ApprovalService
		agentID, userID, conversationID, query := a.GetConfig().ID, req.UserID, req.ConversationID, req.Query
		pinned := make(map[string]string, len(skillCatalog))
		for skillID, activation := range skillCatalog {
			pinned[skillID] = activation.RevisionID
		}
		options = append(options, WithApprovalRequestFn(func(actx context.Context, request port.ToolApprovalRequest) (string, error) {
			return approvalService.Request(actx, ToolApprovalPayload{
				TenantID: meta.TenantID, ExecutionID: executionID, TraceID: meta.TraceID, AgentID: agentID, UserID: userID, ConversationID: conversationID,
				ToolCallID: request.ToolCallID, ServerID: request.ServerID, ToolName: request.ToolName, RiskLevel: request.RiskLevel,
				Query: query, Arguments: request.Arguments, PinnedSkillRevisions: pinned,
			})
		}))
	}
	if s.deps.MCPToolExecutor != nil {
		executor := s.deps.MCPToolExecutor
		options = append(options, WithToolCallFn(executor.ExecuteMCPTool))
	}
	if s.deps.RAGSearch != nil && len(a.GetConfig().KnowledgeWorkspaceIDs) > 0 {
		tenantID := meta.TenantID
		options = append(options, WithRAGSearchFn(func(rctx context.Context, workspaces []string, query string, topK int) (string, error) {
			return s.deps.RAGSearch.SearchKnowledge(rctx, tenantID, workspaces, query, topK)
		}))
	}
	return ctx, options
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

func (s *AgentService) attachTraceStores(a Agent) {
	if s.deps.ToolTraceStore != nil {
		type toolTraceStoreSetter interface {
			SetToolTraceStore(ToolTraceStore)
		}
		if setter, ok := a.(toolTraceStoreSetter); ok {
			setter.SetToolTraceStore(s.deps.ToolTraceStore)
		}
	}
	if s.deps.TraceEventStore != nil {
		type traceEventStoreSetter interface {
			SetTraceEventStore(TraceEventStore)
		}
		if setter, ok := a.(traceEventStoreSetter); ok {
			setter.SetTraceEventStore(s.deps.TraceEventStore)
		}
	}
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
			if s.deps.MCPToolPolicy != nil {
				resolved, err := s.deps.MCPToolPolicy.ResolveMCPToolRisk(ctx, tenantID, serverID, tool.CapabilityID)
				if err == nil && resolved != "" {
					risk = resolved
				}
			}
			tool.Metadata["risk_level"] = string(risk)
			tools = append(tools, tool)
		}
	}

	for _, skillID := range allowedSkills {
		ref := port.SkillRevisionRef{SkillID: skillID}
		if s.deps.SkillRevisionResolver != nil {
			if revisionID, found, err := s.deps.SkillRevisionResolver.ResolveSkillRevision(ctx, tenantID, skillID, subjectID); err == nil && found {
				ref.RevisionID = revisionID
			}
		}
		refs = append(refs, ref)
	}
	catalog := make(map[string]port.SkillActivation)
	if s.deps.SkillActivationResolver != nil && len(refs) > 0 {
		if resolved, err := s.deps.SkillActivationResolver.ResolveSkills(ctx, tenantID, refs); err == nil {
			catalog = resolved
		}
	}
	return tools, catalog
}

// recordExecution fire-and-forget inserts a per-tenant execution record.
// The insert reuses reqCtx — which carries the tenant — but detaches its
// cancel signal so the goroutine survives the HTTP response lifecycle.
func (s *AgentService) recordExecution(
	reqCtx context.Context, executionID, id, userID, agentName, query string,
	result *AgentResult, err error, durationMs int,
) {
	if s.deps.ExecStore == nil {
		return
	}
	rec := domain.ExecutionRecord{
		ID:           executionID,
		AgentID:      id,
		TraceID:      traceIDFromContext(reqCtx),
		UserID:       userID,
		AgentName:    agentName,
		InputPreview: truncateRunes(query, previewMaxChars),
		DurationMs:   durationMs,
	}
	switch {
	case isToolApprovalRequired(err):
		rec.Status = domain.ExecStatusWaitingApproval
	case err != nil:
		rec.Status = domain.ExecStatusError
		rec.ErrorMessage = err.Error()
	case result != nil:
		rec.Status = domain.ExecStatusSuccess
		rec.OutputPreview = truncateRunes(result.Output, previewMaxChars)
		rec.TotalTokens = result.TokensUsed
	default:
		rec.Status = domain.ExecStatusSuccess
	}
	insertCtx := context.WithoutCancel(reqCtx)
	go func() {
		if insertErr := s.deps.ExecStore.Insert(insertCtx, rec); insertErr != nil {
			s.deps.Logger.Warn("execution record insert failed", zap.Error(insertErr))
		}
	}()
}

func isToolApprovalRequired(err error) bool {
	var approvalErr *port.ToolApprovalRequiredError
	return errors.As(err, &approvalErr)
}

func (s *AgentService) ListToolTraces(ctx context.Context, tenantID, traceID string) ([]ToolObservation, error) {
	if s.deps.ToolTraceStore == nil {
		return []ToolObservation{}, nil
	}
	return s.deps.ToolTraceStore.ListByTraceID(ctx, tenantID, traceID)
}

func (s *AgentService) ListTraceEvents(ctx context.Context, tenantID, traceID string) ([]AgentTraceEvent, error) {
	if s.deps.TraceEventStore == nil {
		return []AgentTraceEvent{}, nil
	}
	return s.deps.TraceEventStore.ListByTraceID(ctx, tenantID, traceID)
}

func traceIDFromContext(ctx context.Context) string {
	if sc, ok := observability.SpanFromContext(ctx); ok {
		return sc.TraceID
	}
	return ""
}

// truncateRunes returns s truncated to maxRunes runes (not bytes).
func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes])
}
