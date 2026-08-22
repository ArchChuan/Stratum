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

// TestTemperaturePtrOrNilRoundsToTwoDecimals 回归 Agent ReAct 主链路温度泄漏：
// float64(float32(0.7)) 直转变成 0.699999988079071，智谱等端点校验小数位返回
// 400；0 = unset → nil（网关按模型权威数据注入默认）。
func TestTemperaturePtrOrNilRoundsToTwoDecimals(t *testing.T) {
	if got := temperaturePtrOrNil(0.7); got == nil || *got != 0.7 {
		t.Fatalf("temperaturePtrOrNil(0.7) = %v, want 0.7 (2 位小数)", got)
	}
	if got := temperaturePtrOrNil(0.1); got == nil || *got != 0.1 {
		t.Fatalf("temperaturePtrOrNil(0.1) = %v, want 0.1", got)
	}
	if got := temperaturePtrOrNil(0); got != nil {
		t.Fatalf("temperaturePtrOrNil(0) = %v, want nil (unset)", got)
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
