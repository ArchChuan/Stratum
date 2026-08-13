package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// toolExecResult captures the outcome of a single tool call execution.
type toolExecResult struct {
	content      string
	status       string
	errMsg       string
	fatalToolErr error
	artifact     *domain.SystemAssistantToolArtifact
	// evidence is retrieval provenance (e.g. RAG sources) merged into the
	// tool observation metadata so traces record where tool content came from.
	evidence map[string]any
}

func makeToolNode(capGW port.CapabilityGateway, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if len(s.Messages) == 0 {
			return s, nil
		}
		tracer := otel.Tracer("stratum/agent")
		last := s.Messages[len(s.Messages)-1]
		for _, tc := range last.ToolCalls {
			toolStart := time.Now()
			provider := classifyToolProvider(tc.Name, s.AvailableTools)
			argumentsPayload := observability.SafeTracePayload(tc.Arguments, constants.AgentToolTraceMaxRawJSONBytes)
			toolAttributes := buildToolAttributes(tc, s, provider, argumentsPayload)
			if !s.GovernedAssistant {
				toolAttributes = append(toolAttributes, tracePayloadAttributes(
					ctx, s.TracePayloadStore, s.TenantID, s.TraceID, "tool-arguments", tc.Arguments,
				)...)
			}
			toolCtx, toolSpan := tracer.Start(ctx, "react.tool",
				oteltrace.WithAttributes(toolAttributes...),
			)
			s.TraceEvents = append(s.TraceEvents, buildToolStartedEvent(s, tc, provider, toolStart))
			result := dispatchToolCall(toolCtx, tc, &s, toolStart, provider, logger)
			if tc.ID == s.PlanContinueCallID {
				// 排程化的 continue：执行已在此完成（含 rev checkpoint），
				// 波次观察由 finalize 节点在汇合后补全，这里跳过 observation
				// 与直接消息追加；trace 与 AllToolCalls 审计保留。
				toolSpan.End()
				s.AllToolCalls = append(s.AllToolCalls, tc)
				if result.fatalToolErr != nil {
					return s, result.fatalToolErr
				}
				continue
			}
			if result.artifact != nil {
				s.AssistantToolArtifacts = append(s.AssistantToolArtifacts, *result.artifact)
			}
			recordToolErrorArtifact(&s, provider.CapabilityID, toolStart, result)
			toolLatencyMs := time.Since(toolStart).Milliseconds()
			recordToolSpanResult(toolSpan, result.errMsg, result.content, toolLatencyMs)
			if !s.GovernedAssistant {
				toolSpan.SetAttributes(tracePayloadAttributes(
					toolCtx, s.TracePayloadStore, s.TenantID, s.TraceID, "tool-result", result.content,
				)...)
			}
			appendToolObservation(&s, tc, provider, result, toolStart, toolLatencyMs)
			appendToolTraceEvent(&s, tc, provider, result, toolStart, toolLatencyMs)
			s.Messages = append(s.Messages, port.LLMMessage{
				Role:       "tool",
				Content:    result.content,
				ToolCallID: tc.ID,
			})
			toolSpan.End()
			s.AllToolCalls = append(s.AllToolCalls, tc)
			if result.fatalToolErr != nil {
				return s, result.fatalToolErr
			}
		}
		return s, nil
	}
}

func buildToolAttributes(tc port.ToolCall, s ReActState, provider toolProviderRef, argumentsPayload observability.TracePayload) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("tool.name", tc.Name),
		attribute.String("gen_ai.tool.name", tc.Name),
		attribute.String("gen_ai.tool.call.id", tc.ID),
		attribute.Int("react.step", s.Steps),
		attribute.String("stratum.provider.type", provider.ProviderType),
		attribute.String("stratum.provider.id", provider.ProviderID),
		attribute.String("stratum.server.id", provider.ServerID),
		attribute.String("stratum.capability.id", provider.CapabilityID),
		attribute.String("stratum.resource.revision_id", metadataString(provider.Metadata, "version_id")),
		attribute.String("stratum.arguments.sha256", argumentsPayload.SHA256),
		attribute.Bool("stratum.arguments.truncated", argumentsPayload.Truncated),
		attribute.String("opik.metadata.stratum.tenant_id", s.TenantID),
		attribute.String("opik.metadata.stratum.trace_id", s.TraceID),
		attribute.String("opik.metadata.stratum.tool_call_id", tc.ID),
		attribute.String("opik.metadata.stratum.tool_name", tc.Name),
		attribute.String("opik.metadata.stratum.provider_type", provider.ProviderType),
		attribute.String("opik.metadata.stratum.provider_id", provider.ProviderID),
		attribute.String("opik.metadata.stratum.server_id", provider.ServerID),
		attribute.String("opik.metadata.stratum.capability_id", provider.CapabilityID),
		attribute.String("opik.metadata.stratum.resource_revision_id", metadataString(provider.Metadata, "version_id")),
	}
}

