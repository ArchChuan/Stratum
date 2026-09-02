package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// TestExecuteSkillScenarioRevisionBuildsFromRevision 验证评测 skill 场景的 revision
// 变体（D7）：用锁定 revision 构建被测 agent 并执行，不读 Registry 当前生产配置。
// repo 不设置 Get expectation —— 若实现误读 Registry，testify mock 会 panic。
func TestExecuteSkillScenarioRevisionBuildsFromRevision(t *testing.T) {
	repo := new(mockAgentRepo)
	gw := &mockCapGW{responses: []port.CapabilityResponse{{Content: "scenario done"}}}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		Registry:             agent.NewRegistry(repo, zap.NewNop()),
		TenantResolver:       countingRevisionTenantResolver{gateway: gw},
		TenantModelValidator: lenientModelValidator{},
		Logger:               zap.NewNop(),
	})
	rev := domain.AgentRevision{
		AgentID: "agent-1", Type: domain.ReActAgent,
		Model: "qwen-max", SystemPrompt: "you are helpful",
		MaxIterations: 5,
		ModelParameters: domain.ModelParameters{
			MaxContextTokens: 8000, Temperature: 0.7, MaxTokens: 2000,
		},
	}
	req := agent.ExecRequest{Query: "hi", UserID: "u1"}
	meta := agent.ExecMeta{TenantID: "t1", TraceID: "trace-1"}

	result, _, err := svc.ExecuteSkillScenarioRevision(context.Background(), rev, req, meta,
		[]port.SkillActivation{{SkillID: "s1", RevisionID: "rev-s1"}})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "scenario done", result.Output)
	repo.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
}

// TestExecuteSkillScenarioRevisionValidatesRevision 验证 revision 变体先校验 revision：
// 缺 SystemPrompt 时在构建前返回错误，且不触 Registry。
func TestExecuteSkillScenarioRevisionValidatesRevision(t *testing.T) {
	repo := new(mockAgentRepo)
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		Registry:             agent.NewRegistry(repo, zap.NewNop()),
		TenantModelValidator: lenientModelValidator{},
		Logger:               zap.NewNop(),
	})
	rev := domain.AgentRevision{
		AgentID: "agent-1", Type: domain.ReActAgent,
		Model: "qwen-max", MaxIterations: 5,
	}

	_, _, err := svc.ExecuteSkillScenarioRevision(context.Background(), rev,
		agent.ExecRequest{Query: "hi"}, agent.ExecMeta{TenantID: "t1"},
		[]port.SkillActivation{{SkillID: "s1", RevisionID: "rev-s1"}})

	require.Error(t, err)
	require.Contains(t, err.Error(), "validate")
	repo.AssertNotCalled(t, "Get", mock.Anything, mock.Anything)
}
