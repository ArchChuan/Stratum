package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestAtomicExecute_Success(t *testing.T) {
	p := newMockProvider("skill:test", "llm", "hello", nil)
	ae := newTestEngine(p)

	resp, err := ae.execute(context.Background(), SkillRequest{SkillID: "skill:test", Input: "input"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "hello" {
		t.Errorf("expected output 'hello', got %v", resp.Output)
	}
	if resp.TraceID == "" {
		t.Error("expected non-empty trace_id")
	}
}

func TestAtomicExecute_TraceIDPropagation(t *testing.T) {
	p := newMockProvider("skill:test", "llm", "ok", nil)
	ae := newTestEngine(p)

	req := SkillRequest{SkillID: "skill:test", TraceID: "my-trace-123"}
	resp, err := ae.execute(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.TraceID != "my-trace-123" {
		t.Errorf("expected trace_id 'my-trace-123', got %s", resp.TraceID)
	}
}

func TestAtomicExecute_UsesVersionProviderWhenVersionIDPresent(t *testing.T) {
	p := newMockProvider("skill:test", "db", "version-output", nil)
	ae := newTestEngine(p)

	resp, err := ae.execute(context.Background(), SkillRequest{
		SkillID:   "skill:test",
		VersionID: "version-1",
		Input:     map[string]any{"prompt": "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "version-output" {
		t.Fatalf("expected version output, got %#v", resp.Output)
	}
	if p.versionCallCnt != 1 {
		t.Fatalf("expected ExecuteVersion to be called once, got %d", p.versionCallCnt)
	}
	if p.versionID != "version-1" {
		t.Fatalf("expected version-1, got %q", p.versionID)
	}
}

func TestAtomicExecute_SkillNotFound(t *testing.T) {
	p := newMockProvider("skill:test", "llm", nil, nil)
	ae := newTestEngine(p)

	_, err := ae.execute(context.Background(), SkillRequest{SkillID: "skill:nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	var skillErr *SkillError
	if !errors.As(err, &skillErr) {
		t.Fatalf("expected *SkillError, got %T", err)
	}
	if skillErr.Code != ErrSkillNotFound {
		t.Errorf("expected ErrSkillNotFound, got %d", skillErr.Code)
	}
}

func TestAtomicExecute_Timeout(t *testing.T) {
	slow := &slowProvider{skillID: "skill:slow", delay: 200 * time.Millisecond}
	reg := newProviderRegistry()
	_ = reg.Register(slow)
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())

	_, err := ae.execute(context.Background(), SkillRequest{
		SkillID: "skill:slow",
		Timeout: 50 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var skillErr *SkillError
	if !errors.As(err, &skillErr) {
		t.Fatalf("expected *SkillError, got %T", err)
	}
	if skillErr.Code != ErrSkillTimeout {
		t.Errorf("expected ErrSkillTimeout, got %d", skillErr.Code)
	}
}

func TestAtomicExecute_Retry(t *testing.T) {
	failThenSucceed := &countingProvider{skillID: "skill:retry", failUntil: 2, output: "done"}
	reg := newProviderRegistry()
	_ = reg.Register(failThenSucceed)
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	ae := newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())

	resp, err := ae.execute(context.Background(), SkillRequest{
		SkillID: "skill:retry",
		Retry:   RetryConfig{MaxAttempts: 3, BaseDelay: 1 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	if resp.Output != "done" {
		t.Errorf("expected 'done', got %v", resp.Output)
	}
	if failThenSucceed.callCount != 3 {
		t.Errorf("expected 3 calls (2 fail + 1 success), got %d", failThenSucceed.callCount)
	}
}

func TestAtomicExecute_AllErrors_IsSkillError(t *testing.T) {
	p := newMockProvider("skill:fail", "llm", nil, errors.New("boom"))
	ae := newTestEngine(p)

	_, err := ae.execute(context.Background(), SkillRequest{SkillID: "skill:fail"})
	var skillErr *SkillError
	if !errors.As(err, &skillErr) {
		t.Fatalf("expected *SkillError, got %T", err)
	}
	if skillErr.TraceID == "" {
		t.Error("expected non-empty trace_id in error")
	}
}

// slowProvider 模拟慢速 skill
type slowProvider struct {
	skillID string
	delay   time.Duration
}

func (p *slowProvider) SkillIDs() []string { return []string{p.skillID} }
func (p *slowProvider) Has(id string) bool { return id == p.skillID }
func (p *slowProvider) SkillType() string  { return "test" }
func (p *slowProvider) Execute(ctx context.Context, _ string, _ any) (any, error) {
	select {
	case <-time.After(p.delay):
		return "ok", nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// countingProvider 前 N 次失败，之后成功
type countingProvider struct {
	skillID   string
	failUntil int
	output    any
	callCount int
}

func (p *countingProvider) SkillIDs() []string { return []string{p.skillID} }
func (p *countingProvider) Has(id string) bool { return id == p.skillID }
func (p *countingProvider) SkillType() string  { return "test" }
func (p *countingProvider) Execute(_ context.Context, _ string, _ any) (any, error) {
	p.callCount++
	if p.callCount <= p.failUntil {
		return nil, errors.New("transient error")
	}
	return p.output, nil
}