func buildToolStartedEvent(s ReActState, tc port.ToolCall, provider toolProviderRef, toolStart time.Time) domain.AgentTraceEvent {
	return domain.AgentTraceEvent{
		TraceID:         s.TraceID,
		ConversationID:  s.ConversationID,
		RunType:         domain.RunTypeAgent,
		ObservationType: domain.ObservationTypeTool,
		EventType:       domain.TraceEventToolStarted,
		StepIndex:       s.Steps,
		SpanName:        "react.tool",
		Status:          domain.ToolTraceStatusSuccess,
		ProviderType:    provider.ProviderType,
		ProviderID:      provider.ProviderID,
		NodeID:          provider.NodeID,
		NodeType:        provider.NodeType,
		Input:           map[string]any{"tool_call_id": tc.ID, "tool_name": tc.Name, "arguments": tc.Arguments},
		Summary:         fmt.Sprintf("calling tool %s", tc.Name),
		StartedAt:       toolStart,
		EndedAt:         toolStart,
	}
}

func dispatchToolCall(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time, provider toolProviderRef, logger *zap.Logger) toolExecResult {
	if result, matched := dispatchSkillTool(tc, s); matched {
		return result
	}
	switch tc.Name {
	case "stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan":
		return execPlanTool(toolCtx, tc, s)
	case "stratum_continue_reasoning":
		return toolExecResult{content: "Continuing reasoning...", status: domain.ToolTraceStatusSuccess}
	case "stratum_search_knowledge":
		return execSearchKnowledgeTool(toolCtx, tc, s, toolStart, logger)
	case "stratum_recall_memory":
		return execRecallMemoryTool(toolCtx, tc, s, toolStart, logger)
	default:
		if result, matched := dispatchSystemAssistantTool(toolCtx, tc, s, toolStart); matched {
			return result
		}
		return execMCPTool(toolCtx, tc, s, toolStart, provider, logger)
	}
}

// dispatchSystemAssistantTool 分发系统助手内置工具（官方文档检索、
// 租户诊断、资源变更提案/应用、模型清单/模型更新），避免主分发的
// switch case 数推高圈复杂度。
func dispatchSystemAssistantTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) (toolExecResult, bool) {
	switch tc.Name {
	case domain.SystemAssistantToolSearchOfficialDocs:
		return execOfficialDocsSearchTool(toolCtx, tc, s, toolStart), true
	case domain.SystemAssistantToolDiagnoseTenant:
		return execDiagnoseTenantTool(toolCtx, tc, s, toolStart), true
	case domain.SystemAssistantToolProposeResourceChange:
		return execProposeResourceChangeTool(toolCtx, tc, s, toolStart), true
	case domain.SystemAssistantToolApplyResourceChange:
		return execApplyResourceChangeTool(toolCtx, tc, s, toolStart), true
	case domain.SystemAssistantToolListModels:
		return execListModelsTool(toolCtx, tc, s, toolStart), true
	case domain.SystemAssistantToolUpdateSystemModel:
		return execUpdateSystemModelTool(toolCtx, tc, s, toolStart), true
	case domain.SystemAssistantToolListAgents:
		return execListAgentsTool(toolCtx, tc, s, toolStart), true
	case domain.SystemAssistantToolListMCPServers:
		return execListMCPServersTool(toolCtx, tc, s, toolStart), true
	}
	return toolExecResult{}, false
}

func execOfficialDocsSearchTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.OfficialDocsSearchFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "official docs tool unavailable", content: "error: tool unavailable"}
	}
	query, parseErr := domain.ParseOfficialDocsToolArguments(tc.Arguments)
	if parseErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: parseErr.Error(), content: "error: invalid tool arguments"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	citations, callErr := s.OfficialDocsSearchFn(callCtx, query)
	cancel()
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
	}
	citations = domain.BoundCitations(citations)
	content, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, map[string]any{"citations": citations})
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: content,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, Citations: citations, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

func execDiagnoseTenantTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.DiagnosticFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "diagnostic tool unavailable", content: "error: tool unavailable"}
	}
	areas, parseErr := domain.ParseDiagnosticToolArguments(tc.Arguments)
	if parseErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: parseErr.Error(), content: "error: invalid tool arguments"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	evidence, callErr := s.DiagnosticFn(callCtx, areas)
	cancel()
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
	}
	evidence = domain.BoundDiagnosticEvidence(evidence)
	content, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, map[string]any{"evidence": evidence})
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: content,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, Evidence: &evidence, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

// execListModelsTool 只读：返回当前租户全量可配置模型清单（含停用/
// embedding，标注 enabled 与能力）。对任意角色可用，结果经守卫限界。
func execListModelsTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.ListModelsFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "list models tool unavailable", content: "error: tool unavailable"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	content, callErr := s.ListModelsFn(callCtx)
	cancel()
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
	}
	guarded, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, content)
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: guarded,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

// execListAgentsTool 只读：返回当前租户 agent 目录的安全投影
// （id/name/type/description/model 等，不含 systemPrompt/systemKey）。
// 对任意角色可用，结果经守卫限界。
func execListAgentsTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.ListAgentsFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "list agents tool unavailable", content: "error: tool unavailable"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	content, callErr := s.ListAgentsFn(callCtx)
	cancel()
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
	}
	guarded, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, content)
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: guarded,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

// execListMCPServersTool 只读：返回当前租户已连接 MCP server 的摘要投影
// （名称/状态/传输/工具名列表，不携带工具 InputSchema/OutputSchema 等内部
// 契约）。对任意角色可用，结果经守卫限界。
func execListMCPServersTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.ListMCPServersFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "list mcp servers tool unavailable", content: "error: tool unavailable"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	content, callErr := s.ListMCPServersFn(callCtx)
	cancel()
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
	}
	guarded, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, content)
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: guarded,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

// execUpdateSystemModelTool 写路径：model 参数必填；角色门禁（member
// 拒绝）位于装配闭包内，graph 层只负责参数校验与守卫。
func execUpdateSystemModelTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.UpdateSystemModelFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "update system model tool unavailable", content: "error: tool unavailable"}
	}
	model, _ := tc.Arguments["model"].(string)
	if model == "" {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "invalid tool arguments", content: "error: invalid tool arguments: model required"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	content, callErr := s.UpdateSystemModelFn(callCtx, model)
	cancel()
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
	}
	guarded, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, content)
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: guarded,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

func execProposeResourceChangeTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.ProposalCreateFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "proposal tool unavailable", content: "error: tool unavailable"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	defer cancel()
	proposal, callErr := s.ProposalCreateFn(callCtx, tc.Arguments)
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		if proposal.ID != "" {
			s.AssistantToolArtifacts = append(s.AssistantToolArtifacts, domain.SystemAssistantToolArtifact{
				Tool: tc.Name, Proposal: &proposal, LatencyMs: time.Since(toolStart).Milliseconds(),
				Outcome: "error", ErrorCode: assistantToolErrorCode(message),
			})
		}
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
	}
	content, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, map[string]any{"proposal": proposal})
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: content,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, Proposal: &proposal, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

func execApplyResourceChangeTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.ResourceChangeApplyFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "resource change apply tool unavailable", content: "error: tool unavailable"}
	}
	// Artifact-only extraction: strict argument validation (whitelist fields,
	// payload decode) happens inside ApplyDirect. Here we only read the three
	// display fields so an invalid call still produces a typed error artifact.
	kind, _ := tc.Arguments["resourceKind"].(string)
	operation, _ := tc.Arguments["operation"].(string)
	resourceID, _ := tc.Arguments["resourceId"].(string)
	callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
	result, callErr := s.ResourceChangeApplyFn(callCtx, tc.Arguments)
	cancel()
	artifact := &domain.SystemAssistantDirectApplyArtifact{
		Tool: tc.Name, ResourceKind: domain.ResourceKind(kind), Operation: domain.ProposalOperation(operation),
		ResourceID: resourceID, Outcome: "success",
	}
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		artifact.Outcome = "error"
		artifact.ErrorCode = assistantToolErrorCode(message)
		return toolExecResult{
			content:  "error: " + message,
			status:   domain.ToolTraceStatusError,
			errMsg:   message,
			artifact: &domain.SystemAssistantToolArtifact{Tool: tc.Name, DirectApply: artifact, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "error", ErrorCode: artifact.ErrorCode},
		}
	}
	content, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, map[string]any{"result": result})
	if guardErr != nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
	}
	return toolExecResult{
		content: content,
		status:  domain.ToolTraceStatusSuccess,
		artifact: &domain.SystemAssistantToolArtifact{
			Tool: tc.Name, DirectApply: artifact, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
		},
	}
}

