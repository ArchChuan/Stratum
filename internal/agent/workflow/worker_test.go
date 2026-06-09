package workflow_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/workflow"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewTemporalWorkerComponent_Name(t *testing.T) {
	cfg := &config.TemporalConfig{
		HostPort:  "localhost:7233",
		Namespace: "default",
		TaskQueue: "test",
	}
	comp := workflow.NewTemporalWorkerComponent(cfg, nil, zap.NewNop())
	require.Equal(t, "temporal-worker", comp.Name())
}

func TestTemporalWorkerComponent_StopWithoutStart(t *testing.T) {
	cfg := &config.TemporalConfig{HostPort: "localhost:7233", Namespace: "default", TaskQueue: "test"}
	comp := workflow.NewTemporalWorkerComponent(cfg, nil, zap.NewNop())
	require.NoError(t, comp.Stop(context.Background()))
}
