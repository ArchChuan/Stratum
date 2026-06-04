package skillgateway

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

func newTestPipelineEngine() *pipelineEngine { //nolint:unused
	reg := newProviderRegistry()
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())
	return newPipelineEngine(ae, zap.NewNop())
}

func registerMock(reg *ProviderRegistry, id, typ string, output any, err error) {
	_ = reg.Register(newMockProvider(id, typ, output, err))
}

func TestPipeline_Sequential(t *testing.T) {
	reg := newProviderRegistry()
	registerMock(reg, "skill:a", "test", "result-a", nil)
	registerMock(reg, "skill:b", "test", "result-b", nil)
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())
	pe := newPipelineEngine(ae, zap.NewNop())

	p := NewPipelineBuilder("test-pipeline").
		Step("step1", "skill:a", "input").
		Step("step2", "skill:b", "input").
		Build()

	result, err := pe.execute(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(result.Steps))
	}
	if result.Steps[0].Output != "result-a" {
		t.Errorf("step1 output: expected 'result-a', got %v", result.Steps[0].Output)
	}
}

func TestPipeline_ContextPropagation(t *testing.T) {
	reg := newProviderRegistry()
	registerMock(reg, "skill:a", "test", "value-from-a", nil)

	var capturedInput any
	capture := &captureProvider{skillID: "skill:b", output: "ok"}
	_ = reg.Register(capture)
	_ = reg.Register(newMockProvider("skill:a", "test", "value-from-a", nil))

	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())
	pe := newPipelineEngine(ae, zap.NewNop())

	p := NewPipelineBuilder("ctx-test").
		Step("step1", "skill:a", "input").
		Step("step2", "skill:b", "$prev.output").
		Build()

	_, err := pe.execute(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	capturedInput = capture.lastInput
	if capturedInput != "value-from-a" {
		t.Errorf("expected step2 input to be 'value-from-a', got %v", capturedInput)
	}
}

func TestPipeline_Conditional_ThenBranch(t *testing.T) {
	reg := newProviderRegistry()
	registerMock(reg, "skill:then", "test", "then-result", nil)
	registerMock(reg, "skill:else", "test", "else-result", nil)
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())
	pe := newPipelineEngine(ae, zap.NewNop())

	p := NewPipelineBuilder("cond-test").
		If("branch", func(ctx StepContext) bool { return true }).
		Then(NewPipelineBuilder("").Step("t", "skill:then", nil)).
		Else(NewPipelineBuilder("").Step("e", "skill:else", nil)).
		Build()

	result, err := pe.execute(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Steps[0].Output != "then-result" {
		t.Errorf("expected 'then-result', got %v", result.Steps[0].Output)
	}
}

func TestPipeline_Parallel(t *testing.T) {
	reg := newProviderRegistry()
	registerMock(reg, "skill:p1", "test", "p1", nil)
	registerMock(reg, "skill:p2", "test", "p2", nil)
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())
	pe := newPipelineEngine(ae, zap.NewNop())

	p := NewPipelineBuilder("parallel-test").
		Parallel("par",
			NewPipelineBuilder("").Step("p1", "skill:p1", nil),
			NewPipelineBuilder("").Step("p2", "skill:p2", nil),
		).
		Build()

	result, err := pe.execute(context.Background(), p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outputs, ok := result.Steps[0].Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output from parallel step, got %T", result.Steps[0].Output)
	}
	if outputs["p1"] != "p1" || outputs["p2"] != "p2" {
		t.Errorf("unexpected parallel outputs: %v", outputs)
	}
}

func TestPipeline_StepFailure_ReturnsPipelineError(t *testing.T) {
	reg := newProviderRegistry()
	registerMock(reg, "skill:fail", "test", nil, errors.New("step failed"))
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())
	pe := newPipelineEngine(ae, zap.NewNop())

	p := NewPipelineBuilder("fail-test").
		Step("bad-step", "skill:fail", nil).
		Build()

	_, err := pe.execute(context.Background(), p)
	if err == nil {
		t.Fatal("expected error")
	}
	var pipelineErr *PipelineError
	if !errors.As(err, &pipelineErr) {
		t.Fatalf("expected *PipelineError, got %T", err)
	}
	if pipelineErr.FailedStep != "bad-step" {
		t.Errorf("expected FailedStep='bad-step', got %s", pipelineErr.FailedStep)
	}
}

// captureProvider 记录最后一次 Execute 的 input
type captureProvider struct {
	skillID   string
	output    any
	lastInput any
}

func (p *captureProvider) SkillIDs() []string { return []string{p.skillID} }
func (p *captureProvider) Has(id string) bool { return id == p.skillID }
func (p *captureProvider) SkillType() string  { return "test" }
func (p *captureProvider) Execute(_ context.Context, _ string, input any) (any, error) {
	p.lastInput = input
	return p.output, nil
}
