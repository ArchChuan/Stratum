package capgateway_test

import (
	"context"
	"testing"
	"time"

	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	skillgateway "github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestDefaultCapabilityGateway_RouteLLM(t *testing.T) {
	llmMock := &mockLLMGateway{resp: &llmgateway.CompletionResponse{Content: "ok"}}
	skillMock := &mockSkillGateway{}
	gw := capgateway.NewDefaultCapabilityGateway(
		capgateway.NewLLMAdapter(llmMock, zap.NewNop()),
		capgateway.NewSkillAdapter(skillMock, zap.NewNop()),
		zap.NewNop(),
	)

	req := capgateway.CapabilityRequest{
		TraceID:  "t1",
		TenantID: "tenant1",
		Type:     capgateway.CapLLM,
		Timeout:  5 * time.Second,
		LLM:      &capgateway.LLMCapRequest{Model: "qwen-turbo", Messages: []capgateway.LLMMessage{{Role: "user", Content: "hi"}}},
	}
	resp, err := gw.Route(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "ok", resp.Content)
}

func TestDefaultCapabilityGateway_RouteSkill(t *testing.T) {
	llmMock := &mockLLMGateway{}
	skillMock := &mockSkillGateway{resp: skillgateway.SkillResponse{Output: "result"}}
	gw := capgateway.NewDefaultCapabilityGateway(
		capgateway.NewLLMAdapter(llmMock, zap.NewNop()),
		capgateway.NewSkillAdapter(skillMock, zap.NewNop()),
		zap.NewNop(),
	)

	req := capgateway.CapabilityRequest{
		Type:  capgateway.CapSkill,
		Skill: &capgateway.SkillCapRequest{SkillID: "s1", Input: "data"},
	}
	resp, err := gw.Route(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "result", resp.Output)
}

func TestDefaultCapabilityGateway_RouteValidationError(t *testing.T) {
	gw := capgateway.NewDefaultCapabilityGateway(
		capgateway.NewLLMAdapter(&mockLLMGateway{}, zap.NewNop()),
		capgateway.NewSkillAdapter(&mockSkillGateway{}, zap.NewNop()),
		zap.NewNop(),
	)
	req := capgateway.CapabilityRequest{Type: capgateway.CapLLM} // LLM == nil
	_, err := gw.Route(context.Background(), req)
	require.Error(t, err)
}
