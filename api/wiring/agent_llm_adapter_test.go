package wiring

import (
	"context"
	"errors"
	"testing"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type agentLLMStub struct {
	response *llmdomain.CompletionResponse
	err      error
}

func (s agentLLMStub) Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	return s.response, s.err
}

func (s agentLLMStub) CompleteStream(_ context.Context, _ *llmdomain.CompletionRequest, onToken func(string)) (*llmdomain.CompletionResponse, error) {
	if s.err == nil && s.response != nil {
		onToken(s.response.Content)
	}
	return s.response, s.err
}

func TestAgentLLMAdapterMapsTextAndToolCalls(t *testing.T) {
	response := &llmdomain.CompletionResponse{Content: "hello"}
	response.ToolCalls = []llmdomain.ToolCall{{ID: "call-1", Type: "function"}}
	response.ToolCalls[0].Function.Name = "weather"
	response.ToolCalls[0].Function.Arguments = `{"city":"Beijing"}`

	got, err := newAgentLLMAdapter(agentLLMStub{response: response}).Route(context.Background(), agentport.CapabilityRequest{
		TraceID: "trace-1", Type: agentport.CapLLM,
		LLM: &agentport.LLMCapRequest{Messages: []agentport.LLMMessage{{Role: "user", Content: "hi"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" || len(got.ToolCalls) != 1 || got.ToolCalls[0].Arguments["city"] != "Beijing" {
		t.Fatalf("unexpected mapped response: %#v", got)
	}
}

func TestAgentLLMAdapterPropagatesProviderError(t *testing.T) {
	_, err := newAgentLLMAdapter(agentLLMStub{err: errors.New("upstream down")}).Route(context.Background(), agentport.CapabilityRequest{
		Type: agentport.CapLLM, LLM: &agentport.LLMCapRequest{},
	})
	if err == nil {
		t.Fatal("expected provider error")
	}
}
