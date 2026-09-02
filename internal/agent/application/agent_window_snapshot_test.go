package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// TestResolveExecutionWindowFromSnapshot 验证评测执行（执行快照在 ctx）时
// 执行窗口与输出预留直接取固化值，跳过运行时来源链。
func TestResolveExecutionWindowFromSnapshot(t *testing.T) {
	es := &port.ExecutionSnapshot{ContextWindowTokens: 12345, OutputReserveTokens: 4096}
	ctx := port.WithExecutionSnapshot(context.Background(), es)
	s := &AgentService{deps: AgentServiceDeps{}}
	window, src := s.resolveExecutionWindow(ctx, "", "any-model", 0)
	require.Equal(t, 12345, window)
	require.Equal(t, agentgraph.WindowSnapshot, src)
	reserve := s.resolveOutputReserve(ctx, "", "any-model", 0)
	require.Equal(t, 4096, reserve)
}
