package capgateway

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"go.uber.org/zap"
)

// LLMCompleter is the subset of llmgateway.Gateway needed by LLMAdapter.
type LLMCompleter interface {
	Complete(ctx context.Context, req *llmgateway.CompletionRequest) (*llmgateway.CompletionResponse, error)
}

// LLMAdapter bridges CapabilityRequest → llmgateway and back.
type LLMAdapter struct {
	gw     LLMCompleter
	logger *zap.Logger
}

func NewLLMAdapter(gw LLMCompleter, logger *zap.Logger) *LLMAdapter {
	return &LLMAdapter{gw: gw, logger: logger}
}

func (a *LLMAdapter) Route(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error) {
	start := time.Now()
	llmReq := buildLLMRequest(req.LLM)
	raw, err := a.gw.Complete(ctx, llmReq)
	if err != nil {
		return CapabilityResponse{}, fmt.Errorf("llm_adapter: %w", err)
	}
	return buildCapabilityResponse(req.TraceID, raw, time.Since(start)), nil
}

func buildLLMRequest(r *LLMCapRequest) *llmgateway.CompletionRequest {
	msgs := make([]llmgateway.Message, len(r.Messages))
	for i, m := range r.Messages {
		msgs[i] = llmgateway.Message{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			ToolCalls:  convertToolCallsToGW(m.ToolCalls),
		}
	}

	tools := make([]llmgateway.Tool, len(r.Tools))
	for i, td := range r.Tools {
		tools[i] = llmgateway.Tool{
			Type: "function",
			Function: llmgateway.ToolFunction{
				Name:        td.Name,
				Description: td.Description,
				Parameters:  td.InputSchema,
			},
		}
	}

	return &llmgateway.CompletionRequest{
		Model:       r.Model,
		Messages:    msgs,
		Tools:       tools,
		ToolChoice:  choiceFromTools(tools),
		Temperature: r.Temperature,
		MaxTokens:   r.MaxTokens,
	}
}

func choiceFromTools(tools []llmgateway.Tool) string {
	if len(tools) == 0 {
		return ""
	}
	return "auto"
}

func buildCapabilityResponse(traceID string, raw *llmgateway.CompletionResponse, dur time.Duration) CapabilityResponse {
	tcs := make([]ToolCall, 0, len(raw.ToolCalls))
	for _, tc := range raw.ToolCalls {
		var args map[string]any
		if tc.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]any{"_raw": tc.Function.Arguments}
			}
		}
		tcs = append(tcs, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return CapabilityResponse{
		TraceID:   traceID,
		Type:      CapLLM,
		Duration:  dur,
		Content:   raw.Content,
		ToolCalls: tcs,
		Usage: TokenUsage{
			Prompt:     raw.Usage.PromptTokens,
			Completion: raw.Usage.CompletionTokens,
			Total:      raw.Usage.TotalTokens,
		},
	}
}

func convertToolCallsToGW(tcs []ToolCall) []llmgateway.ToolCall {
	if len(tcs) == 0 {
		return nil
	}
	out := make([]llmgateway.ToolCall, len(tcs))
	for i, tc := range tcs {
		argsJSON, _ := json.Marshal(tc.Arguments)
		out[i] = llmgateway.ToolCall{
			ID:   tc.ID,
			Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{
				Name:      tc.Name,
				Arguments: string(argsJSON),
			},
		}
	}
	return out
}
