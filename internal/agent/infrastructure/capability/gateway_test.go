package capgateway_test

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDefaultCapabilityGateway_RouteLLM(t *testing.T) {
	llmMock := &mockLLMGateway{resp: &llmgateway.CompletionResponse{Content: "ok"}}
	gw := capgateway.NewDefaultCapabilityGateway(
		capgateway.NewLLMAdapter(llmMock, zap.NewNop()),
		zap.NewNop(),
	)

	req := port.CapabilityRequest{
		TraceID:  "t1",
		TenantID: "tenant1",
		Type:     port.CapLLM,
		Timeout:  5 * time.Second,
		LLM:      &port.LLMCapRequest{Model: "qwen-turbo", Messages: []port.LLMMessage{{Role: "user", Content: "hi"}}},
	}
	resp, err := gw.Route(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Content)
}

func TestDefaultCapabilityGateway_RouteValidationError(t *testing.T) {
	gw := capgateway.NewDefaultCapabilityGateway(
		capgateway.NewLLMAdapter(&mockLLMGateway{}, zap.NewNop()),
		zap.NewNop(),
	)
	req := port.CapabilityRequest{Type: port.CapLLM} // LLM == nil
	_, err := gw.Route(context.Background(), req)
	require.Error(t, err)
}
