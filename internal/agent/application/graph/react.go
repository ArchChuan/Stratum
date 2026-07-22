package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

const (
	nodeLLM  = "llm"
	nodeTool = "tool"
)

// ReActState is the mutable state threaded through the ReAct graph.
type ReActState struct {
	TenantID                   string
	TraceID                    string
	ConversationID             string
	LLMAPIKeys                 map[string]string
	Model                      string
	AvailableTools             []port.ToolDefinition
	SkillCatalog               map[string]port.SkillActivation
	ActiveSkill                *port.SkillActivation
	TracePayloadStore          port.TracePayloadStore
	ToolExecutionFn            port.ToolExecutionFn
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
	RAGSearchFn                func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	RecallMemoryFn             func(ctx context.Context, input map[string]any) (string, error)
	// MaxLLMSteps caps LLM-node invocations; on the last allowed call tools are
	// stripped and the model is asked to produce a final answer from collected context.
	MaxLLMSteps int
	// MaxContextTokens bounds each ReAct LLM request. When the
	// accumulated Messages exceed it, older tool-call/tool-result groups are compacted
	// (summarized or dropped) before dispatch. Zero disables in-loop compaction.
	MaxContextTokens int
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
	CheckpointEnabled      bool
	ActivePlan             *domain.Plan
	PlanCheckpointWriter   PlanCheckpointWriter
	PlanCheckpointIdentity PlanCheckpointIdentity
	PlanIDSource           func() string
	PlanLimits             domain.PlanLimits
	PlanToolsDisabled      bool
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

// BuildReActGraph constructs and compiles the ReAct agent graph.
func BuildReActGraph(capGW port.CapabilityGateway, ledger TokenRecorder, logger *zap.Logger) (*CompiledGraph[ReActState], error) {
	g := New[ReActState]()
	g.AddNode(nodeLLM, makeLLMNode(capGW, ledger, logger))
	g.AddNode(nodeTool, makeToolNode(capGW, logger))
	g.AddConditionalEdge(nodeLLM, func(s ReActState) string {
		if len(s.Messages) == 0 {
			return END
		}
		last := s.Messages[len(s.Messages)-1]
		if last.Role == "assistant" && len(last.ToolCalls) > 0 {
			return nodeTool
		}
		return END
	})
	g.AddEdge(nodeTool, nodeLLM)
	g.SetEntryPoint(nodeLLM)
	return g.Compile()
}

func makeLLMNode(capGW port.CapabilityGateway, ledger TokenRecorder, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		start := time.Now()

		tools := effectiveTools(s.AvailableTools, s.SkillCatalog, s.ActiveSkill, s.AgentKnowledgeWorkspaceIDs, s.AgentMemoryScope)
		if s.PlanToolsDisabled {
			tools = withoutPlanTools(tools)
		}
		messages := messagesWithActiveSkill(s.Messages, s.ActiveSkill)
		protectedUsers := 1
		if s.MaxLLMSteps > 0 && s.Steps >= s.MaxLLMSteps-1 {
			tools = nil
			protectedUsers = 2
			messages = append(messages, port.LLMMessage{
				Role:    "user",
				Content: "You have reached the maximum reasoning steps. Based on your analysis and tool results so far, provide your final answer now. Do not call any tools.",
			})
		}
		// In-loop compaction: bound the complete request, including any final-step
		// instruction, without mutating s.Messages (trace/history stay complete).
		tools = fitToolsToContextBudget(tools, messages, s.MaxContextTokens, protectedUsers)
		toolTokens := 0
		if encodedTools, err := json.Marshal(tools); err == nil {
			toolTokens = tokenutil.EstimateText(string(encodedTools))
		}
		messages = compactLoopMessagesWithProtectedUsers(ctx, messages, s.MaxContextTokens, toolTokens, constants.LoopCompactionRecentGroups, protectedUsers, s.HistoryCompactor)

		tracer := otel.Tracer("stratum/agent")
		inputPayload := observability.SafeTracePayload(map[string]any{"messages": messages, "tools": tools}, constants.AgentToolTraceMaxRawJSONBytes)
		llmAttributes := []attribute.KeyValue{
			attribute.String("llm.model", s.Model),
			attribute.String("gen_ai.request.model", s.Model),
			attribute.Int("react.step", s.Steps+1),
			attribute.Int("stratum.llm.step", s.Steps+1),
			attribute.String("stratum.input.sha256", inputPayload.SHA256),
			attribute.Bool("stratum.input.truncated", inputPayload.Truncated),
			attribute.String("opik.metadata.stratum.tenant_id", s.TenantID),
			attribute.String("opik.metadata.stratum.trace_id", s.TraceID),
			attribute.String("opik.metadata.stratum.provider_type", domain.ProviderTypeLLM),
			attribute.String("opik.metadata.stratum.provider_id", s.Model),
			attribute.String("opik.metadata.stratum.status", domain.ToolTraceStatusSuccess),
		}
		llmAttributes = append(llmAttributes, tracePayloadAttributes(
			ctx, s.TracePayloadStore, s.TenantID, s.TraceID, "llm-input",
			map[string]any{"messages": messages, "tools": tools},
		)...)
		ctx, llmSpan := tracer.Start(ctx, "react.llm",
			oteltrace.WithAttributes(llmAttributes...),
		)
		defer llmSpan.End()
		s.TraceEvents = append(s.TraceEvents, domain.AgentTraceEvent{
			TraceID:         s.TraceID,
			ConversationID:  s.ConversationID,
			RunType:         domain.RunTypeAgent,
			ObservationType: domain.ObservationTypeLLM,
			EventType:       domain.TraceEventLLMRequest,
			StepIndex:       s.Steps + 1,
			SpanName:        "react.llm",
			Status:          domain.ToolTraceStatusSuccess,
			ProviderType:    domain.ProviderTypeLLM,
			ProviderID:      s.Model,
			NodeID:          nodeLLM,
			NodeType:        domain.ObservationTypeLLM,
			Input: map[string]any{
				"model":    s.Model,
				"messages": messages,
				"tools":    tools,
			},
			Model:     s.Model,
			StartedAt: start,
			EndedAt:   start,
		})

		// Always stream: tool-decision turns typically produce empty content so no tokens
		// reach the client; final-answer turns stream the output to the frontend as required.
		resp, err := RetryFn(ctx, DefaultRetry, func() (port.CapabilityResponse, error) {
			return capGW.Route(ctx, port.CapabilityRequest{
				TraceID:     s.TraceID,
				TenantID:    s.TenantID,
				Type:        port.CapLLM,
				LLMAPIKeys:  s.LLMAPIKeys,
				TokenStream: s.OnToken,
				LLM: &port.LLMCapRequest{
					Model:    s.Model,
					Messages: messages,
					Tools:    tools,
				},
			})
		})
		latencyMs := time.Since(start).Milliseconds()
		if err != nil {
			llmSpan.SetAttributes(attribute.String("opik.metadata.stratum.status", domain.ToolTraceStatusError))
			llmSpan.RecordError(err)
			llmSpan.SetStatus(codes.Error, "llm call failed")
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				logger.Info("react.llm",
					zap.String("trace_id", s.TraceID),
					zap.String("tenant_id", s.TenantID),
					zap.String("conversation_id", s.ConversationID),
					zap.String("model", s.Model),
					zap.Int("step", s.Steps+1),
					zap.Int64("latency_ms", latencyMs),
					zap.String("error", "context canceled"),
				)
			} else {
				logger.Error("react.llm",
					zap.String("trace_id", s.TraceID),
					zap.String("tenant_id", s.TenantID),
					zap.String("conversation_id", s.ConversationID),
					zap.String("model", s.Model),
					zap.Int("step", s.Steps+1),
					zap.Int64("latency_ms", latencyMs),
					zap.Error(err),
				)
			}
			return s, fmt.Errorf("react llm node: %w", err)
		}
		s.Steps++
		total, cost := ledger.Record(ctx, s.Model, resp.Usage)
		s.TotalTokens += total
		s.TotalCostUSD += cost
		llmSpan.SetAttributes(
			attribute.Int("llm.prompt_tokens", resp.Usage.Prompt),
			attribute.Int("llm.completion_tokens", resp.Usage.Completion),
			attribute.Int("gen_ai.usage.input_tokens", resp.Usage.Prompt),
			attribute.Int("gen_ai.usage.output_tokens", resp.Usage.Completion),
			attribute.Float64("stratum.cost_usd", cost),
			attribute.Bool("llm.has_tool_calls", len(resp.ToolCalls) > 0),
			attribute.Int64("opik.metadata.stratum.latency_ms", latencyMs),
			attribute.Int64("opik.metadata.stratum.total_tokens", int64(resp.Usage.Total)),
			attribute.Float64("opik.metadata.stratum.cost_usd", cost),
		)
		outputPayload := observability.SafeTracePayload(map[string]any{"content": resp.Content, "tool_calls": resp.ToolCalls}, constants.AgentToolTraceMaxRawJSONBytes)
		outputAttributes := []attribute.KeyValue{
			attribute.String("stratum.output.sha256", outputPayload.SHA256),
			attribute.Bool("stratum.output.truncated", outputPayload.Truncated),
		}
		outputAttributes = append(outputAttributes, tracePayloadAttributes(
			ctx, s.TracePayloadStore, s.TenantID, s.TraceID, "llm-output",
			map[string]any{"content": resp.Content, "tool_calls": resp.ToolCalls},
		)...)
		llmSpan.SetAttributes(outputAttributes...)
		logger.Info("react.llm",
			zap.String("trace_id", s.TraceID),
			zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID),
			zap.String("model", s.Model),
			zap.Int("step", s.Steps),
			zap.Int("total_tokens", s.TotalTokens),
			zap.Float64("cost_usd", s.TotalCostUSD),
			zap.Int64("latency_ms", latencyMs),
			zap.Bool("has_tool_calls", len(resp.ToolCalls) > 0),
		)
		if logger.Core().Enabled(zap.DebugLevel) {
			preview := resp.Content
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			logger.Debug("react.llm.response",
				zap.String("trace_id", s.TraceID),
				zap.String("model", s.Model),
				zap.Int("step", s.Steps),
				zap.Int("tool_calls", len(resp.ToolCalls)),
				zap.String("content_preview", preview),
			)
		}
		if len(resp.ToolCalls) == 0 {
			s.Output = resp.Content
			s.Messages = append(s.Messages, port.LLMMessage{
				Role:    "assistant",
				Content: resp.Content,
			})
			s.TraceEvents = append(s.TraceEvents, domain.AgentTraceEvent{
				TraceID:          s.TraceID,
				ConversationID:   s.ConversationID,
				RunType:          domain.RunTypeAgent,
				ObservationType:  domain.ObservationTypeLLM,
				EventType:        domain.TraceEventLLMResponse,
				StepIndex:        s.Steps,
				SpanName:         "react.llm",
				Status:           domain.ToolTraceStatusSuccess,
				Output:           map[string]any{"content": resp.Content},
				Summary:          truncateRunes(resp.Content, 500),
				Model:            s.Model,
				ProviderType:     domain.ProviderTypeLLM,
				ProviderID:       s.Model,
				NodeID:           nodeLLM,
				NodeType:         domain.ObservationTypeLLM,
				PromptTokens:     resp.Usage.Prompt,
				CompletionTokens: resp.Usage.Completion,
				TotalTokens:      resp.Usage.Total,
				CostUSD:          cost,
				LatencyMs:        latencyMs,
				StartedAt:        start,
				EndedAt:          start.Add(time.Duration(latencyMs) * time.Millisecond),
			})
		} else {
			s.Messages = append(s.Messages, port.LLMMessage{
				Role:      "assistant",
				ToolCalls: resp.ToolCalls,
			})
			s.TraceEvents = append(s.TraceEvents, domain.AgentTraceEvent{
				TraceID:          s.TraceID,
				ConversationID:   s.ConversationID,
				RunType:          domain.RunTypeAgent,
				ObservationType:  domain.ObservationTypeLLM,
				EventType:        domain.TraceEventLLMResponse,
				StepIndex:        s.Steps,
				SpanName:         "react.llm",
				Status:           domain.ToolTraceStatusSuccess,
				Output:           map[string]any{"tool_calls": resp.ToolCalls},
				Summary:          fmt.Sprintf("model requested %d tool call(s)", len(resp.ToolCalls)),
				Model:            s.Model,
				ProviderType:     domain.ProviderTypeLLM,
				ProviderID:       s.Model,
				NodeID:           nodeLLM,
				NodeType:         domain.ObservationTypeLLM,
				PromptTokens:     resp.Usage.Prompt,
				CompletionTokens: resp.Usage.Completion,
				TotalTokens:      resp.Usage.Total,
				CostUSD:          cost,
				LatencyMs:        latencyMs,
				StartedAt:        start,
				EndedAt:          start.Add(time.Duration(latencyMs) * time.Millisecond),
			})
		}
		return s, nil
	}
}

