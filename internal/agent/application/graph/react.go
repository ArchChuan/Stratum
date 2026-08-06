package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	TenantID       string
	TraceID        string
	ConversationID string
	Model          string
	Temperature    float32 // 0 = provider default
	MaxTokens      int     // 0 = unset
	// ModelResolved 是本次执行最后一次 LLM 调用实际成功的模型名（fallback
	// 降级后与 Model 不同）；ModelRoutedVia 是实际尝试过的模型链。
	ModelResolved              string
	ModelRoutedVia             []string
	AvailableTools             []port.ToolDefinition
	SkillCatalog               map[string]port.SkillActivation
	Actives                    []port.SkillActivation
	TracePayloadStore          port.TracePayloadStore
	ToolExecutionFn            port.ToolExecutionFn
	GovernedAssistant          bool
	AssistantToolArtifacts     []domain.SystemAssistantToolArtifact
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
	OfficialDocsSearchFn       func(context.Context, string) ([]domain.Citation, error)
	DiagnosticFn               func(context.Context, []domain.DiagnosticArea) (domain.DiagnosticEvidence, error)
	ProposalCreateFn           func(context.Context, map[string]any) (domain.ResourceChangeProposalArtifact, error)
	InternalToolResultGuardFn  func(any) (port.GuardedToolResult, error)
	// MaxLLMSteps caps LLM-node invocations; on the last allowed call tools are
	// stripped and the model is asked to produce a final answer from collected context.
	MaxLLMSteps int
	// MaxContextTokens bounds each ReAct LLM request. When the
	// accumulated Messages exceed it, older tool-call/tool-result groups are compacted
	// (summarized or dropped) before dispatch. Zero disables in-loop compaction.
	MaxContextTokens int
	// CompactionRecentGroups overrides the recent-groups count during in-loop
	// compaction. 0 = auto-derive from MaxContextTokens.
	CompactionRecentGroups int
	// CompactionSafetyRatio overrides the compaction safety ratio. 0 = default.
	CompactionSafetyRatio float32
	// TokenCorrection is the EMA correction factor applied to the compaction
	// threshold, learned from the previous step's estimated-vs-actual prompt
	// token ratio. Must be > 0; buildReActInitState initializes it to 1.0.
	TokenCorrection float64
	// LastEstimatedTokens is the estimated token count of the previous
	// dispatched request (post-compaction messages + tools). It is the ratio
	// baseline for TokenCorrection; 0 until the first request is dispatched.
	LastEstimatedTokens int
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
	PlanNodeExecutor       PlanNodeExecutor
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

		tools := effectiveTools(s.AvailableTools, s.SkillCatalog, s.Actives, s.AgentKnowledgeWorkspaceIDs, s.AgentMemoryScope, s.GovernedAssistant)
		if s.PlanToolsDisabled {
			tools = withoutPlanTools(tools)
		}
		messages := messagesWithActiveSkills(s.Messages, s.Actives)
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
		// Tunable overrides resolve here: 0 means auto-derive from the window.
		recentGroups, safetyRatio, correction := loopPolicy(s)
		tools = fitToolsToContextBudget(tools, messages, s.MaxContextTokens, protectedUsers, correction, safetyRatio)
		toolTokens := 0
		if encodedTools, err := json.Marshal(tools); err == nil {
			toolTokens = tokenutil.EstimateText(string(encodedTools))
		}
		messages = compactLoopMessagesWithPolicy(ctx, messages, s.MaxContextTokens, toolTokens, recentGroups, protectedUsers, correction, safetyRatio, s.HistoryCompactor)
		// Baseline for the usage-feedback loop: the estimate of what is actually
		// dispatched this step (post-compaction messages + tools), so the ratio
		// stays on a consistent basis across steps.
		s.LastEstimatedTokens = tokenutil.EstimateMessages(toEstimate(messages)) + toolTokens

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
		resp, err := routeLLM(ctx, s, messages, tools, capGW)
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
		total, cost := recordModelResolution(ctx, &s, resp, ledger)
		s.TotalTokens += total
		s.TotalCostUSD += cost
		s.TokenCorrection = updateTokenCorrection(s.TokenCorrection, s.LastEstimatedTokens, resp.Usage.Prompt)
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

