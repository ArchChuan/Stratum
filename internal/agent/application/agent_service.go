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
	Registry              *Registry
	TenantSettings        port.TenantSettings
	SkillLookup           port.SkillLookup
	SkillToolResolver     port.SkillToolResolver
	SkillRevisionResolver port.SkillRevisionResolver
	RAGSearch             port.RAGSearchProvider
	TenantResolver        port.TenantCapabilityResolver
	MCPTools              port.MCPToolProvider
	ExecStore             ExecutionStore
	ChatStore             ChatStore
	ToolTraceStore        ToolTraceStore
	TraceEventStore       TraceEventStore
	CheckpointStore       CheckpointStore
	MemoryCleaner         port.AgentMemoryCleaner
	MemoryBuffer          port.BufferMemoryFn
	Metrics               observability.MetricsProvider
	Logger                *zap.Logger
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
	MCPServerIDs          []string
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
	MCPServerIDs          []string
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
	MCPServerIDs          []string
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
		MCPServerIDs:          in.MCPServerIDs,
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
		MCPServerIDs:          in.MCPServerIDs,
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
	if err := s.deps.Registry.Remove(ctx, id); err != nil {
		return err
	}
	if s.deps.MemoryCleaner != nil {
		_ = s.deps.MemoryCleaner.ClearAgentMemories(ctx, tenantID, id)
	}
	if s.deps.ChatStore != nil {
		_ = s.deps.ChatStore.DeleteByAgent(ctx, tenantID, id)
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
		MCPServerIDs:          cfg.MCPServerIDs,
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
	_, options := s.assembleOptions(ctx, a, req, meta)
	executionID := uuid.Must(uuid.NewV7()).String()
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
	streamCtx, options := s.assembleOptions(ctx, a, req, meta)
	executionID := uuid.Must(uuid.NewV7()).String()
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

// assembleOptions builds the ExecutionOption slice and resolves the
// per-tenant CapabilityGateway. When meta.Stream is true, the returned
// ctx carries the per-tenant LLM completer for streaming inner calls.
func (s *AgentService) assembleOptions(
	ctx context.Context, a Agent, req ExecRequest, meta ExecMeta,
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
	extraTools, skillIndex := s.buildExtraTools(
		ctx, meta.TenantID, subjectID, a.GetConfig().MCPServerIDs, a.GetConfig().AllowedSkills,
	)
	options = append(options,
		WithExtraTools(extraTools),
		WithSkillToolIndex(skillIndex),
	)
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

// buildExtraTools converts MCPServerIDs and AllowedSkills into ToolDefinitions
// for the ReAct loop. Published skills use their tool contract names; legacy
// skills fall back to tenant-scoped names. The returned index maps tool names
// back to skill/version refs for execution routing.
func (s *AgentService) buildExtraTools(
	ctx context.Context,
	tenantID, subjectID string,
	mcpServerIDs, allowedSkills []string,
) ([]port.ToolDefinition, map[string]port.SkillToolRef) {
	var tools []port.ToolDefinition
	index := make(map[string]port.SkillToolRef, len(allowedSkills))

	for _, serverID := range mcpServerIDs {
		if s.deps.MCPTools == nil {
			continue
		}
		for _, tool := range s.deps.MCPTools.ToolsForServer(ctx, serverID) {
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
			tools = append(tools, tool)
		}
	}

	if s.deps.SkillToolResolver != nil && len(allowedSkills) > 0 {
		resolvedTools, resolvedIndex, err := s.deps.SkillToolResolver.ResolveTools(ctx, tenantID, allowedSkills)
		if err == nil {
			for i := range resolvedTools {
				ref, ok := resolvedIndex[resolvedTools[i].Name]
				if ok {
					resolvedTools[i].ProviderType = domain.ProviderTypeSkill
					resolvedTools[i].ProviderID = ref.SkillID
					resolvedTools[i].CapabilityID = ref.SkillID
					resolvedTools[i].NodeType = domain.ObservationTypeSkill
					resolvedTools[i].Metadata = map[string]any{"version_id": ref.VersionID}
				}
			}
			tools = append(tools, resolvedTools...)
			for name, ref := range resolvedIndex {
				if s.deps.SkillRevisionResolver != nil {
					if revisionID, found, resolveErr := s.deps.SkillRevisionResolver.ResolveSkillRevision(
						ctx, tenantID, ref.SkillID, subjectID,
					); resolveErr == nil && found {
						ref.VersionID = revisionID
					}
				}
				index[name] = ref
			}
			return tools, index
		}
	}

	for _, skillID := range allowedSkills {
		name := skillID
		description := skillID
		if s.deps.SkillLookup != nil && tenantID != "" {
			if n, d, err := s.deps.SkillLookup.LookupSkill(ctx, tenantID, skillID); err == nil && n != "" {
				name = n
				description = d
			}
		}
		toolName := fmt.Sprintf("tenant_%s_%s", tenantID, name)
		index[toolName] = port.SkillToolRef{SkillID: skillID}
		tools = append(tools, port.ToolDefinition{
			Name:         toolName,
			Description:  name + ": " + description,
			ProviderType: domain.ProviderTypeSkill,
			ProviderID:   skillID,
			CapabilityID: skillID,
			NodeType:     domain.ObservationTypeSkill,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{
						"type":        "string",
						"description": "需要 skill 处理的文本输入",
					},
				},
				"required": []string{"prompt"},
			},
		})
	}
	return tools, index
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
