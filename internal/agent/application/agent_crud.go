// Agent CRUD, configuration building and memory-parameter merging.

package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

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
	a := NewBaseAgent(cfg, s.deps.Logger)
	if s.deps.Metrics != nil {
		a = a.WithMetrics(s.deps.Metrics)
	}
	if s.deps.Ledger != nil {
		a = a.WithLedger(s.deps.Ledger)
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

func (s *AgentService) Delete(ctx context.Context, tenantID, id, actorID string) error {
	existing, ok, err := s.deps.Registry.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("delete agent: load managed identity: %w", err)
	}
	if !ok {
		return ErrNotFound
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

// GrantEditorOnApproval grants editor whitelist access to editorID after an
// approved grant_editor proposal. The approval executes as the operation gate
// system actor: ownership was adjudicated by the human approver, and the
// audit row records the system actor with the proposal carrying
// proposer/reviewer provenance (same pattern as gated_self_modify's approved
// replay). Already-whitelisted ids are a no-op (idempotent grant).

func (s *AgentService) GrantEditorOnApproval(ctx context.Context, tenantID, agentID, editorID string) error {
	if s.deps.ResourceEditorRepo == nil {
		return fmt.Errorf("agent grant editor: editor repo not wired")
	}
	if editorID == "" {
		return fmt.Errorf("agent grant editor: empty editor id")
	}
	// Atomic single-row grant (INSERT ... ON CONFLICT DO NOTHING, eligibility
	// re-checked inside the repository transaction). This replaces the previous
	// list-then-replace read-modify-write, which could drop a concurrent grant
	// on the same resource (two approvals racing would clobber each other).
	ctx = reqctx.WithSystemActor(ctx, operationGateActor)
	if err := s.deps.ResourceEditorRepo.AddEditorForKind(ctx, tenantID, "agent", agentID, editorID, operationGateActor); err != nil {
		return fmt.Errorf("agent grant editor: %w", err)
	}
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