// dispatchSkillTool matches a tool call against the skill catalog.
// The tool name is the ActivationContract name, not a hardcoded prefix.
func dispatchSkillTool(tc port.ToolCall, s *ReActState) (toolExecResult, bool) {
	for _, activation := range s.SkillCatalog {
		if activation.Name == tc.Name || (activation.Name == "" && activation.SkillID == tc.Name) {
			s.Actives = upsertActivation(s.Actives, activation)
			return toolExecResult{
				content: fmt.Sprintf("activated skill %s revision %s", activation.SkillID, activation.RevisionID),
				status:  domain.ToolTraceStatusSuccess,
			}, true
		}
	}
	return toolExecResult{}, false
}

func execPlanTool(toolCtx context.Context, tc port.ToolCall, s *ReActState) toolExecResult {
	content, planErr := ExecutePlanTool(toolCtx, s, tc)
	if planErr != nil {
		return toolExecResult{content: content, status: domain.ToolTraceStatusError, errMsg: planErr.Error()}
	}
	return toolExecResult{content: content, status: domain.ToolTraceStatusSuccess}
}

func execSearchKnowledgeTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time, logger *zap.Logger) toolExecResult {
	if s.RAGSearchFn == nil && s.RAGSearchFnWithEvidence == nil {
		return toolExecResult{content: "error: stratum_search_knowledge tool not configured", status: domain.ToolTraceStatusSuccess}
	}
	workspaces := extractStringSliceArg(tc.Arguments, "workspaces")
	query, _ := tc.Arguments["query"].(string)
	workspaces = allowedKnowledgeWorkspaces(workspaces, s.AgentKnowledgeWorkspaceIDs, s.Actives)
	if len(workspaces) == 0 {
		msg := "error: no authorized knowledge workspace"
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: msg, content: msg}
	}
	topK := clampTopK(tc.Arguments)
	ragCtx, ragCancel := context.WithTimeout(toolCtx, constants.AgentRAGSearchTimeout)
	var content string
	var evidence map[string]any
	var ragErr error
	// evidenceRes carries the raw evidence for citation aggregation on the
	// success path; zero-valued (empty sources) on the plain-search branch,
	// where appendCitationSources is a no-op.
	var evidenceRes port.RAGSearchEvidence
	if s.RAGSearchFnWithEvidence != nil {
		// Evidence-capable search: provenance travels with the content. The
		// viewer identity is threaded through state so the tool node stays
		// signature-compatible with plain and evidence variants alike.
		evidenceRes, ragErr = s.RAGSearchFnWithEvidence(ragCtx, workspaces, query, topK, s.ViewerID)
		content = evidenceRes.Content
		evidence = ragEvidenceToMetadata(evidenceRes)
	} else {
		content, ragErr = s.RAGSearchFn(ragCtx, workspaces, query, topK, s.ViewerID)
	}
	ragCancel()
	if ragErr != nil {
		r := toolExecResult{status: domain.ToolTraceStatusError, errMsg: ragErr.Error(), content: fmt.Sprintf("error: %v", ragErr)}
		if errors.Is(ragErr, domain.ErrKnowledgeRevisionUnavailable) {
			r.fatalToolErr = ragErr
		}
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", time.Since(toolStart).Milliseconds()))
		return r
	}
	logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
		zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
		zap.Int64("latency_ms", time.Since(toolStart).Milliseconds()))
	s.appendCitationSources(evidenceRes)
	return toolExecResult{content: content, status: domain.ToolTraceStatusSuccess, evidence: evidence}
}