// recordModelResolution 回写模型解析结果（fallback 降级后与配置模型不同）
// 并按实际解析模型记账（价格不同），返回累计 token 与成本。
func recordModelResolution(ctx context.Context, s *ReActState, resp port.CapabilityResponse, ledger TokenRecorder) (int, float64) {
	s.ModelResolved = resp.ModelResolved
	s.ModelRoutedVia = resp.ModelRoutedVia
	ledgerModel := s.Model
	if resp.ModelResolved != "" {
		ledgerModel = resp.ModelResolved
	}
	return ledger.Record(ctx, ledgerModel, resp.Usage)
}

// loopPolicy resolves the in-loop compaction tunables from the run state:
// 0 means auto-derive (recent groups from the window size, safety ratio from
// the constant default), and a zero correction is treated as 1 (no correction).
func loopPolicy(s ReActState) (recentGroups int, safetyRatio, correction float64) {
	recentGroups = s.CompactionRecentGroups
	if recentGroups == 0 {
		recentGroups = constants.DynamicRecentGroups(s.MaxContextTokens)
	}
	safetyRatio = float64(s.CompactionSafetyRatio)
	if safetyRatio == 0 {
		safetyRatio = constants.LoopCompactionSafetyRatio
	}
	correction = s.TokenCorrection
	if correction <= 0 {
		correction = 1
	}
	return recentGroups, safetyRatio, correction
}

// updateTokenCorrection folds the previous step's estimated-vs-actual prompt
// token ratio into the EMA correction factor, clamped to [TokenCorrectionMin,
// TokenCorrectionMax]. Without a baseline (first step) or a reported prompt
// count the correction is left unchanged. Under-estimation (ratio > 1) raises
// the correction, lowering the next compaction threshold so compaction starts
// earlier.
func updateTokenCorrection(correction float64, estimatedTokens, actualPrompt int) float64 {
	if correction <= 0 {
		// 零值 state（绕过 buildReActInitState 的调用方）按 1.0 处理，
		// 与 compactLoopMessagesWithPolicy 的 correction≤0→1 同语义，
		// 否则 0.9×0 会把 EMA 塌到 clamp 下限，低估反而变成更晚压缩。
		correction = 1
	}
	if estimatedTokens <= 0 || actualPrompt <= 0 {
		return correction
	}
	ratio := float64(actualPrompt) / float64(estimatedTokens)
	smoothed := constants.TokenCorrectionAlpha*ratio + (1-constants.TokenCorrectionAlpha)*correction
	return min(max(smoothed, constants.TokenCorrectionMin), constants.TokenCorrectionMax)
}

// routeLLM streams one LLM call with retry. Extracted from makeLLMNode so the
// request construction and retry closure stay within the code-quality line and
// complexity budgets of the node function.
func routeLLM(ctx context.Context, s ReActState, messages []port.LLMMessage, tools []port.ToolDefinition, capGW port.CapabilityGateway) (port.CapabilityResponse, error) {
	return RetryFn(ctx, DefaultRetry, func() (port.CapabilityResponse, error) {
		return capGW.Route(ctx, port.CapabilityRequest{
			TraceID:     s.TraceID,
			TenantID:    s.TenantID,
			Type:        port.CapLLM,
			TokenStream: s.OnToken,
			LLM: &port.LLMCapRequest{
				Model:       s.Model,
				Messages:    messages,
				Tools:       tools,
				Temperature: s.Temperature,
				MaxTokens:   s.MaxTokens,
			},
		})
	})
}

func fitToolsToContextBudget(tools []port.ToolDefinition, messages []port.LLMMessage, budget, protectedUsers int, correction, safetyRatio float64) []port.ToolDefinition {
	if budget <= 0 || len(tools) == 0 {
		return tools
	}
	threshold := compactionThreshold(budget, 0, correction, safetyRatio)
	groups := groupMessages(messages)
	protectedMessages := flatten(groups[:anchorCount(groups)])
	protectedMessages = append(protectedMessages, protectedUserMessages(groups, protectedUsers)...)
	toolAllowance := max(threshold-tokenutil.EstimateMessages(toEstimate(protectedMessages)), 0)
	return fitToolList(tools, toolAllowance)
}

