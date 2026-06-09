package capgateway_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockLLMGateway struct {
	resp *llmgateway.CompletionResponse
	err  error
}

func (m *mockLLMGateway) Complete(_ context.Context, _ *llmgateway.CompletionRequest) (*llmgateway.CompletionResponse, error) {
	return m.resp, m.err
}

func TestLLMAdapter_RouteTextContent(t *testing.T) {
	mock := &mockLLMGateway{
		resp: &llmgateway.CompletionResponse{
			Content: "hello world",
			Model:   "qwen-turbo",
		},
	}
	adapter := capgateway.NewLLMAdapter(mock, zap.NewNop())

	req := capgateway.CapabilityRequest{
		TraceID:  "trace-1",
		TenantID: "t1",
		Type:     capgateway.CapLLM,
		LLM: &capgateway.LLMCapRequest{
			Model:    "qwen-turbo",
			Messages: []capgateway.LLMMessage{{Role: "user", Content: "hi"}},
		},
	}
	resp, err := adapter.Route(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "hello world", resp.Content)
	require.Empty(t, resp.ToolCalls)
}

func TestLLMAdapter_RouteToolCalls(t *testing.T) {
	mock := &mockLLMGateway{
		resp: &llmgateway.CompletionResponse{
			ToolCalls: []llmgateway.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{Name: "get_weather", Arguments: `{"city":"Beijing"}`},
			}},
		},
	}
	adapter := capgateway.NewLLMAdapter(mock, zap.NewNop())

	req := capgateway.CapabilityRequest{
		Type: capgateway.CapLLM,
		LLM:  &capgateway.LLMCapRequest{Model: "qwen-turbo", Messages: []capgateway.LLMMessage{{Role: "user", Content: "weather?"}}},
	}
	resp, err := adapter.Route(context.Background(), req)
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	require.Equal(t, "get_weather", resp.ToolCalls[0].Name)
	require.Equal(t, "Beijing", resp.ToolCalls[0].Arguments["city"])
}

func TestLLMAdapter_RouteError(t *testing.T) {
	mock := &mockLLMGateway{err: errors.New("upstream down")}
	adapter := capgateway.NewLLMAdapter(mock, zap.NewNop())

	req := capgateway.CapabilityRequest{
		Type: capgateway.CapLLM,
		LLM:  &capgateway.LLMCapRequest{Model: "qwen-turbo", Messages: []capgateway.LLMMessage{{Role: "user", Content: "hi"}}},
	}
	_, err := adapter.Route(context.Background(), req)
	require.Error(t, err)
}
