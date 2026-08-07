package application_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/application"
	"github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
)

type fakeAuditRepo struct {
	insertBatchFn func(ctx context.Context, events []domain.AuditEvent) error
	queryFn       func(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error)
	getByIDFn     func(ctx context.Context, tenantID, id string) (*domain.AuditEvent, error)
	deleteFn      func(ctx context.Context, before time.Time) error
}

func (f *fakeAuditRepo) InsertBatch(ctx context.Context, events []domain.AuditEvent) error {
	if f.insertBatchFn != nil {
		return f.insertBatchFn(ctx, events)
	}
	return nil
}

func (f *fakeAuditRepo) Query(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	if f.queryFn != nil {
		return f.queryFn(ctx, filter)
	}
	return nil, nil
}

func (f *fakeAuditRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.AuditEvent, error) {
	if f.getByIDFn != nil {
		return f.getByIDFn(ctx, tenantID, id)
	}
	return nil, nil
}

func (f *fakeAuditRepo) DeleteOlderThan(ctx context.Context, before time.Time) error {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, before)
	}
	return nil
}

func TestNewAuditService_NilMetrics_DoesNotPanic(t *testing.T) {
	repo := &fakeAuditRepo{}
	svc := application.NewAuditService(repo, nil, zap.NewNop())
	defer svc.Stop(context.Background())

	// Nil metrics must not panic on Record.
	err := svc.Record(context.Background(), domain.AuditEvent{
		Action: "POST /test", RiskLevel: "medium",
	})
	if err != nil {
		t.Fatalf("Record with nil metrics: %v", err)
	}
}

func TestAuditService_Record_EnqueuesEvent(t *testing.T) {
	var (
		mu    sync.Mutex
		saved []domain.AuditEvent
		// Signal that InsertBatch was called so Record→flush can be observed.
		flushed = make(chan struct{})
	)
	repo := &fakeAuditRepo{
		insertBatchFn: func(_ context.Context, events []domain.AuditEvent) error {
			mu.Lock()
			saved = append(saved, events...)
			mu.Unlock()
			select {
			case flushed <- struct{}{}:
			default:
			}
			return nil
		},
	}
	svc := application.NewAuditService(repo, observability.NoopMetrics{}, zap.NewNop())
	defer svc.Stop(context.Background())

	// Record enough events to trigger a batch flush.
	for i := 0; i < 101; i++ {
		svc.Record(context.Background(), domain.AuditEvent{
			Action: "POST /test", RiskLevel: "medium",
		})
	}

	// Wait for batch flush (100 events = AuditBatchSize triggers flush).
	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for batch flush")
	}

	mu.Lock()
	count := len(saved)
	mu.Unlock()
	if count < 100 {
		t.Fatalf("expected >=100 events flushed, got %d", count)
	}
}

func TestAuditService_Record_FillsDefaults(t *testing.T) {
	var saved domain.AuditEvent
	repo := &fakeAuditRepo{
		insertBatchFn: func(_ context.Context, events []domain.AuditEvent) error {
			if len(events) > 0 {
				saved = events[0]
			}
			return nil
		},
	}
	svc := application.NewAuditService(repo, observability.NoopMetrics{}, zap.NewNop())
	defer svc.Stop(context.Background())

	svc.Record(context.Background(), domain.AuditEvent{
		Action: "DELETE /admin/tenants/:id",
	})
	// Force flush via Stop.
	svc.Stop(context.Background())

	if saved.ID == "" {
		t.Error("ID not populated")
	}
	if saved.OccurredAt.IsZero() {
		t.Error("OccurredAt not populated")
	}
	if saved.RiskLevel != "low" {
		t.Errorf("default RiskLevel=%q, want low", saved.RiskLevel)
	}
	if saved.Outcome != "success" {
		t.Errorf("default Outcome=%q, want success", saved.Outcome)
	}
}

