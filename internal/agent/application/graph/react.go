package graph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
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
	LLMAPIKeys     map[string]string
	Model          string
	AvailableTools []port.ToolDefinition
	// SkillToolIndex maps exposed tool names to skill/version refs.
	SkillToolIndex   map[string]port.SkillToolRef
	Messages         []port.LLMMessage
	AllToolCalls     []port.ToolCall
	ToolObservations []domain.ToolObservation
	TraceEvents      []domain.AgentTraceEvent
	Output           string
	Steps            int
	TotalTokens      int
	TotalCostUSD     float64
	OnToken          func(string) // if non-nil, stream tokens from the final LLM response
	RAGSearchFn      func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	RecallMemoryFn   func(ctx context.Context, input map[string]any) (string, error)
	// MaxLLMSteps caps LLM-node invocations; on the last allowed call tools are
	// stripped and the model is asked to produce a final answer from collected context.
	MaxLLMSteps int

	// Lazy planning — non-zero StuckThreshold enables Reflect→Plan→Execute path.
	StuckThreshold    int // 0 = disabled
	PlanTriggered     bool
	ReflectionSummary string
	Plan              []domain.PlanStep
	PlanTemplateID    string
	CurrentStepIndex  int
	StepResults       []domain.StepResult
	CheckpointEnabled bool
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

		tools := s.AvailableTools
		messages := s.Messages
		if s.MaxLLMSteps > 0 && s.Steps >= s.MaxLLMSteps-1 {
			tools = nil
			messages = append(messages, port.LLMMessage{
				Role:    "user",
				Content: "You have reached the maximum reasoning steps. Based on your analysis and tool results so far, provide your final answer now. Do not call any tools.",
			})
		}

		tracer := otel.Tracer("stratum/agent")
		ctx, llmSpan := tracer.Start(ctx, "react.llm",
			oteltrace.WithAttributes(
				attribute.String("llm.model", s.Model),
				attribute.Int("react.step", s.Steps+1),
			),
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
			llmSpan.RecordError(err)
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
			attribute.Bool("llm.has_tool_calls", len(resp.ToolCalls) > 0),
		)
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

func makeToolNode(capGW port.CapabilityGateway, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if len(s.Messages) == 0 {
			return s, nil
		}
		tracer := otel.Tracer("stratum/agent")
		last := s.Messages[len(s.Messages)-1]
		for _, tc := range last.ToolCalls {
			toolStart := time.Now()
			_, toolSpan := tracer.Start(ctx, "react.tool",
				oteltrace.WithAttributes(
					attribute.String("tool.name", tc.Name),
					attribute.Int("react.step", s.Steps),
				),
			)
			var content string
			provider := classifyToolProvider(tc.Name, s.AvailableTools, s.SkillToolIndex)
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
			case "stratum_continue_reasoning":
				content = "Continuing reasoning..."
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
					topK := 5
					if v, ok := tc.Arguments["top_k"].(float64); ok {
						topK = int(v)
						if topK > constants.MaxRAGTopK {
							topK = constants.MaxRAGTopK
						}
					}
					var ragErr error
					ragCtx, ragCancel := context.WithTimeout(ctx, constants.AgentRAGSearchTimeout)
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
				if s.RecallMemoryFn == nil {
					content = "error: stratum_recall_memory tool not configured"
				} else {
					var recallErr error
					recallCtx, recallCancel := context.WithTimeout(ctx, constants.AgentMemoryRecallTimeout)
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
				skillRef, ok := s.SkillToolIndex[tc.Name]
				if !ok && provider.ProviderType != domain.ProviderTypeMCP {
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
				skillID := skillRef.SkillID
				versionID := skillRef.VersionID
				if provider.ProviderType == domain.ProviderTypeMCP {
					skillID = tc.Name
				}
				toolResp, err := capGW.Route(ctx, port.CapabilityRequest{
					TraceID:  s.TraceID,
					TenantID: s.TenantID,
					Type:     port.CapSkill,
					Timeout:  30 * time.Second,
					Skill: &port.SkillCapRequest{
						SkillID:   skillID,
						VersionID: versionID,
						Input:     tc.Arguments,
					},
				})
				toolLatencyMs := time.Since(toolStart).Milliseconds()
				switch {
				case err != nil:
					status = domain.ToolTraceStatusError
					errMsg = err.Error()
					logger.Error("react.tool",
						zap.String("trace_id", s.TraceID),
						zap.String("tenant_id", s.TenantID),
						zap.String("conversation_id", s.ConversationID),
						zap.String("tool_name", tc.Name),
						zap.Int64("latency_ms", toolLatencyMs),
						zap.Error(err),
					)
					content = fmt.Sprintf("error: %v", err)
				case toolResp.Output != nil:
					logger.Info("react.tool",
						zap.String("trace_id", s.TraceID),
						zap.String("tenant_id", s.TenantID),
						zap.String("conversation_id", s.ConversationID),
						zap.String("tool_name", tc.Name),
						zap.Int64("latency_ms", toolLatencyMs),
					)
					content = fmt.Sprintf("%v", toolResp.Output)
				default:
					logger.Info("react.tool",
						zap.String("trace_id", s.TraceID),
						zap.String("tenant_id", s.TenantID),
						zap.String("conversation_id", s.ConversationID),
						zap.String("tool_name", tc.Name),
						zap.Int64("latency_ms", toolLatencyMs),
					)
					content = toolResp.Content
				}
			}
			toolLatencyMs := time.Since(toolStart).Milliseconds()
			if errMsg != "" {
				toolSpan.RecordError(fmt.Errorf("%s", errMsg))
			}
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

func classifyToolProvider(name string, tools []port.ToolDefinition, skillIndex map[string]port.SkillToolRef) toolProviderRef {
	switch name {
	case "stratum_continue_reasoning":
		return toolProviderRef{ToolType: domain.ToolTypeReasoning, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_search_knowledge":
		return toolProviderRef{ToolType: domain.ToolTypeBuiltinRAG, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
	case "stratum_recall_memory":
		return toolProviderRef{ToolType: domain.ToolTypeBuiltinMemory, ProviderType: domain.ProviderTypeBuiltin, ProviderID: name, CapabilityID: name, NodeID: nodeTool, NodeType: domain.ObservationTypeTool}
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
		if ref, ok := skillIndex[name]; ok {
			return toolProviderRef{
				ToolType:     domain.ToolTypeSkill,
				ProviderType: domain.ProviderTypeSkill,
				ProviderID:   ref.SkillID,
				CapabilityID: ref.SkillID,
				NodeID:       name,
				NodeType:     domain.ObservationTypeSkill,
				Metadata:     map[string]any{"version_id": ref.VersionID},
			}
		}
		return toolProviderRef{ToolType: domain.ToolTypeInternal, ProviderType: domain.ProviderTypeInternal, ProviderID: name, CapabilityID: name, NodeID: name, NodeType: domain.ToolTypeInternal}
	}
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
