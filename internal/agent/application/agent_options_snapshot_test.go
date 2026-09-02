package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// TestCaptureParametersOptionFromSnapshot 验证评测执行（执行快照在 ctx）时
// trace.capture_parameters 从快照 TraceParameters 读取，不触碰 provider
// （provider 为 nil 也不 panic），且命中快照后返回非 nil option。
func TestCaptureParametersOptionFromSnapshot(t *testing.T) {
	es := &port.ExecutionSnapshot{
		TraceParameters: map[string]any{"trace.capture_parameters": true},
	}
	ctx := port.WithExecutionSnapshot(context.Background(), es)
	// provider nil + 快照在 ctx → 从快照读（不 panic）
	opt := captureParametersOption(ctx, nil)
	require.NotNil(t, opt)
	require.True(t, applyOptions([]ExecutionOption{opt}).CaptureParameters)
}
