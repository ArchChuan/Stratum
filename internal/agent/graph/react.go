package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
)

const (
	nodeLLM  = "llm"
	nodeTool = "tool"
)

// ReActState is the mutable state threaded through the ReAct graph.
type ReActState struct {
	TenantID       string
	TraceID        string
	LLMAPIKeys     map[string]string
	Model          string
	SystemPrompt   string
	AvailableTools []capgateway.ToolDefinition
	Messages       []capgateway.LLMMessage
	AllToolCalls   []capgateway.ToolCall
	Output         string
	Steps          int
}

// BuildReActGraph constructs and compiles the ReAct agent graph.
func BuildReActGraph(capGW capgateway.CapabilityGateway) (*CompiledGraph[ReActState], error) {
	g := New[ReActState]()
	g.AddNode(nodeLLM, makeLLMNode(capGW))
	g.AddNode(nodeTool, makeToolNode(capGW))
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

func makeLLMNode(capGW capgateway.CapabilityGateway) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		resp, err := RetryFn(ctx, DefaultRetry, func() (capgateway.CapabilityResponse, error) {
			return capGW.Route(ctx, capgateway.CapabilityRequest{
				TraceID:    s.TraceID,
				TenantID:   s.TenantID,
				Type:       capgateway.CapLLM,
				Timeout:    60 * time.Second,
				LLMAPIKeys: s.LLMAPIKeys,
				LLM: &capgateway.LLMCapRequest{
					Model:    s.Model,
					Messages: s.Messages,
					Tools:    s.AvailableTools,
				},
			})
		})
		if err != nil {
			return s, fmt.Errorf("react llm node: %w", err)
		}
		s.Steps++
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

func makeToolNode(capGW capgateway.CapabilityGateway) NodeFunc[ReActState] {
	return func(ctx context.Context, s ReActState) (ReActState, error) {
		if len(s.Messages) == 0 {
			return s, nil
		}
		last := s.Messages[len(s.Messages)-1]
		for _, tc := range last.ToolCalls {
			toolResp, err := capGW.Route(ctx, capgateway.CapabilityRequest{
				TraceID:  s.TraceID,
				TenantID: s.TenantID,
				Type:     capgateway.CapSkill,
				Timeout:  30 * time.Second,
				Skill:    &capgateway.SkillCapRequest{SkillID: tc.Name, Input: tc.Arguments},
			})
			content := ""
			switch {
			case err != nil:
				content = fmt.Sprintf("error: %v", err)
			case toolResp.Output != nil:
				content = fmt.Sprintf("%v", toolResp.Output)
			default:
				content = toolResp.Content
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
