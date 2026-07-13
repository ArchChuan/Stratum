package capgateway_test

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	capgateway "github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type mockSkillGateway struct {
	output       any
	err          error
	gotSkillID   string
	gotVersionID string
}

func (m *mockSkillGateway) Execute(_ context.Context, _, skillID, versionID string, _ any) (any, error) {
	m.gotSkillID = skillID
	m.gotVersionID = versionID
	return m.output, m.err
}

func TestSkillAdapter_RouteSuccess(t *testing.T) {
	mock := &mockSkillGateway{output: "42"}
	adapter := capgateway.NewSkillAdapter(mock, zap.NewNop())

	req := port.CapabilityRequest{
		TraceID:  "tr1",
		TenantID: "t1",
		Type:     port.CapSkill,
		Skill:    &port.SkillCapRequest{SkillID: "skill_a", Input: map[string]any{"x": 1}},
	}
	resp, err := adapter.Route(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, port.CapSkill, resp.Type)
	require.Equal(t, "42", resp.Output)
}

func TestSkillAdapter_RoutePassesVersionID(t *testing.T) {
	mock := &mockSkillGateway{output: "42"}
	adapter := capgateway.NewSkillAdapter(mock, zap.NewNop())

	req := port.CapabilityRequest{
		TraceID:  "tr1",
		TenantID: "t1",
		Type:     port.CapSkill,
		Skill: &port.SkillCapRequest{
			SkillID:   "skill_a",
			VersionID: "version_a",
			Input:     map[string]any{"x": 1},
		},
	}
	_, err := adapter.Route(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, "skill_a", mock.gotSkillID)
	require.Equal(t, "version_a", mock.gotVersionID)
}

func TestSkillAdapter_RouteError(t *testing.T) {
	mock := &mockSkillGateway{err: errors.New("skill failed")}
	adapter := capgateway.NewSkillAdapter(mock, zap.NewNop())

	req := port.CapabilityRequest{
		Type:  port.CapSkill,
		Skill: &port.SkillCapRequest{SkillID: "skill_b"},
	}
	_, err := adapter.Route(context.Background(), req)
	require.Error(t, err)
}