// appendCitationSources merges retrieval evidence into the execution's
// citation list for the chat UI: deduplicated by chunk ID (newest search
// wins), capped at MaxAgentResultSources. Only evidence-capable searches
// reach here, so the list is already viewer-filtered by the knowledge side.
func (s *ReActState) appendCitationSources(evidence port.RAGSearchEvidence) {
	if len(evidence.Sources) == 0 {
		return
	}
	seen := make(map[string]bool, len(s.CitationSources)+len(evidence.Sources))
	for _, existing := range s.CitationSources {
		seen[existing.ChunkID] = true
	}
	var fresh []port.RAGSearchSource
	for _, src := range evidence.Sources {
		if src.ChunkID == "" || seen[src.ChunkID] {
			continue
		}
		seen[src.ChunkID] = true
		fresh = append(fresh, src)
	}
	if len(fresh) == 0 {
		return
	}
	s.CitationSources = append(s.CitationSources, fresh...)
	if len(s.CitationSources) > constants.MaxAgentResultSources {
		s.CitationSources = s.CitationSources[len(s.CitationSources)-constants.MaxAgentResultSources:]
	}
}

func execRecallMemoryTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time, logger *zap.Logger) toolExecResult {
	switch {
	case len(s.Actives) > 0 && !anyActiveAllowsMemoryScope(s.Actives, s.AgentMemoryScope):
		msg := "error: active skill does not permit this memory scope"
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", time.Since(toolStart).Milliseconds()))
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: msg, content: msg}
	case s.RecallMemoryFn == nil:
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", time.Since(toolStart).Milliseconds()))
		return toolExecResult{content: "error: stratum_recall_memory tool not configured", status: domain.ToolTraceStatusSuccess}
	default:
		recallCtx, recallCancel := context.WithTimeout(toolCtx, constants.AgentMemoryRecallTimeout)
		content, recallErr := s.RecallMemoryFn(recallCtx, tc.Arguments)
		recallCancel()
		if recallErr != nil {
			logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
				zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
				zap.Int64("latency_ms", time.Since(toolStart).Milliseconds()))
			return toolExecResult{status: domain.ToolTraceStatusError, errMsg: recallErr.Error(), content: fmt.Sprintf("error: %v", recallErr)}
		}
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", time.Since(toolStart).Milliseconds()))
		return toolExecResult{content: content, status: domain.ToolTraceStatusSuccess}
	}
}

func execMCPTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time, provider toolProviderRef, logger *zap.Logger) toolExecResult {
	tool, ok := findTool(tc.Name, s.AvailableTools)
	if !ok || provider.ProviderType != domain.ProviderTypeMCP {
		logger.Error("react.tool.unknown", zap.String("trace_id", s.TraceID),
			zap.String("tool_name", tc.Name), zap.String("tool_call_id", tc.ID))
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: fmt.Sprintf("unknown tool %q", tc.Name), content: fmt.Sprintf("error: unknown tool %q", tc.Name)}
	}
	if s.ToolExecutionFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "MCP tool executor not configured", content: "error: MCP tool executor not configured"}
	}
	callCtx, cancel := context.WithTimeout(toolCtx, constants.AgentMCPToolCallTimeout)
	toolOutput, callErr := s.ToolExecutionFn(callCtx, port.ToolExecutionRequest{
		ToolCallID: tc.ID, Tool: tool, Arguments: tc.Arguments, Actives: s.Actives,
	})
	cancel()
	var approvalRequired *port.ToolApprovalRequiredError
	if errors.As(callErr, &approvalRequired) {
		// Approval is a fatal signal — caller must bubble it up.
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: callErr.Error(), content: "", fatalToolErr: callErr}
	}
	toolLatencyMs := time.Since(toolStart).Milliseconds()
	switch {
	case callErr != nil:
		logger.Error("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", toolLatencyMs), zap.Error(callErr))
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: callErr.Error(), content: fmt.Sprintf("error: %v", callErr)}
	case toolOutput != nil:
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", toolLatencyMs))
		guarded, safe := toolOutput.(port.GuardedToolResult)
		if !safe {
			return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "tool result was not validated", content: "error: tool result validation failed"}
		}
		return toolExecResult{
			content: guarded.ModelContent,
			status:  domain.ToolTraceStatusSuccess,
		}
	default:
		logger.Info("react.tool", zap.String("trace_id", s.TraceID), zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID), zap.String("tool_name", tc.Name),
			zap.Int64("latency_ms", toolLatencyMs))
		return toolExecResult{content: "", status: domain.ToolTraceStatusSuccess}
	}
}

func recordToolErrorArtifact(s *ReActState, capabilityID string, toolStart time.Time, result toolExecResult) {
	if result.status == domain.ToolTraceStatusError && isSystemAssistantTool(capabilityID) {
		s.AssistantToolArtifacts = append(s.AssistantToolArtifacts, domain.SystemAssistantToolArtifact{
			Tool: capabilityID, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "error", ErrorCode: assistantToolErrorCode(result.errMsg),
		})
	}
}

