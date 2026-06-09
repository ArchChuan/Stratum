package workflow

import (
	"fmt"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func ReActWorkflow(ctx workflow.Context, req ReActRequest) (*ReActResult, error) {
	messages := buildInitialMessages(req.AgentCfg, req.Input)
	var allToolCalls []capgateway.ToolCall
	steps := 0

	for i := 0; i < maxIterations(req.AgentCfg.MaxIterations); i++ {
		llmResp, err := callCapabilityActivity(ctx, capgateway.CapabilityRequest{
			TraceID:  req.TraceID,
			TenantID: req.TenantID,
			Type:     capgateway.CapLLM,
			Timeout:  60 * time.Second,
			LLM: &capgateway.LLMCapRequest{
				Model:    req.AgentCfg.LLMModel,
				Messages: messages,
				Tools:    req.AvailableTools,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("react: llm step %d: %w", i, err)
		}
		steps++

		if len(llmResp.ToolCalls) == 0 {
			return &ReActResult{Output: llmResp.Content, ToolCalls: allToolCalls, Steps: steps}, nil
		}

		messages = append(messages, capgateway.LLMMessage{
			Role:      "assistant",
			ToolCalls: llmResp.ToolCalls,
		})

		for _, tc := range llmResp.ToolCalls {
			toolResp, err := callCapabilityActivity(ctx, capgateway.CapabilityRequest{
				TraceID:  req.TraceID,
				TenantID: req.TenantID,
				Type:     capgateway.CapSkill,
				Timeout:  30 * time.Second,
				Skill:    &capgateway.SkillCapRequest{SkillID: tc.Name, Input: tc.Arguments},
			})
			messages = append(messages, formatToolResult(tc, toolResp, err))
			allToolCalls = append(allToolCalls, tc)
		}
	}

	return nil, fmt.Errorf("react: max iterations reached: %d", req.AgentCfg.MaxIterations)
}

func callCapabilityActivity(ctx workflow.Context, req capgateway.CapabilityRequest) (capgateway.CapabilityResponse, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: req.Timeout,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:    3,
			InitialInterval:    100 * time.Millisecond,
			BackoffCoefficient: 2.0,
		},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)
	var resp capgateway.CapabilityResponse
	err := workflow.ExecuteActivity(actCtx, ExecuteCapabilityActivityName, req).Get(actCtx, &resp)
	return resp, err
}

func buildInitialMessages(cfg AgentWorkflowConfig, input string) []capgateway.LLMMessage {
	msgs := make([]capgateway.LLMMessage, 0, 2)
	if cfg.SystemPrompt != "" {
		msgs = append(msgs, capgateway.LLMMessage{Role: "system", Content: cfg.SystemPrompt})
	}
	msgs = append(msgs, capgateway.LLMMessage{Role: "user", Content: input})
	return msgs
}

func formatToolResult(tc capgateway.ToolCall, resp capgateway.CapabilityResponse, err error) capgateway.LLMMessage {
	content := ""
	if err != nil {
		content = fmt.Sprintf("error: %v", err)
	} else if resp.Output != nil {
		content = fmt.Sprintf("%v", resp.Output)
	}
	return capgateway.LLMMessage{
		Role:       "tool",
		Content:    content,
		ToolCallID: tc.ID,
	}
}

func maxIterations(n int) int {
	if n <= 0 {
		return 10
	}
	return n
}
