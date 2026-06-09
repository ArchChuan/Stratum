package workflow_test

import (
	"context"
	"reflect"
	"testing"
	"unsafe"

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
	comp := workflow.NewTemporalWorkerComponent(cfg, nil, zap.NewNop())
	_, err := comp.ExecuteWorkflow(context.Background(), temporalclient.StartWorkflowOptions{}, nil)
	assert.ErrorContains(t, err, "client not initialized")
}

// mockTemporalClient is a minimal mock for temporalclient.Client.
type mockTemporalClient struct {
	temporalclient.Client // embed to satisfy interface; only ExecuteWorkflow is overridden
	called                bool
	run                   temporalclient.WorkflowRun
	err                   error
}

func (m *mockTemporalClient) ExecuteWorkflow(
	_ context.Context,
	_ temporalclient.StartWorkflowOptions,
	_ interface{},
	_ ...interface{},
) (temporalclient.WorkflowRun, error) {
	m.called = true
	return m.run, m.err
}

// mockWorkflowRun is a stub WorkflowRun for test assertions.
type mockWorkflowRun struct {
	id    string
	runID string
}

func (m *mockWorkflowRun) GetID() string                              { return m.id }
func (m *mockWorkflowRun) GetRunID() string                           { return m.runID }
func (m *mockWorkflowRun) Get(_ context.Context, _ interface{}) error { return nil }
func (m *mockWorkflowRun) GetWithOptions(_ context.Context, _ interface{}, _ temporalclient.WorkflowRunGetOptions) error {
	return nil
}

func TestTemporalWorkerComponent_ExecuteWorkflow_DelegatesToClient(t *testing.T) {
	cfg := &config.TemporalConfig{HostPort: "localhost:7233"}
	comp := workflow.NewTemporalWorkerComponent(cfg, nil, zap.NewNop())

	wantRun := &mockWorkflowRun{id: "wf-1", runID: "run-1"}
	mock := &mockTemporalClient{run: wantRun}

	// Inject mock client via reflect+unsafe since the field is unexported.
	v := reflect.ValueOf(comp).Elem()
	f := v.FieldByName("client")
	ptr := (*temporalclient.Client)(unsafe.Pointer(f.UnsafeAddr())) //nolint:gosec // test-only: inject unexported field via reflect+unsafe
	*ptr = mock

	got, err := comp.ExecuteWorkflow(context.Background(), temporalclient.StartWorkflowOptions{}, "wf-type")
	assert.NoError(t, err)
	assert.True(t, mock.called)
	assert.Equal(t, wantRun, got)
}