func isSystemAssistantTool(toolName string) bool {
	switch toolName {
	case domain.SystemAssistantToolSearchOfficialDocs,
		domain.SystemAssistantToolDiagnoseTenant,
		domain.SystemAssistantToolProposeResourceChange,
		domain.SystemAssistantToolListModels,
		domain.SystemAssistantToolUpdateSystemModel,
		domain.SystemAssistantToolListAgents,
		domain.SystemAssistantToolListMCPServers:
		return true
	default:
		return false
	}
}

func guardInternalAssistantEvidence(fn func(any) (port.GuardedToolResult, error), value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > constants.SystemAssistantToolMaxJSONBytes {
		return "", domain.ErrSystemAssistantEvidenceTooLarge
	}
	if fn == nil {
		return "", errors.New("internal tool result guard unavailable")
	}
	guarded, err := fn(value)
	if err != nil {
		return "", err
	}
	return guarded.ModelContent, nil
}

func safeAssistantToolError(err error) string {
	// Deadline/cancel 是执行层信号，固定文案，不进入哨兵白名单。
	if errors.Is(err, context.DeadlineExceeded) {
		return "tool timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "tool cancelled"
	}
	var applyErr *port.ResourceApplyError
	if errors.As(err, &applyErr) {
		// apply 失败按 outcome 分流：definite_failure 提取内层错误再走
		// 白名单（如提案过期/越权等可读场景）；unknown_outcome 保持
		// fail-closed 的 "evidence unavailable"，不猜测平台健康状态。
		if applyErr.Outcome != port.ResourceApplyDefiniteFailure {
			return "evidence unavailable"
		}
		return safeAssistantToolError(applyErr.Err)
	}
	var invalidArgs *domain.InvalidToolArgumentsError
	if errors.As(err, &invalidArgs) {
		// 字段级校验错误带 detail，须置于哨兵匹配之前：
		// InvalidToolArgumentsError 的 Unwrap 链会让哨兵先命中、
		// 吞掉 detail，模型就收不到可自纠的具体字段错误。
		return "invalid tool arguments: " + invalidArgs.Detail
	}
	if msg, ok := matchAssistantToolSentinel(err); ok {
		return msg
	}
	return "evidence unavailable"
}

// matchAssistantToolSentinel 把已知领域哨兵错误映射为模型可读的固定文案；
// 匹配失败返回 ok=false，由调用方落入默认 fail-closed 分支。
func matchAssistantToolSentinel(err error) (string, bool) {
	switch {
	case errors.Is(err, domain.ErrOfficialEvidenceNotFound):
		return "official evidence not found", true
	case errors.Is(err, domain.ErrDiagnosticForbidden):
		return "diagnostic forbidden", true
	case errors.Is(err, domain.ErrProposalForbidden):
		return "proposal forbidden", true
	case errors.Is(err, domain.ErrProposalInvalid):
		return "invalid proposal payload", true
	case errors.Is(err, domain.ErrProposalExpired):
		return "proposal expired", true
	case errors.Is(err, domain.ErrSystemAssistantManaged):
		return "resource is system-managed", true
	case errors.Is(err, domain.ErrInvalidSystemAssistantToolArguments):
		// 参数解析失败（如 payload 非法）是模型可自纠的错误，明确返回
		// 而非落入默认分支，否则 LLM 会把非法参数误判为环境不可用。
		return "invalid tool arguments", true
	default:
		return "", false
	}
}

func recordToolSpanResult(toolSpan oteltrace.Span, errMsg, content string, toolLatencyMs int64) {
	if errMsg != "" {
		toolSpan.RecordError(fmt.Errorf("%s", errMsg))
		toolSpan.SetStatus(codes.Error, "tool call failed")
	}
	resultPayload := observability.SafeTracePayload(content, constants.AgentToolTraceMaxRawTextBytes)
	toolSpan.SetAttributes(
		attribute.String("stratum.result.sha256", resultPayload.SHA256),
		attribute.Bool("stratum.result.truncated", resultPayload.Truncated),
		attribute.Int64("stratum.latency_ms", toolLatencyMs),
		attribute.Int64("opik.metadata.stratum.latency_ms", toolLatencyMs),
	)
}