func fitToolsToContextBudget(tools []port.ToolDefinition, messages []port.LLMMessage, budget, protectedUsers int) []port.ToolDefinition {
	if budget <= 0 || len(tools) == 0 {
		return tools
	}
	threshold := int(float64(budget) * constants.LoopCompactionSafetyRatio)
	groups := groupMessages(messages)
	protectedMessages := flatten(groups[:anchorCount(groups)])
	usersKept := 0
	for i := len(groups) - 1; i >= 0 && usersKept < protectedUsers; i-- {
		if groups[i].role0 == "user" {
			protectedMessages = append(protectedMessages, groups[i].msgs...)
			usersKept++
		}
	}
	toolAllowance := max(threshold-tokenutil.EstimateMessages(toEstimate(protectedMessages)), 0)
	fitted := make([]port.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		candidate := make([]port.ToolDefinition, len(fitted), len(fitted)+1)
		copy(candidate, fitted)
		candidate = append(candidate, tool)
		encoded, err := json.Marshal(candidate)
		if err == nil && tokenutil.EstimateText(string(encoded)) <= toolAllowance {
			fitted = candidate
		}
	}
	return fitted
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
			toolAttributes := []attribute.KeyValue{
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
			toolAttributes = append(toolAttributes, tracePayloadAttributes(
				ctx, s.TracePayloadStore, s.TenantID, s.TraceID, "tool-arguments", tc.Arguments,
			)...)
			toolCtx, toolSpan := tracer.Start(ctx, "react.tool",
				oteltrace.WithAttributes(toolAttributes...),
			)
			var content string
			status := domain.ToolTraceStatusSuccess
			errMsg := ""
			s.TraceEvents = append(s.TraceEvents, domain.AgentTraceEvent{
				TraceID:         s.TraceID,
				ConversationID:  s.ConversationID,
				RunType:         domain.RunTypeAgent,
				ObservationType: domain.ObservationTypeTool,
				EventType:       domain.TraceEventToolStarted,
				StepIndex:       s.Steps,
				SpanName:        "react.tool",
				Status:          status,
				ProviderType:    provider.ProviderType,
				ProviderID:      provider.ProviderID,
				NodeID:          provider.NodeID,
				NodeType:        provider.NodeType,
				Input:           map[string]any{"tool_call_id": tc.ID, "tool_name": tc.Name, "arguments": tc.Arguments},
				Summary:         fmt.Sprintf("calling tool %s", tc.Name),
				StartedAt:       toolStart,
				EndedAt:         toolStart,
			})
			switch tc.Name {
			case "stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan":
				var planErr error
				content, planErr = ExecutePlanTool(toolCtx, &s, tc)
				if planErr != nil {
					status = domain.ToolTraceStatusError
					errMsg = planErr.Error()
				}
			case "stratum_continue_reasoning":
				content = "Continuing reasoning..."
			case "stratum_activate_skill":
				skillID, _ := tc.Arguments["skill_id"].(string)
				activation, ok := s.SkillCatalog[skillID]
				if !ok {
					content = fmt.Sprintf("error: skill %q is not available in this run", skillID)
					status = domain.ToolTraceStatusError
					errMsg = content
					break
				}
				s.ActiveSkill = &activation
				content = fmt.Sprintf("activated skill %s revision %s", activation.SkillID, activation.RevisionID)
			case "stratum_search_knowledge":
				if s.RAGSearchFn == nil {
					content = "error: stratum_search_knowledge tool not configured"
				} else {
					var workspaces []string
					if raw, ok := tc.Arguments["workspaces"].([]interface{}); ok {
						for _, v := range raw {
							if s, ok := v.(string); ok {
								workspaces = append(workspaces, s)
							}
						}
					}
					query, _ := tc.Arguments["query"].(string)
					workspaces = allowedKnowledgeWorkspaces(workspaces, s.AgentKnowledgeWorkspaceIDs, s.ActiveSkill)
					if len(workspaces) == 0 {
						content = "error: no authorized knowledge workspace"
						status = domain.ToolTraceStatusError
						errMsg = content
						break
					}
					topK := 5
					if v, ok := tc.Arguments["top_k"].(float64); ok {
						topK = int(v)
						if topK > constants.MaxRAGTopK {
							topK = constants.MaxRAGTopK
						}
					}
					var ragErr error
					ragCtx, ragCancel := context.WithTimeout(toolCtx, constants.AgentRAGSearchTimeout)
					content, ragErr = s.RAGSearchFn(ragCtx, workspaces, query, topK)
					ragCancel()
					if ragErr != nil {
						content = fmt.Sprintf("error: %v", ragErr)
						status = domain.ToolTraceStatusError
						errMsg = ragErr.Error()
					}
				}
				toolLatencyMs := time.Since(toolStart).Milliseconds()
				logger.Info("react.tool",
					zap.String("trace_id", s.TraceID),
					zap.String("tenant_id", s.TenantID),
					zap.String("conversation_id", s.ConversationID),
					zap.String("tool_name", tc.Name),
					zap.Int64("latency_ms", toolLatencyMs),
				)
			case "stratum_recall_memory":
				switch {
				case s.ActiveSkill != nil && !containsString(s.ActiveSkill.MemoryScopes, s.AgentMemoryScope):
					content = "error: active skill does not permit this memory scope"
					status = domain.ToolTraceStatusError
					errMsg = content
				case s.RecallMemoryFn == nil:
					content = "error: stratum_recall_memory tool not configured"
				default:
					var recallErr error
					recallCtx, recallCancel := context.WithTimeout(toolCtx, constants.AgentMemoryRecallTimeout)
					content, recallErr = s.RecallMemoryFn(recallCtx, tc.Arguments)
					recallCancel()
					if recallErr != nil {
						content = fmt.Sprintf("error: %v", recallErr)
						status = domain.ToolTraceStatusError
						errMsg = recallErr.Error()
					}
				}
				toolLatencyMs := time.Since(toolStart).Milliseconds()
				logger.Info("react.tool",
					zap.String("trace_id", s.TraceID),
					zap.String("tenant_id", s.TenantID),
					zap.String("conversation_id", s.ConversationID),
					zap.String("tool_name", tc.Name),
					zap.Int64("latency_ms", toolLatencyMs),
				)
			default:
				tool, ok := findTool(tc.Name, s.AvailableTools)
				if !ok || provider.ProviderType != domain.ProviderTypeMCP {
					content = fmt.Sprintf("error: unknown tool %q", tc.Name)
					status = domain.ToolTraceStatusError
					errMsg = fmt.Sprintf("unknown tool %q", tc.Name)
					logger.Error("react.tool.unknown",
						zap.String("trace_id", s.TraceID),
						zap.String("tool_name", tc.Name),
						zap.String("tool_call_id", tc.ID),
					)
					break
				}
				if s.ToolExecutionFn == nil {
					content = "error: MCP tool executor not configured"
					status = domain.ToolTraceStatusError
					errMsg = content
					break
				}
				callCtx, cancel := context.WithTimeout(toolCtx, constants.AgentMCPToolCallTimeout)
				toolOutput, callErr := s.ToolExecutionFn(callCtx, port.ToolExecutionRequest{
					ToolCallID: tc.ID, Tool: tool, Arguments: tc.Arguments, ActiveSkill: s.ActiveSkill,
				})
				cancel()
				var approvalRequired *port.ToolApprovalRequiredError
				if errors.As(callErr, &approvalRequired) {
					return s, callErr
				}
				toolLatencyMs := time.Since(toolStart).Milliseconds()
				switch {
				case callErr != nil:
					status = domain.ToolTraceStatusError
					errMsg = callErr.Error()
					logger.Error("react.tool",
						zap.String("trace_id", s.TraceID),
						zap.String("tenant_id", s.TenantID),
						zap.String("conversation_id", s.ConversationID),
						zap.String("tool_name", tc.Name),
						zap.Int64("latency_ms", toolLatencyMs),
						zap.Error(callErr),
					)
					content = fmt.Sprintf("error: %v", callErr)
				case toolOutput != nil:
					logger.Info("react.tool",
						zap.String("trace_id", s.TraceID),
						zap.String("tenant_id", s.TenantID),
						zap.String("conversation_id", s.ConversationID),
						zap.String("tool_name", tc.Name),
						zap.Int64("latency_ms", toolLatencyMs),
					)
					guarded, ok := toolOutput.(port.GuardedToolResult)
					if !ok {
						status = domain.ToolTraceStatusError
						errMsg = "tool result was not validated"
						content = "error: tool result validation failed"
						break
					}
					content = guarded.ModelContent
				default:
					logger.Info("react.tool",
						zap.String("trace_id", s.TraceID),
						zap.String("tenant_id", s.TenantID),
						zap.String("conversation_id", s.ConversationID),
						zap.String("tool_name", tc.Name),
						zap.Int64("latency_ms", toolLatencyMs),
					)
					content = ""
				}
			}
			toolLatencyMs := time.Since(toolStart).Milliseconds()
			if errMsg != "" {
				toolSpan.RecordError(fmt.Errorf("%s", errMsg))
				toolSpan.SetStatus(codes.Error, "tool call failed")
			}
			resultPayload := observability.SafeTracePayload(content, constants.AgentToolTraceMaxRawTextBytes)
			resultAttributes := []attribute.KeyValue{
				attribute.String("stratum.result.sha256", resultPayload.SHA256),
				attribute.Bool("stratum.result.truncated", resultPayload.Truncated),
				attribute.Int64("stratum.latency_ms", toolLatencyMs),
				attribute.Int64("opik.metadata.stratum.latency_ms", toolLatencyMs),
				attribute.String("opik.metadata.stratum.status", status),
			}
			resultAttributes = append(resultAttributes, tracePayloadAttributes(
				toolCtx, s.TracePayloadStore, s.TenantID, s.TraceID, "tool-result", content,
			)...)
			toolSpan.SetAttributes(resultAttributes...)
			summary := summarizeToolObservation(tc.Name, content, status, errMsg)
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
				RawResult:      content,
				RawText:        content,
				Summary:        summary,
				Status:         status,
				ErrorMessage:   errMsg,
				LatencyMs:      toolLatencyMs,
				Metadata:       provider.Metadata,
				StartedAt:      toolStart,
				EndedAt:        toolStart.Add(time.Duration(toolLatencyMs) * time.Millisecond),
			})
			eventType := domain.TraceEventToolFinished
			if status == domain.ToolTraceStatusError {
				eventType = domain.TraceEventToolFailed
			}
			s.TraceEvents = append(s.TraceEvents, domain.AgentTraceEvent{
				TraceID:         s.TraceID,
				ConversationID:  s.ConversationID,
				RunType:         domain.RunTypeAgent,
				ObservationType: domain.ObservationTypeTool,
				EventType:       eventType,
				StepIndex:       s.Steps,
				SpanName:        "react.tool",
				Status:          status,
				ProviderType:    provider.ProviderType,
				ProviderID:      provider.ProviderID,
				NodeID:          provider.NodeID,
				NodeType:        provider.NodeType,
				Metadata:        provider.Metadata,
				Output:          map[string]any{"tool_call_id": tc.ID, "tool_name": tc.Name, "summary": summary},
				Summary:         summary,
				ErrorMessage:    errMsg,
				LatencyMs:       toolLatencyMs,
				ToolTraceID:     tc.ID,
				StartedAt:       toolStart,
				EndedAt:         toolStart.Add(time.Duration(toolLatencyMs) * time.Millisecond),
			})
			if logger.Core().Enabled(zap.DebugLevel) {
				preview := content
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				logger.Debug("react.tool.response",
					zap.String("trace_id", s.TraceID),
					zap.String("tool_name", tc.Name),
					zap.Int("step", s.Steps),
					zap.String("content_preview", preview),
				)
			}
			s.Messages = append(s.Messages, port.LLMMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
			})
			toolSpan.End()
			s.AllToolCalls = append(s.AllToolCalls, tc)
		}
		return s, nil
	}
}

