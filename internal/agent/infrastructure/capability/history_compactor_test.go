package capgateway

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/stretchr/testify/require"
)

type deadlineRecordingGateway struct {
	deadline time.Time
}

func (g *deadlineRecordingGateway) Route(ctx context.Context, _ port.CapabilityRequest) (port.CapabilityResponse, error) {
	g.deadline, _ = ctx.Deadline()
	return port.CapabilityResponse{Content: "summary"}, nil
}

func TestLLMHistoryCompactor_UsesIndependentShortDeadline(t *testing.T) {
	gw := &deadlineRecordingGateway{}
	compactor := NewLLMHistoryCompactor(gw, "qwen", nil)
	parent, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := compactor.CompactHistory(parent, []port.LLMMessage{{Role: "user", Content: "history"}})
	require.NoError(t, err)
	require.False(t, gw.deadline.IsZero())
	require.WithinDuration(t, time.Now().Add(historyCompactionTimeout), gw.deadline, time.Second)
}