// ragEvidenceToMetadata converts retrieval provenance into the metadata
// shape stored on tool observations. Per-chunk content is excluded (the raw
// result already carries it); only attribution fields are kept.
func ragEvidenceToMetadata(evidence port.RAGSearchEvidence) map[string]any {
	sources := make([]any, 0, len(evidence.Sources))
	for _, src := range evidence.Sources {
		item := map[string]any{
			"workspace_id":   src.WorkspaceID,
			"workspace_name": src.WorkspaceName,
			"chunk_id":       src.ChunkID,
		}
		if src.HasScore {
			item["score"] = src.Score
		}
		sources = append(sources, item)
	}
	return map[string]any{"source_count": len(sources), "sources": sources}
}

func appendToolObservation(s *ReActState, tc port.ToolCall, provider toolProviderRef, result toolExecResult, toolStart time.Time, toolLatencyMs int64) {
	summary := summarizeToolObservation(tc.Name, result.content, result.status, result.errMsg)
	metadata := provider.Metadata
	if len(result.evidence) > 0 {
		// Merge, never overwrite: provider metadata wins on key collision.
		merged := make(map[string]any, len(metadata)+1)
		for k, v := range metadata {
			merged[k] = v
		}
		merged["evidence"] = result.evidence
		metadata = merged
	}
	s.ToolObservations = append(s.ToolObservations, domain.ToolObservation{
		TraceID:        s.TraceID,
		ConversationID: s.ConversationID,
		StepIndex:      s.Steps,
		ToolCallID:     tc.ID,
		ToolName:       tc.Name,
		ToolType:       provider.ToolType,
		ProviderType:   provider.ProviderType,
		ProviderID:     provider.ProviderID,
		ServerID:       provider.ServerID,
		CapabilityID:   provider.CapabilityID,
		Arguments:      tc.Arguments,
		RawResult:      result.content,
		RawText:        result.content,
		Summary:        summary,
		Status:         result.status,
		ErrorMessage:   result.errMsg,
		LatencyMs:      toolLatencyMs,
		Metadata:       metadata,
		StartedAt:      toolStart,
		EndedAt:        toolStart.Add(time.Duration(toolLatencyMs) * time.Millisecond),
	})
}

func appendToolTraceEvent(s *ReActState, tc port.ToolCall, provider toolProviderRef, result toolExecResult, toolStart time.Time, toolLatencyMs int64) {
	eventType := domain.TraceEventToolFinished
	if result.status == domain.ToolTraceStatusError {
		eventType = domain.TraceEventToolFailed
	}
	summary := summarizeToolObservation(tc.Name, result.content, result.status, result.errMsg)
	s.TraceEvents = append(s.TraceEvents, domain.AgentTraceEvent{
		TraceID:         s.TraceID,
		ConversationID:  s.ConversationID,
		RunType:         domain.RunTypeAgent,
		ObservationType: domain.ObservationTypeTool,
		EventType:       eventType,
		StepIndex:       s.Steps,
		SpanName:        "react.tool",
		Status:          result.status,
		ProviderType:    provider.ProviderType,
		ProviderID:      provider.ProviderID,
		NodeID:          provider.NodeID,
		NodeType:        provider.NodeType,
		Metadata:        provider.Metadata,
		Output:          map[string]any{"tool_call_id": tc.ID, "tool_name": tc.Name, "summary": summary},
		Summary:         summary,
		ErrorMessage:    result.errMsg,
		LatencyMs:       toolLatencyMs,
		ToolTraceID:     tc.ID,
		StartedAt:       toolStart,
		EndedAt:         toolStart.Add(time.Duration(toolLatencyMs) * time.Millisecond),
	})
}

type toolProviderRef struct {
	ToolType     string
	ProviderType string
	ProviderID   string
	ServerID     string
	CapabilityID string
	NodeID       string
	NodeType     string
	Metadata     map[string]any
}

