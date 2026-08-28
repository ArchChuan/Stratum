package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

type stubEvidenceReader struct {
	trace port.ObservedTrace
	err   error
}

func (s *stubEvidenceReader) Resolve(ctx context.Context, tenantID, traceID string) (port.ObservedTrace, error) {
	if s.err != nil {
		return port.ObservedTrace{}, s.err
	}
	return s.trace, nil
}

func (s *stubEvidenceReader) ResolveBatch(ctx context.Context, tenantID string, traceIDs []string) (map[string]port.ObservedTrace, error) {
	return nil, errors.New("not used")
}

type stubJudge struct {
	result  domain.AssertionResult
	err     error
	enabled bool
	calls   int
}

func (j *stubJudge) Enabled(ctx context.Context) bool { return j.enabled }
func (j *stubJudge) Judge(ctx context.Context, req port.JudgeRequest) (domain.AssertionResult, error) {
	j.calls++
	if j.err != nil {
		return domain.AssertionResult{}, j.err
	}
	return j.result, nil
}

type stubObservationRepo struct {
	saved []domain.EvalObservation
	err   error
}

func (s *stubObservationRepo) Save(ctx context.Context, tenantID string, obs *domain.EvalObservation) error {
	if s.err != nil {
		return s.err
	}
	s.saved = append(s.saved, *obs)
	return nil
}
func (s *stubObservationRepo) Get(ctx context.Context, tenantID, id string) (*domain.EvalObservation, error) {
	return nil, errors.New("not used")
}
func (s *stubObservationRepo) QueryByResource(ctx context.Context, tenantID, resourceKind, resourceID string,
	from, to *time.Time, limit, offset int,
) ([]domain.EvalObservation, error) {
	return nil, errors.New("not used")
}

func newTestObservationService(repo *stubObservationRepo, reader *stubEvidenceReader, judge *stubJudge) *ObservationService {
	return NewObservationService(ObservationServiceDeps{
		Enabled:    func(context.Context) bool { return true },
		SampleRate: func(context.Context) float64 { return 1.0 },
		Evidence:   reader, Judge: judge, Repo: repo,
		Metrics: observability.NoopMetrics{}, Logger: zap.NewNop(),
	})
}

func TestObservationServiceProcessJudgesAndSaves(t *testing.T) {
	repo := &stubObservationRepo{}
	reader := &stubEvidenceReader{trace: port.ObservedTrace{
		TraceID: "trace-1", Input: "用户问题", Output: "助手回答",
		CostUSD: 0.01, LatencyMs: 800, Success: true,
	}}
	judge := &stubJudge{enabled: true, result: domain.AssertionResult{Passed: true}}
	svc := newTestObservationService(repo, reader, judge)

	evt := domain.ObservationReferenceEvent{
		TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1",
		AgentID: "agent-1", ResourceKind: "agent", ResourceID: "agent-1",
	}
	if err := svc.Process(context.Background(), evt); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("saved %d observations, want 1", len(repo.saved))
	}
	saved := repo.saved[0]
	if saved.TraceID != "trace-1" || saved.Resource.Kind != "agent" || saved.Resource.ResourceID != "agent-1" {
		t.Fatalf("saved identity mismatch: %+v", saved)
	}
	if len(saved.Signals.Judge) != 3 {
		t.Fatalf("judge signals = %d, want 3 dimensions", len(saved.Signals.Judge))
	}
	if saved.Verdict != domain.VerdictPass {
		t.Fatalf("verdict = %s, want pass", saved.Verdict)
	}
	if judge.calls != 3 {
		t.Fatalf("judge calls = %d, want 3", judge.calls)
	}
}

func TestObservationServiceProcessDisabledSkips(t *testing.T) {
	repo := &stubObservationRepo{}
	svc := NewObservationService(ObservationServiceDeps{
		Enabled:  func(context.Context) bool { return false },
		Evidence: &stubEvidenceReader{}, Judge: &stubJudge{},
		Repo: repo, Metrics: observability.NoopMetrics{}, Logger: zap.NewNop(),
	})
	if err := svc.Process(context.Background(), domain.ObservationReferenceEvent{TraceID: "trace-1"}); err != nil {
		t.Fatalf("Process disabled: %v", err)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("disabled must not save, got %d", len(repo.saved))
	}
}

