package capgateway_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/capgateway"
	skillgateway "github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockSkillGateway struct {
	resp skillgateway.SkillResponse
	err  error
}

func (m *mockSkillGateway) Execute(_ context.Context, _ skillgateway.SkillRequest) (skillgateway.SkillResponse, error) {
	return m.resp, m.err
}

func TestSkillAdapter_RouteSuccess(t *testing.T) {
	mock := &mockSkillGateway{
		resp: skillgateway.SkillResponse{SkillID: "skill_a", Output: "42"},
	}
	adapter := capgateway.NewSkillAdapter(mock, zap.NewNop())

	req := capgateway.CapabilityRequest{
		TraceID:  "tr1",
		TenantID: "t1",
		Type:     capgateway.CapSkill,
		Skill:    &capgateway.SkillCapRequest{SkillID: "skill_a", Input: map[string]any{"x": 1}},
	}
	resp, err := adapter.Route(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, capgateway.CapSkill, resp.Type)
	require.Equal(t, "42", resp.Output)
}

func TestSkillAdapter_RouteError(t *testing.T) {
	mock := &mockSkillGateway{err: errors.New("skill failed")}
	adapter := capgateway.NewSkillAdapter(mock, zap.NewNop())

	req := capgateway.CapabilityRequest{
		Type:  capgateway.CapSkill,
		Skill: &capgateway.SkillCapRequest{SkillID: "skill_b"},
	}
	_, err := adapter.Route(context.Background(), req)
	require.Error(t, err)
}