func classifyToolProvider(name string, tools []port.ToolDefinition) toolProviderRef {
	switch name {
	case domain.SystemAssistantToolSearchOfficialDocs,
		domain.SystemAssistantToolDiagnoseTenant,
		domain.SystemAssistantToolProposeResourceChange,
		domain.SystemAssistantToolApplyResourceChange,
		domain.SystemAssistantToolListModels,
		domain.SystemAssistantToolUpdateSystemModel,
		domain.SystemAssistantToolListAgents,
		domain.SystemAssistantToolListMCPServers:
		return toolProviderRef{ToolType: domain.ToolTypeInternal, ProviderType: domain.ProviderTypeInternal,
			ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_continue_reasoning":
		return toolProviderRef{ToolType: domain.ToolTypeReasoning, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_search_knowledge":
		return toolProviderRef{ToolType: domain.ToolTypeBuiltinRAG, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_recall_memory":
		return toolProviderRef{ToolType: domain.ToolTypeBuiltinMemory, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan":
		return toolProviderRef{ToolType: domain.ToolTypeInternal, ProviderType: domain.ProviderTypeInternal, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	default:
		if ref, ok := lookupToolRef(name, tools); ok {
			return ref
		}
		return toolProviderRef{ToolType: domain.ToolTypeInternal, ProviderType: domain.ProviderTypeInternal, ProviderID: name, CapabilityID: name, NodeID: name, NodeType: domain.ToolTypeInternal}
	}
}

// lookupToolRef 在工具定义列表中查找 name，命中则返回填充默认值的引用。
func lookupToolRef(name string, tools []port.ToolDefinition) (toolProviderRef, bool) {
	for _, td := range tools {
		if td.Name != name {
			continue
		}
		return toolRefWithDefaults(td, name), true
	}
	return toolProviderRef{}, false
}

// toolRefWithDefaults 从工具定义构造 provider 引用，空字段回退到工具名。
func toolRefWithDefaults(td port.ToolDefinition, name string) toolProviderRef {
	ref := toolProviderRef{
		ToolType:     td.ProviderType,
		ProviderType: td.ProviderType,
		ProviderID:   td.ProviderID,
		ServerID:     td.ServerID,
		CapabilityID: td.CapabilityID,
		NodeID:       td.NodeID,
		NodeType:     td.NodeType,
		Metadata:     td.Metadata,
	}
	if ref.ToolType == "" {
		ref.ToolType = domain.ToolTypeInternal
	}
	if ref.ProviderType == "" {
		ref.ProviderType = domain.ProviderTypeInternal
	}
	if ref.ProviderID == "" {
		ref.ProviderID = name
	}
	if ref.CapabilityID == "" {
		ref.CapabilityID = name
	}
	if ref.NodeID == "" {
		ref.NodeID = name
	}
	if ref.NodeType == "" {
		ref.NodeType = ref.ProviderType
	}
	return ref
}

func findTool(name string, tools []port.ToolDefinition) (port.ToolDefinition, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			if tool.CapabilityID == "" {
				tool.CapabilityID = tool.Name
			}
			return tool, true
		}
	}
	return port.ToolDefinition{}, false
}

func allowedKnowledgeWorkspaces(requested, agentAllowed []string, actives []port.SkillActivation) []string {
	agentSet := make(map[string]struct{}, len(agentAllowed))
	for _, id := range agentAllowed {
		agentSet[id] = struct{}{}
	}
	skillSet := map[string]struct{}{}
	for _, active := range actives {
		for _, id := range active.KnowledgeWorkspaceIDs {
			skillSet[id] = struct{}{}
		}
	}
	if len(requested) == 0 {
		requested = agentAllowed
	}
	out := make([]string, 0, len(requested))
	seen := map[string]struct{}{}
	for _, id := range requested {
		if _, ok := agentSet[id]; !ok {
			continue
		}
		if len(actives) > 0 {
			if _, ok := skillSet[id]; !ok {
				continue
			}
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func assistantToolErrorCode(message string) string {
	switch message {
	case "tool timeout":
		return "timeout"
	case "tool cancelled":
		return "cancelled"
	case "official evidence not found":
		return "not_found"
	case "diagnostic forbidden", "proposal forbidden":
		return "forbidden"
	case "invalid proposal payload":
		return "invalid_payload"
	case "proposal expired":
		return "expired"
	case "resource is system-managed":
		return "system_managed"
	case "invalid official docs arguments", "invalid diagnostic arguments",
		"invalid tool arguments", "invalid system assistant tool arguments":
		return "invalid_arguments"
	default:
		return assistantToolErrorCodeDefault(message)
	}
}

// assistantToolErrorCodeDefault 处理默认分支：字段级校验 detail 以
// "invalid tool arguments: " 前缀开头仍归 invalid_arguments（模型可自纠），
// 其余 fallback 到 unavailable。
func assistantToolErrorCodeDefault(message string) string {
	if strings.HasPrefix(message, "invalid tool arguments: ") {
		return "invalid_arguments"
	}
	return "unavailable"
}
