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
	// SkillToolIndex maps tenant-scoped tool names ("tenant_{id}_{name}") to their skill UUIDs.
	SkillToolIndex map[string]string
	Messages       []port.LLMMessage
	AllToolCalls   []port.ToolCall
	Output         string
	Steps          int
	TotalTokens    int
	OnToken        func(string) // if non-nil, stream tokens from the final LLM response
	RAGSearchFn    func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	RecallMemoryFn func(ctx context.Context, input map[string]any) (string, error)
	// MaxLLMSteps caps LLM-node invocations; on the last allowed call tools are
	// stripped and the model is asked to produce a final answer from collected context.
	MaxLLMSteps int
}

// BuildReActGraph constructs and compiles the ReAct agent graph.
func BuildReActGraph(capGW port.CapabilityGateway, logger *zap.Logger) (*CompiledGraph[ReActState], error) {
	g := New[ReActState]()
	g.AddNode(nodeLLM, makeLLMNode(capGW, logger))
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

func makeLLMNode(capGW port.CapabilityGateway, logger *zap.Logger) NodeFunc[ReActState] {
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
		s.TotalTokens += resp.Usage.Total
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
			zap.Int("prompt_tokens", resp.Usage.Prompt),
			zap.Int("completion_tokens", resp.Usage.Completion),
			zap.Int("total_tokens", s.TotalTokens),
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
		} else {
			s.Messages = append(s.Messages, port.LLMMessage{
				Role:      "assistant",
				ToolCalls: resp.ToolCalls,
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
				skillID, ok := s.SkillToolIndex[tc.Name]
				if !ok {
					content = fmt.Sprintf("error: unknown tool %q", tc.Name)
					logger.Error("react.tool.unknown",
						zap.String("trace_id", s.TraceID),
						zap.String("tool_name", tc.Name),
						zap.String("tool_call_id", tc.ID),
					)
					break
				}
				toolResp, err := capGW.Route(ctx, port.CapabilityRequest{
					TraceID:  s.TraceID,
					TenantID: s.TenantID,
					Type:     port.CapSkill,
					Timeout:  30 * time.Second,
					Skill:    &port.SkillCapRequest{SkillID: skillID, Input: tc.Arguments},
				})
				toolLatencyMs := time.Since(toolStart).Milliseconds()
				switch {
				case err != nil:
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
