package harness

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

type mockComponent struct {
	name      string
	startErr  error
	stopErr   error
	healthErr error
	started   bool
	stopped   bool
}

func (m *mockComponent) Name() string {
	return m.name
}

func (m *mockComponent) Start(ctx context.Context) error {
	if m.startErr != nil {
		return m.startErr
	}
	m.started = true
	return nil
}

func (m *mockComponent) Stop(ctx context.Context) error {
	if m.stopErr != nil {
		return m.stopErr
	}
	m.stopped = true
	return nil
}

func (m *mockComponent) HealthCheck(ctx context.Context) error {
	return m.healthErr
}

func TestNew(t *testing.T) {
	logger := zap.NewNop()
	harness := New(logger)

	if harness == nil {
		t.Error("expected Harness to be non-nil")
	}
}

func TestHarnessRegister(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test-comp"}

	err := h.Register(comp)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if len(h.components) != 1 {
		t.Errorf("expected 1 component, got %d", len(h.components))
	}
}

func TestHarnessRegisterDuplicate(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp1 := &mockComponent{name: "test"}
	comp2 := &mockComponent{name: "test"}

	_ = h.Register(comp1)
	err := h.Register(comp2)

	if err == nil {
		t.Error("expected error for duplicate component")
	}
}

func TestHarnessRegisterAfterStart(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp1 := &mockComponent{name: "comp1"}
	_ = h.Register(comp1)

	ctx := context.Background()
	_ = h.Start(ctx)

	comp2 := &mockComponent{name: "comp2"}
	err := h.Register(comp2)

	if err == nil {
		t.Error("expected error when registering after start")
	}
}

func TestHarnessStart(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test"}
	_ = h.Register(comp)

	ctx := context.Background()
	err := h.Start(ctx)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !h.started {
		t.Error("expected harness to be started")
	}

	if !comp.started {
		t.Error("expected component to be started")
	}
}

func TestHarnessStartAlreadyStarted(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test"}
	_ = h.Register(comp)

	ctx := context.Background()
	_ = h.Start(ctx)
	err := h.Start(ctx)

	if err == nil {
		t.Error("expected error when starting already started harness")
	}
}

func TestHarnessStartComponentError(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test", startErr: errors.New("start failed")}
	_ = h.Register(comp)

	ctx := context.Background()
	err := h.Start(ctx)

	if err == nil {
		t.Error("expected error from component start")
	}

	if h.started {
		t.Error("expected harness not to be started after component error")
	}
}

func TestHarnessStop(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test"}
	_ = h.Register(comp)

	ctx := context.Background()
	_ = h.Start(ctx)
	err := h.Stop(ctx)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if h.started {
		t.Error("expected harness to be stopped")
	}

	if !comp.stopped {
		t.Error("expected component to be stopped")
	}
}

func TestHarnessStopNotStarted(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)

	ctx := context.Background()
	err := h.Stop(ctx)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestHarnessHealthCheck(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp1 := &mockComponent{name: "comp1", healthErr: nil}
	comp2 := &mockComponent{name: "comp2", healthErr: errors.New("unhealthy")}
	_ = h.Register(comp1)
	_ = h.Register(comp2)

	ctx := context.Background()
	results := h.HealthCheck(ctx)

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}

	if results["comp1"] != nil {
		t.Error("expected comp1 to be healthy")
	}

	if results["comp2"] == nil {
		t.Error("expected comp2 to be unhealthy")
	}
}

func TestHarnessGetComponent(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test"}
	_ = h.Register(comp)

	retrieved, exists := h.GetComponent("test")

	if !exists {
		t.Error("expected component to exist")
	}

	if retrieved != comp {
		t.Error("expected to retrieve same component")
	}
}

func TestHarnessGetComponentNotFound(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)

	_, exists := h.GetComponent("nonexistent")

	if exists {
		t.Error("expected component not to exist")
	}
}

func TestHarnessMultipleComponents(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp1 := &mockComponent{name: "comp1"}
	comp2 := &mockComponent{name: "comp2"}
	comp3 := &mockComponent{name: "comp3"}

	_ = h.Register(comp1)
	_ = h.Register(comp2)
	_ = h.Register(comp3)

	ctx := context.Background()
	err := h.Start(ctx)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !comp1.started || !comp2.started || !comp3.started {
		t.Error("expected all components to be started")
	}

	_ = h.Stop(ctx)

	if !comp1.stopped || !comp2.stopped || !comp3.stopped {
		t.Error("expected all components to be stopped")
	}
}

func TestHarnessRun(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test"}
	_ = h.Register(comp)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := h.Run(ctx)

	if err != context.DeadlineExceeded {
		t.Errorf("expected context deadline exceeded, got %v", err)
	}

	if !comp.started {
		t.Error("expected component to be started")
	}

	if !comp.stopped {
		t.Error("expected component to be stopped")
	}
}

func TestHarnessRunStartError(t *testing.T) {
	logger := zap.NewNop()
	h := New(logger)
	comp := &mockComponent{name: "test", startErr: errors.New("start failed")}
	_ = h.Register(comp)

	ctx := context.Background()
	err := h.Run(ctx)

	if err == nil {
		t.Error("expected error from component start")
	}
}