// protectedUserMessages collects the most recent protected user turns (the
// active task and, when configured, earlier task history) so tools never crowd
// out the messages that must survive compaction.
func protectedUserMessages(groups []msgGroup, protectedUsers int) []port.LLMMessage {
	var out []port.LLMMessage
	usersKept := 0
	for i := len(groups) - 1; i >= 0 && usersKept < protectedUsers; i-- {
		if groups[i].role0 == "user" {
			out = append(out, groups[i].msgs...)
			usersKept++
		}
	}
	return out
}

// fitToolList greedily packs tool schemas while the encoded definition list
// stays within the token allowance, preserving declaration order.
func fitToolList(tools []port.ToolDefinition, allowance int) []port.ToolDefinition {
	fitted := make([]port.ToolDefinition, 0, len(tools))
	for _, tool := range tools {
		candidate := make([]port.ToolDefinition, len(fitted), len(fitted)+1)
		copy(candidate, fitted)
		candidate = append(candidate, tool)
		encoded, err := json.Marshal(candidate)
		if err == nil && tokenutil.EstimateText(string(encoded)) <= allowance {
			fitted = candidate
		}
	}
	return fitted
}

// toolExecResult captures the outcome of a single tool call execution.
type toolExecResult struct {
	content      string
	status       string
	errMsg       string
	fatalToolErr error
	artifact     *domain.SystemAssistantToolArtifact
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
	case domain.SystemAssistantToolSearchOfficialDocs:
		return execOfficialDocsSearchTool(toolCtx, tc, s, toolStart)
	case domain.SystemAssistantToolDiagnoseTenant:
		return execDiagnoseTenantTool(toolCtx, tc, s, toolStart)
	case domain.SystemAssistantToolProposeResourceChange:
		return execProposeResourceChangeTool(toolCtx, tc, s, toolStart)
	default:
		return execMCPTool(toolCtx, tc, s, toolStart, provider, logger)
	}
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

func execProposeResourceChangeTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
	if !s.GovernedAssistant || s.ProposalCreateFn == nil {
		return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "proposal tool unavailable", content: "error: tool unavailable"}
	}
	proposal, callErr := s.ProposalCreateFn(toolCtx, tc.Arguments)
	if callErr != nil {
		message := safeAssistantToolError(callErr)
		if proposal.ID != "" {
			s.AssistantToolArtifacts = append(s.AssistantToolArtifacts, domain.SystemAssistantToolArtifact{
				Tool: tc.Name, Proposal: &proposal, LatencyMs: time.Since(toolStart).Milliseconds(),
				Outcome: "error", ErrorCode: assistantToolErrorCode(callErr.Error()),
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
	if s.RAGSearchFn == nil {
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
	content, ragErr := s.RAGSearchFn(ragCtx, workspaces, query, topK)
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
	return toolExecResult{content: content, status: domain.ToolTraceStatusSuccess}
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
		domain.SystemAssistantToolProposeResourceChange:
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
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "tool timeout"
	case errors.Is(err, context.Canceled):
		return "tool cancelled"
	case errors.Is(err, domain.ErrOfficialEvidenceNotFound):
		return "official evidence not found"
	case errors.Is(err, domain.ErrDiagnosticForbidden):
		return "diagnostic forbidden"
	default:
		return "evidence unavailable"
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

func appendToolObservation(s *ReActState, tc port.ToolCall, provider toolProviderRef, result toolExecResult, toolStart time.Time, toolLatencyMs int64) {
	summary := summarizeToolObservation(tc.Name, result.content, result.status, result.errMsg)
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
		Metadata:       provider.Metadata,
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

func extractStringSliceArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func clampTopK(args map[string]any) int {
	topK := 5
	if v, ok := args["top_k"].(float64); ok {
		topK = int(v)
		if topK > constants.MaxRAGTopK {
			topK = constants.MaxRAGTopK
		}
	}
	return topK
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
	case domain.SystemAssistantToolSearchOfficialDocs,
		domain.SystemAssistantToolDiagnoseTenant,
		domain.SystemAssistantToolProposeResourceChange:
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

// buildSkillToolDefinitions returns one tool definition per skill in the
// catalog, sorted by SkillID for deterministic ordering. Each tool uses the
// ActivationContract name, description, and input schema so the LLM selects
// by semantics rather than by opaque skill ID.
func buildSkillToolDefinitions(catalog map[string]port.SkillActivation) []port.ToolDefinition {
	if len(catalog) == 0 {
		return nil
	}
	sorted := make([]string, 0, len(catalog))
	for id := range catalog {
		sorted = append(sorted, id)
	}
	sort.Strings(sorted)
	out := make([]port.ToolDefinition, 0, len(sorted))
	for _, skillID := range sorted {
		activation := catalog[skillID]
		schema := activation.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		toolName := activation.Name
		if toolName == "" {
			toolName = activation.SkillID
		}
		out = append(out, port.ToolDefinition{
			Name:         toolName,
			Description:  activation.Description,
			InputSchema:  schema,
			ProviderType: domain.ProviderTypeSkill,
			ProviderID:   activation.SkillID,
			CapabilityID: activation.SkillID,
			NodeID:       toolName,
			NodeType:     domain.ObservationTypeSkill,
		})
	}
	return out
}

func effectiveTools(
	available []port.ToolDefinition,
	catalog map[string]port.SkillActivation,
	actives []port.SkillActivation,
	agentKnowledgeWorkspaceIDs []string,
	agentMemoryScope string,
	governedAssistant bool,
) []port.ToolDefinition {
	if governedAssistant {
		return append([]port.ToolDefinition(nil), available...)
	}
	out := make([]port.ToolDefinition, 0, len(available)+5)
	out = append(out, PlanToolDefinitions()...)
	out = append(out, buildSkillToolDefinitions(catalog)...)
	allowedMCP := map[string]struct{}{}
	for _, active := range actives {
		for _, id := range active.MCPToolIDs {
			allowedMCP[id] = struct{}{}
		}
	}
	for _, tool := range available {
		if isReservedPlanTool(tool.Name) {
			continue
		}
		if len(actives) > 0 && tool.Name == "stratum_recall_memory" && !anyActiveAllowsMemoryScope(actives, agentMemoryScope) {
			continue
		}
		if len(actives) > 0 && tool.Name == "stratum_search_knowledge" && len(allowedKnowledgeWorkspaces(nil, agentKnowledgeWorkspaceIDs, actives)) == 0 {
			continue
		}
		if tool.ProviderType == domain.ProviderTypeMCP && len(actives) > 0 {
			if _, ok := allowedMCP[tool.Name]; !ok {
				continue
			}
		}
		out = append(out, tool)
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
	case "diagnostic forbidden":
		return "forbidden"
	case "invalid official docs arguments", "invalid diagnostic arguments":
		return "invalid_arguments"
	default:
		return "unavailable"
	}
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

// upsertActivation 按 SkillID 原位替换（保留激活顺序）或末尾追加。
func upsertActivation(actives []port.SkillActivation, activation port.SkillActivation) []port.SkillActivation {
	for i, active := range actives {
		if active.SkillID == activation.SkillID {
			actives[i] = activation
			return actives
		}
	}
	return append(actives, activation)
}

// anyActiveAllowsMemoryScope 报告任一 active skill 的 MemoryScopes 包含 scope。
func anyActiveAllowsMemoryScope(actives []port.SkillActivation, scope string) bool {
	for _, active := range actives {
		if containsString(active.MemoryScopes, scope) {
			return true
		}
	}
	return false
}

func messagesWithActiveSkills(messages []port.LLMMessage, actives []port.SkillActivation) []port.LLMMessage {
	var instructions []port.LLMMessage
	for _, active := range actives {
		if active.Instructions == "" {
			continue
		}
		instructions = append(instructions, port.LLMMessage{
			Role:    "system",
			Content: fmt.Sprintf("Active Skill %s (revision %s):\n%s", active.Name, active.RevisionID, active.Instructions),
		})
	}
	if len(instructions) == 0 {
		return messages
	}
	// 多条指令作为连续块整体插入首个 system 消息之后；逐个插入会反转顺序。
	out := make([]port.LLMMessage, 0, len(messages)+len(instructions))
	if len(messages) > 0 && messages[0].Role == "system" {
		out = append(out, messages[0])
		out = append(out, instructions...)
		return append(out, messages[1:]...)
	}
	out = append(out, instructions...)
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