func TestAuditService_Query_DelegatesToRepo(t *testing.T) {
	want := []domain.AuditEvent{{ID: "evt-1", Action: "POST /test"}}
	repo := &fakeAuditRepo{
		queryFn: func(_ context.Context, f domain.AuditFilter) ([]domain.AuditEvent, error) {
			if f.TenantID != "t1" {
				t.Errorf("filter TenantID=%q, want t1", f.TenantID)
			}
			return want, nil
		},
	}
	svc := application.NewAuditService(repo, observability.NoopMetrics{}, zap.NewNop())
	defer svc.Stop(context.Background())

	events, err := svc.Query(context.Background(), domain.AuditFilter{TenantID: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ID != "evt-1" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestAuditService_GetByID_DelegatesToRepoWithTenant(t *testing.T) {
	want := &domain.AuditEvent{ID: "evt-1"}
	repo := &fakeAuditRepo{
		getByIDFn: func(_ context.Context, tenantID, id string) (*domain.AuditEvent, error) {
			if tenantID != "t1" {
				t.Errorf("tenantID=%q, want t1", tenantID)
			}
			if id != "evt-1" {
				return nil, nil
			}
			return want, nil
		},
	}
	svc := application.NewAuditService(repo, observability.NoopMetrics{}, zap.NewNop())
	defer svc.Stop(context.Background())

	got, err := svc.GetByID(context.Background(), "t1", "evt-1")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != "evt-1" {
		t.Fatal("unexpected result")
	}

	got, err = svc.GetByID(context.Background(), "t1", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil for missing")
	}
}

func TestAuditService_DeleteOlderThan_DelegatesToRepo(t *testing.T) {
	var called time.Time
	repo := &fakeAuditRepo{
		deleteFn: func(_ context.Context, before time.Time) error {
			called = before
			return nil
		},
	}
	svc := application.NewAuditService(repo, observability.NoopMetrics{}, zap.NewNop())
	defer svc.Stop(context.Background())

	cutoff := time.Now().Add(-180 * 24 * time.Hour)
	if err := svc.DeleteOlderThan(context.Background(), cutoff); err != nil {
		t.Fatal(err)
	}
	if !called.Equal(cutoff) {
		t.Errorf("before=%v, want %v", called, cutoff)
	}
}

func TestAuditService_Stop_DoubleCloseSafe(t *testing.T) {
	svc := application.NewAuditService(&fakeAuditRepo{}, observability.NoopMetrics{}, zap.NewNop())
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatal("second Stop should be no-op", err)
	}
}

// TestAuditService_FlushFailure_RetainsEventsUntilRetrySucceeds pins the
// no-event-loss contract: a failed batch insert must keep the events in the
// in-memory batch (retried on the next flush) instead of clearing them.
func TestAuditService_FlushFailure_RetainsEventsUntilRetrySucceeds(t *testing.T) {
	var (
		mu        sync.Mutex
		callCount int
		saved     []domain.AuditEvent
		flushed   = make(chan struct{}, 1)
	)
	repo := &fakeAuditRepo{
		insertBatchFn: func(_ context.Context, events []domain.AuditEvent) error {
			mu.Lock()
			defer mu.Unlock()
			callCount++
			if len(events) < 101 {
				// Simulate a store outage: partial batches are rejected.
				// The writer must retain them and retry until the whole set
				// is deliverable in one flush.
				return errors.New("db unavailable")
			}
			saved = append(saved, events...)
			select {
			case flushed <- struct{}{}:
			default:
			}
			return nil
		},
	}
	svc := application.NewAuditService(repo, observability.NoopMetrics{}, zap.NewNop())
	defer svc.Stop(context.Background())

	for i := 0; i < 101; i++ {
		svc.Record(context.Background(), domain.AuditEvent{
			Action: "POST /test", RiskLevel: "medium",
		})
	}

	select {
	case <-flushed:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for successful retry flush")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 101 {
		t.Fatalf("expected all 101 events persisted after retry, got %d", len(saved))
	}
	if callCount < 2 {
		t.Fatalf("expected at least one failed attempt before success, got %d calls", callCount)
	}
}

// TestAuditService_Stop_PropagatesPersistError pins that a failing store
// surfaces through Stop instead of silently dropping the pending events.
func TestAuditService_Stop_PropagatesPersistError(t *testing.T) {
	repo := &fakeAuditRepo{
		insertBatchFn: func(_ context.Context, _ []domain.AuditEvent) error {
			return errors.New("db down")
		},
	}
	svc := application.NewAuditService(repo, observability.NoopMetrics{}, zap.NewNop())

	svc.Record(context.Background(), domain.AuditEvent{Action: "POST /test"})
	if err := svc.Stop(context.Background()); err == nil {
		t.Fatal("expected Stop to propagate the persist error, got nil")
	}
}
