// Execution-option assembly (assembleOptions, RAG/factcheck/params).

package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/application/factcheck"
	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

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
	// D16：模型校验对所有 agent 生效（平台助手等同化，普通 agent 同样要求
	// 模型在租户可用列表）。空 model / validator 缺失 → ErrAssistantModelUnavailable
	// (503)；模型不在租户列表 → ErrInvalidAgentModel → 503（执行期请求不合法模型）。
	model := strings.TrimSpace(a.GetConfig().LLMModel)
	if model == "" || s.deps.TenantModelValidator == nil {
		return ctx, nil, domain.ErrAssistantModelUnavailable
	}
	if err := s.deps.TenantModelValidator.ValidateTenantChatModel(ctx, meta.TenantID, model); err != nil {
		if errors.Is(err, domain.ErrInvalidAgentModel) {
			return ctx, nil, domain.ErrAssistantModelUnavailable
		}
		return ctx, nil, fmt.Errorf("assemble agent model: %w", err)
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
		ctx, meta, req, a, subjectID, &authorization,
	)
	if toolingErr != nil {
		return ctx, nil, toolingErr
	}
	evolutionTrace := meta.EvolutionTrace
	if evolutionTrace.ResourceManifest == nil {
		evolutionTrace.ResourceManifest = make(map[string]string)
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
		options = appendRAGSearchOptions(options, meta.TenantID, s.deps.RAGSearch, knowledgeAssignments)
	}
	options = applyFactCheckOption(options, s.deps.FactCheck)
	// 所有 agent 统一装配内部工具结果 guard：RAG/recall/8 运维工具结果的
	// <untrusted_tool_result> 标记依赖 InternalToolResultGuardFn，漏装配会让
	// 这些工具在 guard 上 fail-closed 报错。无条件装配，对无 RAG agent 无害。
	options = append(options, withAssistantRoleClass(roleClass),
		withInternalToolResultGuard(makeInternalToolResultGuard(NewToolResultGuard())))
	// 8 个内置运维工具的 in-process 能力闭包对所有 agent 注入（等化后通用；
	// 写路径角色门禁在闭包内按 roleClass fail-closed）。
	options = append(options, s.assistantExecutionOptions(ctx, meta, req, roleClass, authorization,
		domain.CurrentExecutionArtifactProfileVersion, a.GetConfig().ID)...)
	return ctx, s.resolveEffectiveParameters(ctx, a, options), nil
}

// applyFactCheckOption 透传幻觉校验 option（fail-closed：nil/disabled 不注入）。
// judge 与 TopK 等由 wiring 装配；EvidenceFn 在 collectGraphResult 填充。

func applyFactCheckOption(options []ExecutionOption, settings *factcheck.Settings) []ExecutionOption {
	if settings == nil || (!settings.Enabled && !settings.CitationVerify) {
		return options
	}
	return append(options, WithFactCheck(settings))
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
		var noAnswer *domain.NoAnswerInfo
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
			// 聚合无答案信号：revision 部分无信号，证据部分信号在
			// sources 仍为空时透传（revision 有内容即视为有答案）。
			if len(sources) == 0 && ev.NoAnswer != nil {
				noAnswer = ev.NoAnswer
			}
		}
		return port.RAGSearchEvidence{Content: combined.String(), Sources: sources, NoAnswer: noAnswer}, nil
	}))
}

// resolveTooling resolves the skill/tool catalog for an agent: the per-tenant
// buildExtraToolsChecked plus the 8 generic in-process tools, authorizing the
// current member for the role class that gates the write paths. 平台助手与
// 普通 Agent 等同化后统一走此路径（能力拉起，不区分对待）。

func (s *AgentService) resolveTooling(
	ctx context.Context, meta ExecMeta, req ExecRequest, a Agent, subjectID string,
	authorization *domain.DiagnosticAuthorization,
) (extraTools []port.ToolDefinition, skillCatalog map[string]port.SkillActivation, roleClass string, err error) {
	extraTools, skillCatalog, err = s.buildExtraToolsChecked(
		ctx, meta.TenantID, subjectID, a.GetConfig().MCPToolIDs, a.GetConfig().AllowedSkills,
	)
	if err != nil {
		return nil, nil, "", fmt.Errorf("resolve experiment resources: %w", err)
	}
	// 8 个内置运维工具对所有 agent 通用。角色门禁统一由 DiagnosticProvider
	// 解析：写操作 admin/owner、member 在装配闭包内 fail-closed；只读
	// list 类所有角色可用。
	extraTools = append(extraTools, SystemAssistantToolDefinitions()...)
	roleClass = "unknown"
	if s.deps.DiagnosticProvider != nil {
		var authorizeErr error
		*authorization, authorizeErr = s.deps.DiagnosticProvider.Authorize(ctx, domain.DiagnosticRequest{
			TenantID: meta.TenantID, UserID: req.UserID,
			Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent, domain.DiagnosticAreaSkill, domain.DiagnosticAreaMCP, domain.DiagnosticAreaKnowledge, domain.DiagnosticAreaModel},
		})
		if authorizeErr != nil {
			// D18：无 HTTP actor 的内部执行（revision/评估/工作流/collab）没有
			// 真实用户身份——UserID 为空或是合成标识（"collab:"+planID、
			// "workflow"），Authorize 失败回退 member（self 范围），禁止
			// fail-open 但也不阻断合法内部流程；HTTP 执行带真实 UUID actor
			// （JWT sub 派生），维持 fail-closed。
			if !isRealActor(req.UserID) {
				roleClass = "member"
				*authorization = domain.DiagnosticAuthorization{RoleClass: "member"}
			} else {
				return nil, nil, "", authorizeErr
			}
		} else {
			roleClass = boundedAssistantRoleClass(authorization.RoleClass)
		}
	}
	return extraTools, skillCatalog, roleClass, nil
}

// isRealActor 判断 UserID 是否为真实租户成员身份（UUID）。内部执行
// （collab/workflow/revision/评估）使用合成标识（"collab:"+planID、
// "workflow"）或空串，不参与角色门禁：Authorize 失败回退 member，禁止
// fail-open 但也不阻断合法内部流程；真实 HTTP actor 由 JWT sub 派生且
// 恒为 UUID，维持 fail-closed。

func isRealActor(userID string) bool {
	id := strings.TrimSpace(userID)
	if id == "" {
		return false
	}
	_, err := uuid.Parse(id)
	return err == nil
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

// assistantExecutionOptions attaches the in-process capability callbacks
// (official docs search, tenant diagnostics, governed proposals, model/agent/
// MCP listing) shared by all agents. 平台助手等同化后 8 运维工具对所有 agent
// 通用，roleClass 门禁在写路径闭包内 fail-closed。