func TestObservationServiceProcessEvidenceErrorPropagates(t *testing.T) {
	repo := &stubObservationRepo{}
	reader := &stubEvidenceReader{err: errors.New("opik down")}
	svc := newTestObservationService(repo, reader, &stubJudge{enabled: true})
	err := svc.Process(context.Background(), domain.ObservationReferenceEvent{TraceID: "trace-1", ResourceKind: "agent", ResourceID: "a1"})
	if err == nil {
		t.Fatal("evidence error must propagate for NATS redelivery")
	}
	if len(repo.saved) != 0 {
		t.Fatalf("must not save on evidence error, got %d", len(repo.saved))
	}
}

func TestObservationServiceProcessJudgeFailureDegrades(t *testing.T) {
	repo := &stubObservationRepo{}
	reader := &stubEvidenceReader{trace: port.ObservedTrace{TraceID: "trace-1", Input: "q", Output: "a"}}
	judge := &stubJudge{enabled: true, err: errors.New("judge down")}
	svc := newTestObservationService(repo, reader, judge)
	if err := svc.Process(context.Background(), domain.ObservationReferenceEvent{TraceID: "trace-1", ResourceKind: "agent", ResourceID: "a1"}); err != nil {
		t.Fatalf("Process with judge failure should degrade without error: %v", err)
	}
	// judge 故障 §14 采样降级跳过：不落库、不重投、不伪造零信号 pass 观察。
	if len(repo.saved) != 0 {
		t.Fatalf("judge failure must skip observation, got %d saved", len(repo.saved))
	}
}

func TestObservationServiceProcessJudgeDisabledSkips(t *testing.T) {
	repo := &stubObservationRepo{}
	reader := &stubEvidenceReader{trace: port.ObservedTrace{TraceID: "trace-1", Input: "q", Output: "a"}}
	judge := &stubJudge{enabled: false} // judge 关闭（配置态）
	svc := newTestObservationService(repo, reader, judge)
	if err := svc.Process(context.Background(), domain.ObservationReferenceEvent{
		TraceID: "trace-1", ResourceKind: "agent", ResourceID: "a1",
	}); err != nil {
		t.Fatalf("Process with judge disabled should skip without error: %v", err)
	}
	// judge 关闭时跳过本次观测：不落零信号 pass 观测（§14 精神），非故障降级。
	if len(repo.saved) != 0 {
		t.Fatalf("judge disabled must not save zero-signal observation, got %d", len(repo.saved))
	}
}

func TestObservationServiceProcessInvalidObservationDrops(t *testing.T) {
	repo := &stubObservationRepo{}
	reader := &stubEvidenceReader{trace: port.ObservedTrace{TraceID: "trace-1", Input: "q", Output: "a"}}
	judge := &stubJudge{enabled: true, result: domain.AssertionResult{Passed: true}}
	svc := newTestObservationService(repo, reader, judge)
	// ResourceID 为空 → buildObservation 后 obs.Validate 触发「resource id required」。
	evt := domain.ObservationReferenceEvent{
		TraceID: "trace-1", ResourceKind: "agent", ResourceID: "",
	}
	if err := svc.Process(context.Background(), evt); err != nil {
		t.Fatalf("invalid observation must drop without error (no redelivery): %v", err)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("invalid observation must not save, got %d", len(repo.saved))
	}
}

func TestObservationServiceProcessSaveFailureDrops(t *testing.T) {
	repo := &stubObservationRepo{err: errors.New("repo down")} // Save 失败
	reader := &stubEvidenceReader{trace: port.ObservedTrace{TraceID: "trace-1", Input: "q", Output: "a"}}
	judge := &stubJudge{enabled: true, result: domain.AssertionResult{Passed: true}}
	svc := newTestObservationService(repo, reader, judge)
	if err := svc.Process(context.Background(), domain.ObservationReferenceEvent{
		TraceID: "trace-1", ResourceKind: "agent", ResourceID: "a1",
	}); err != nil {
		t.Fatalf("save failure must drop without error (no redelivery): %v", err)
	}
	if len(repo.saved) != 0 {
		t.Fatalf("save failure must not save, got %d", len(repo.saved))
	}
}
