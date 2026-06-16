package graph

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/capgateway"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

const (
	nodeLLM         = "llm"
	nodeTool        = "tool"
	reactLLMTimeout = 60 * time.Second
)

// ReActState is the mutable state threaded through the ReAct graph.
type ReActState struct {
	TenantID       string
	TraceID        string
	ConversationID string
	LLMAPIKeys     map[string]string
	Model          string
	AvailableTools []capgateway.ToolDefinition
	Messages       []capgateway.LLMMessage
	AllToolCalls   []capgateway.ToolCall
	Output         string
	Steps          int
	TotalTokens    int
	OnToken        func(string) // if non-nil, stream tokens from the final LLM response
	RAGSearchFn    func(ctx context.Context, workspaces []string, query string, topK int) (string, error)
	RecallMemoryFn func(ctx context.Context, input map[string]any) (string, error)
}

// BuildReActGraph constructs and compiles the ReAct agent graph.
func BuildReActGraph(capGW capgateway.CapabilityGateway, logger *zap.Logger) (*CompiledGraph[ReActState], error) {
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

func makeLLMNode(capGW capgateway.CapabilityGateway, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		start := time.Now()
		resp, err := RetryFn(ctx, DefaultRetry, func() (capgateway.CapabilityResponse, error) {
			return capGW.Route(ctx, capgateway.CapabilityRequest{
				TraceID:     s.TraceID,
				TenantID:    s.TenantID,
				Type:        capgateway.CapLLM,
				Timeout:     reactLLMTimeout,
				LLMAPIKeys:  s.LLMAPIKeys,
				TokenStream: s.OnToken,
				LLM: &capgateway.LLMCapRequest{
					Model:    s.Model,
					Messages: s.Messages,
					Tools:    s.AvailableTools,
				},
			})
		})
		latencyMs := time.Since(start).Milliseconds()
		if err != nil {
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
		logger.Info("react.llm",
			zap.String("trace_id", s.TraceID),
			zap.String("tenant_id", s.TenantID),
			zap.String("conversation_id", s.ConversationID),
			zap.String("model", s.Model),
			zap.Int("step", s.Steps),
			zap.Int("tokens", resp.Usage.Total),
			zap.Int("total_tokens", s.TotalTokens),
			zap.Int64("latency_ms", latencyMs),
			zap.Bool("has_tool_calls", len(resp.ToolCalls) > 0),
		)
		if len(resp.ToolCalls) == 0 {
			s.Output = resp.Content
			s.Messages = append(s.Messages, capgateway.LLMMessage{
				Role:    "assistant",
				Content: resp.Content,
			})
		} else {
			s.Messages = append(s.Messages, capgateway.LLMMessage{
				Role:      "assistant",
				ToolCalls: resp.ToolCalls,
			})
		}
		return s, nil
	}
}

func makeToolNode(capGW capgateway.CapabilityGateway, logger *zap.Logger) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if len(s.Messages) == 0 {
			return s, nil
		}
		last := s.Messages[len(s.Messages)-1]
		for _, tc := range last.ToolCalls {
			toolStart := time.Now()
			var content string
			switch tc.Name {
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
					content, ragErr = s.RAGSearchFn(ctx, workspaces, query, topK)
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
					content, recallErr = s.RecallMemoryFn(ctx, tc.Arguments)
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
				toolResp, err := capGW.Route(ctx, capgateway.CapabilityRequest{
					TraceID:  s.TraceID,
					TenantID: s.TenantID,
					Type:     capgateway.CapSkill,
					Timeout:  30 * time.Second,
					Skill:    &capgateway.SkillCapRequest{SkillID: tc.Name, Input: tc.Arguments},
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
			s.Messages = append(s.Messages, capgateway.LLMMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: tc.ID,
			})
			s.AllToolCalls = append(s.AllToolCalls, tc)
		}
		return s, nil
	}
}
