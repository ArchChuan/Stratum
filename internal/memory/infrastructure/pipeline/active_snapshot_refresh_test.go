package pipeline

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"go.uber.org/zap"
)

const snapshotTestTenant = "42c9b62d-4f66-4bc4-a1b8-eed81cdae7b1"

type stubActiveSnapshotRepo struct {
	got *domain.ActiveSnapshot
	err error
}

func (r *stubActiveSnapshotRepo) Upsert(_ context.Context, snapshot *domain.ActiveSnapshot) error {
	r.got = snapshot
	return r.err
}
func (r *stubActiveSnapshotRepo) Get(context.Context, string, string, string) (*domain.ActiveSnapshot, error) {
	return nil, r.err
}
func (r *stubActiveSnapshotRepo) Delete(context.Context, string, string, string) error { return r.err }

func TestEnricherRefreshesBoundedActiveSnapshotFromExistingAsyncEvent(t *testing.T) {
	repo := &stubActiveSnapshotRepo{}
	w := &EnricherWorker{snapshotRepo: repo}
	now := time.Now().UTC()
	ev := &MemoryEnrichedEvent{MemoryRawEvent: MemoryRawEvent{TenantID: snapshotTestTenant, UserID: "user-1", AgentID: "agent-1", MessageID: "msg-1", CreatedAt: now}}
	result := &EnrichmentResult{WorkContext: []string{"implement phase one"}, PersonalContext: []string{"prefers short reports"}, TopOfMind: []string{"tenant safety"}}

	if err := w.refreshActiveSnapshot(context.Background(), ev, result); err != nil {
		t.Fatal(err)
	}
	if repo.got == nil || repo.got.Source.Reference != "msg-1" || repo.got.Source.Type != "message" {
		t.Fatalf("missing minimal source reference: %#v", repo.got)
	}
	if !repo.got.ExpiresAt.After(repo.got.UpdatedAt) || repo.got.Status != domain.SnapshotStatusActive {
		t.Fatalf("missing active TTL metadata: %#v", repo.got)
	}
}

func TestEnricherSnapshotFailureIsBestEffort(t *testing.T) {
	want := errors.New("snapshot unavailable")
	w := &EnricherWorker{logger: zap.NewNop(), snapshotRepo: &stubActiveSnapshotRepo{err: want}}
	ev := &MemoryEnrichedEvent{MemoryRawEvent: MemoryRawEvent{TenantID: snapshotTestTenant, UserID: "user-1", AgentID: "agent-1", MessageID: "msg-1", CreatedAt: time.Now().UTC()}}
	err := w.refreshActiveSnapshot(context.Background(), ev, &EnrichmentResult{TopOfMind: []string{"task"}})
	if err != nil {
		t.Fatalf("derived snapshot failure must not fail core enrichment: %v", err)
	}
}

func TestEnricherSkipsActiveSnapshotWithZeroEventTime(t *testing.T) {
	repo := &stubActiveSnapshotRepo{}
	w := &EnricherWorker{logger: zap.NewNop(), snapshotRepo: repo}
	ev := &MemoryEnrichedEvent{MemoryRawEvent: MemoryRawEvent{TenantID: snapshotTestTenant, UserID: "user-1", AgentID: "agent-1", MessageID: "msg-1"}}

	if err := w.refreshActiveSnapshot(context.Background(), ev, &EnrichmentResult{TopOfMind: []string{"task"}}); err != nil {
		t.Fatal(err)
	}
	if repo.got != nil {
		t.Fatalf("zero-time event must not produce a non-deterministic snapshot: %#v", repo.got)
	}
}

func TestEnricherSkipsEmptyActiveSnapshot(t *testing.T) {
	repo := &stubActiveSnapshotRepo{}
	w := &EnricherWorker{snapshotRepo: repo}
	if err := w.refreshActiveSnapshot(context.Background(), &MemoryEnrichedEvent{}, &EnrichmentResult{}); err != nil || repo.got != nil {
		t.Fatalf("empty snapshot should be ignored: got=%#v err=%v", repo.got, err)
	}
}
