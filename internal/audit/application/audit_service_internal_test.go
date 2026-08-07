package application

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// alwaysFailAuditRepo keeps failing inserts so the retained batch would grow
// without bound; the test observes what actually reaches InsertBatch.
type alwaysFailAuditRepo struct {
	inserted chan []domain.AuditEvent
}

func (r *alwaysFailAuditRepo) InsertBatch(_ context.Context, events []domain.AuditEvent) error {
	r.inserted <- events
	return errors.New("db unavailable")
}

func (r *alwaysFailAuditRepo) Query(context.Context, domain.AuditFilter) ([]domain.AuditEvent, error) {
	return nil, nil
}

func (r *alwaysFailAuditRepo) GetByID(context.Context, string, string) (*domain.AuditEvent, error) {
	return nil, nil
}

func (r *alwaysFailAuditRepo) DeleteOlderThan(context.Context, time.Time) error {
	return nil
}

// TestFlush_OverflowDropsOldestBoundsMemory pins the bounded-memory contract:
// with the store down, the retained batch is capped at MaxAuditBatchPending —
// the oldest events are dropped with an ERROR log + overflow metric, the newest
// survive for the next retry, and the「成功才清空」contract is unchanged (flush
// still returns the persist error).
func TestFlush_OverflowDropsOldestBoundsMemory(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	repo := &alwaysFailAuditRepo{inserted: make(chan []domain.AuditEvent, 1)}
	svc := NewAuditService(repo, observability.NoopMetrics{}, zap.New(core))
	defer svc.Stop(context.Background())

	const extra = 50
	batch := make([]domain.AuditEvent, 0, constants.MaxAuditBatchPending+extra)
	for i := 0; i < constants.MaxAuditBatchPending+extra; i++ {
		batch = append(batch, domain.AuditEvent{ID: fmt.Sprintf("evt-%d", i), Action: "POST /test"})
	}

	if err := svc.flush(&batch); err == nil {
		t.Fatal("flush with failing repo must return the persist error (retain-for-retry contract)")
	}

	// 保留最新的 cap 条，丢弃最旧 extra 条。
	if len(batch) != constants.MaxAuditBatchPending {
		t.Fatalf("retained batch len: got %d, want %d", len(batch), constants.MaxAuditBatchPending)
	}
	if batch[0].ID != "evt-50" {
		t.Fatalf("oldest retained event: got %q, want %q (oldest 50 must be dropped)", batch[0].ID, "evt-50")
	}

	select {
	case inserted := <-repo.inserted:
		if len(inserted) != constants.MaxAuditBatchPending {
			t.Fatalf("InsertBatch received %d events, want %d", len(inserted), constants.MaxAuditBatchPending)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for InsertBatch")
	}

	// 可观测性：ERROR 日志含丢弃/保留数量。
	found := false
	for _, entry := range logs.All() {
		if entry.Message != "audit: batch overflow, dropping oldest events" {
			continue
		}
		found = true
		if got := entry.ContextMap()["dropped"]; got != int64(extra) {
			t.Errorf("dropped field: got %v, want %d", got, extra)
		}
		if got := entry.ContextMap()["retained"]; got != int64(constants.MaxAuditBatchPending) {
			t.Errorf("retained field: got %v, want %d", got, constants.MaxAuditBatchPending)
		}
	}
	if !found {
		t.Fatal("expected overflow ERROR log entry")
	}
}

// TestFlush_UnderCapUntouched pins that batches within the cap are passed
// through as-is: no drop, no overflow log.
func TestFlush_UnderCapUntouched(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	repo := &alwaysFailAuditRepo{inserted: make(chan []domain.AuditEvent, 1)}
	svc := NewAuditService(repo, observability.NoopMetrics{}, zap.New(core))
	defer svc.Stop(context.Background())

	batch := make([]domain.AuditEvent, 0, 50)
	for i := 0; i < 50; i++ {
		batch = append(batch, domain.AuditEvent{ID: fmt.Sprintf("evt-%d", i)})
	}
	if err := svc.flush(&batch); err == nil {
		t.Fatal("flush with failing repo must return the persist error")
	}
	if len(batch) != 50 {
		t.Fatalf("retained batch len: got %d, want 50 (no drop under cap)", len(batch))
	}
	for _, entry := range logs.All() {
		if entry.Message == "audit: batch overflow, dropping oldest events" {
			t.Fatal("overflow log must not fire for batches under the cap")
		}
	}
}
