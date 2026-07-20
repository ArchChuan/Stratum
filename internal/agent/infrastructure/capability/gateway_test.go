package capgateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type adapterFunc func(context.Context, port.CapabilityRequest) (port.CapabilityResponse, error)

func (f adapterFunc) Route(ctx context.Context, req port.CapabilityRequest) (port.CapabilityResponse, error) {
	return f(ctx, req)
}

func TestDefaultCapabilityGateway_RouteLLM(t *testing.T) {
	gw := capgateway.NewDefaultCapabilityGateway(
		adapterFunc(func(context.Context, port.CapabilityRequest) (port.CapabilityResponse, error) {
			return port.CapabilityResponse{Content: "ok"}, nil
		}),
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
		adapterFunc(func(context.Context, port.CapabilityRequest) (port.CapabilityResponse, error) {
			return port.CapabilityResponse{}, errors.New("must not route invalid request")
		}),
		zap.NewNop(),
	)
	req := port.CapabilityRequest{Type: port.CapLLM} // LLM == nil
	_, err := gw.Route(context.Background(), req)
	require.Error(t, err)
}