func tracePayloadAttributes(
	ctx context.Context,
	store port.TracePayloadStore,
	tenantID, traceID, kind string,
	value any,
) []attribute.KeyValue {
	if !observability.TraceContentCaptureEnabled() || store == nil {
		return nil
	}
	ref, err := store.Put(ctx, port.TracePayload{
		TenantID: tenantID, TraceID: traceID, Kind: kind, Value: value,
	})
	if err != nil {
		return []attribute.KeyValue{
			attribute.String("opik.metadata.stratum.payload_storage_status", "error"),
		}
	}
	return []attribute.KeyValue{
		attribute.String("opik.metadata.stratum.payload_storage_status", "stored"),
		attribute.String("opik.metadata.stratum.payload_ref", ref.Reference),
		attribute.String("opik.metadata.stratum.payload_sha256", ref.SHA256),
		attribute.Int64("opik.metadata.stratum.payload_size_bytes", ref.SizeBytes),
	}
}

func metadataString(metadata map[string]any, key string) string {
	value, _ := metadata[key].(string)
	return value
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
	case "stratum_continue_reasoning":
		return toolProviderRef{ToolType: domain.ToolTypeReasoning, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_search_knowledge":
		return toolProviderRef{ToolType: domain.ToolTypeBuiltinRAG, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_recall_memory":
		return toolProviderRef{ToolType: domain.ToolTypeBuiltinMemory, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_activate_skill":
		return toolProviderRef{ToolType: domain.ToolTypeSkill, ProviderType: domain.ProviderTypeSkill, ProviderID: name, CapabilityID: name, NodeID: name, NodeType: domain.ObservationTypeSkill}
	case "stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan":
		return toolProviderRef{ToolType: domain.ToolTypeInternal, ProviderType: domain.ProviderTypeInternal, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	default:
		for _, td := range tools {
			if td.Name != name {
				continue
			}
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
		return toolProviderRef{ToolType: domain.ToolTypeInternal, ProviderType: domain.ProviderTypeInternal, ProviderID: name, CapabilityID: name, NodeID: name, NodeType: domain.ToolTypeInternal}
	}
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

func allowedKnowledgeWorkspaces(requested, agentAllowed []string, active *port.SkillActivation) []string {
	agentSet := make(map[string]struct{}, len(agentAllowed))
	for _, id := range agentAllowed {
		agentSet[id] = struct{}{}
	}
	skillSet := map[string]struct{}{}
	if active != nil {
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
		if active != nil {
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

func effectiveTools(
	available []port.ToolDefinition,
	catalog map[string]port.SkillActivation,
	active *port.SkillActivation,
	agentKnowledgeWorkspaceIDs []string,
	agentMemoryScope string,
) []port.ToolDefinition {
	out := make([]port.ToolDefinition, 0, len(available)+5)
	out = append(out, PlanToolDefinitions()...)
	if len(catalog) > 0 {
		ids := make([]any, 0, len(catalog))
		for id := range catalog {
			ids = append(ids, id)
		}
		out = append(out, port.ToolDefinition{
			Name:         "stratum_activate_skill",
			Description:  "Activate one versioned task method for the current run. Activating another skill replaces the current one.",
			ProviderType: domain.ProviderTypeSkill,
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"skill_id": map[string]any{"type": "string", "enum": ids}},
				"required":   []string{"skill_id"},
			},
		})
	}
	allowedMCP := map[string]struct{}{}
	if active != nil {
		for _, id := range active.MCPToolIDs {
			allowedMCP[id] = struct{}{}
		}
	}
	for _, tool := range available {
		if isReservedPlanTool(tool.Name) {
			continue
		}
		if active != nil && tool.Name == "stratum_recall_memory" && !containsString(active.MemoryScopes, agentMemoryScope) {
			continue
		}
		if active != nil && tool.Name == "stratum_search_knowledge" && len(allowedKnowledgeWorkspaces(nil, agentKnowledgeWorkspaceIDs, active)) == 0 {
			continue
		}
		if tool.ProviderType == domain.ProviderTypeMCP && active != nil {
			if _, ok := allowedMCP[tool.Name]; !ok {
				continue
			}
		}
		out = append(out, tool)
	}
	return out
}

func isReservedPlanTool(name string) bool {
	switch name {
	case "stratum_create_plan", "stratum_revise_plan", "stratum_continue_plan", "stratum_cancel_plan":
		return true
	default:
		return false
	}
}

func withoutPlanTools(tools []port.ToolDefinition) []port.ToolDefinition {
	filtered := make([]port.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		if !isReservedPlanTool(tool.Name) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func messagesWithActiveSkill(messages []port.LLMMessage, active *port.SkillActivation) []port.LLMMessage {
	if active == nil || active.Instructions == "" {
		return messages
	}
	instruction := port.LLMMessage{
		Role:    "system",
		Content: fmt.Sprintf("Active Skill %s (revision %s):\n%s", active.Name, active.RevisionID, active.Instructions),
	}
	out := make([]port.LLMMessage, 0, len(messages)+1)
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, messages[0], instruction)
		out = append(out, messages[1:]...)
		return out
	}
	out = append(out, instruction)
	return append(out, messages...)
}

func summarizeToolObservation(name, content, status, errMsg string) string {
	if status == domain.ToolTraceStatusError {
		return truncateRunes(fmt.Sprintf("%s failed: %s", name, errMsg), 800)
	}
	return truncateRunes(fmt.Sprintf("%s returned: %s", name, content), 800)
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "...[truncated]"
}
