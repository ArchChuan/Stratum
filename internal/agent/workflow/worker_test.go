package workflow_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/agent/workflow"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"
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

func TestTemporalWorkerComponent_ExecuteWorkflow_NilClient(t *testing.T) {
	cfg := &config.TemporalConfig{HostPort: "localhost:7233"}
	comp := workflow.NewTemporalWorkerComponent(cfg, nil, nil)
	_, err := comp.ExecuteWorkflow(context.Background(), temporalclient.StartWorkflowOptions{}, nil)
	assert.ErrorContains(t, err, "client not initialized")
}
