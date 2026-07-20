package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

type agentLLMCompleter interface {
	Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error)
	CompleteStream(context.Context, *llmdomain.CompletionRequest, func(string)) (*llmdomain.CompletionResponse, error)
}

type agentLLMAdapter struct{ client agentLLMCompleter }

func newAgentLLMAdapter(client agentLLMCompleter) agentport.Adapter {
	return agentLLMAdapter{client: client}
}

func (a agentLLMAdapter) Route(ctx context.Context, req agentport.CapabilityRequest) (agentport.CapabilityResponse, error) {
	start := time.Now()
	llmReq := buildAgentLLMRequest(req.LLM)
	var response *llmdomain.CompletionResponse
	var err error
	if req.TokenStream != nil {
		response, err = a.client.CompleteStream(ctx, llmReq, req.TokenStream)
	} else {
		llmCtx, cancel := context.WithTimeout(ctx, constants.LLMRequestTimeout)
		defer cancel()
		response, err = a.client.Complete(llmCtx, llmReq)
	}
	if err != nil {
		return agentport.CapabilityResponse{}, fmt.Errorf("llm_adapter: %w", err)
	}
	return buildAgentCapabilityResponse(req.TraceID, response, time.Since(start)), nil
}

func buildAgentLLMRequest(req *agentport.LLMCapRequest) *llmdomain.CompletionRequest {
	messages := make([]llmdomain.Message, len(req.Messages))
	for i, message := range req.Messages {
		messages[i] = llmdomain.Message{
			Role: message.Role, Content: message.Content, ToolCallID: message.ToolCallID,
			ToolCalls: agentToolCallsToGateway(message.ToolCalls),
		}
	}
	tools := make([]llmdomain.Tool, len(req.Tools))
	for i, tool := range req.Tools {
		tools[i] = llmdomain.Tool{Type: "function", Function: llmdomain.ToolFunction{
			Name: tool.Name, Description: tool.Description, Parameters: tool.InputSchema,
		}}
	}
	choice := ""
	if len(tools) > 0 {
		choice = "auto"
	}
	return &llmdomain.CompletionRequest{
		Model: req.Model, Messages: messages, Tools: tools, ToolChoice: choice,
		Temperature: req.Temperature, MaxTokens: req.MaxTokens,
	}
}

func buildAgentCapabilityResponse(traceID string, raw *llmdomain.CompletionResponse, duration time.Duration) agentport.CapabilityResponse {
	toolCalls := make([]agentport.ToolCall, 0, len(raw.ToolCalls))
	for _, toolCall := range raw.ToolCalls {
		var arguments map[string]any
		if toolCall.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &arguments); err != nil {
				arguments = map[string]any{"_raw": toolCall.Function.Arguments}
			}
		}
		toolCalls = append(toolCalls, agentport.ToolCall{ID: toolCall.ID, Name: toolCall.Function.Name, Arguments: arguments})
	}
	return agentport.CapabilityResponse{
		TraceID: traceID, Type: agentport.CapLLM, Duration: duration, Content: raw.Content, ToolCalls: toolCalls,
		Usage: agentport.TokenUsage{Prompt: raw.Usage.PromptTokens, Completion: raw.Usage.CompletionTokens, Total: raw.Usage.TotalTokens},
	}
}

func agentToolCallsToGateway(toolCalls []agentport.ToolCall) []llmdomain.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	result := make([]llmdomain.ToolCall, len(toolCalls))
	for i, toolCall := range toolCalls {
		arguments, _ := json.Marshal(toolCall.Arguments)
		result[i] = llmdomain.ToolCall{ID: toolCall.ID, Type: "function"}
		result[i].Function.Name = toolCall.Name
		result[i].Function.Arguments = string(arguments)
	}
	return result
}
