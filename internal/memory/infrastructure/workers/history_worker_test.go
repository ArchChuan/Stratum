package workers

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

type fakeHistoryRepo struct {
	batches                                               []*domain.HistoryBatch
	batchErrs                                             []error
	overflow                                              *domain.HistoryOverflowGroup
	overflowErr, upsertErr, replaceErr                    error
	nextCalls, upserts, lifecycle, archives, replacements int
	replacement                                           *domain.HistorySegment
	upserted                                              *domain.HistorySegment
	replacedIDs                                           []string
}

func (f *fakeHistoryRepo) NextBatch(context.Context, string, int, int) (*domain.HistoryBatch, error) {
	i := f.nextCalls
	f.nextCalls++
	var batch *domain.HistoryBatch
	if i < len(f.batches) {
		batch = f.batches[i]
	}
	if i < len(f.batchErrs) {
		return batch, f.batchErrs[i]
	}
	return batch, nil
}
func (f *fakeHistoryRepo) Upsert(_ context.Context, _ string, h *domain.HistorySegment) error {
	f.upserts++
	f.upserted = h
	return f.upsertErr
}
func (f *fakeHistoryRepo) NextOverflow(context.Context, string, int, int, int) (*domain.HistoryOverflowGroup, error) {
	return f.overflow, f.overflowErr
}
func (f *fakeHistoryRepo) ReplaceOverflow(_ context.Context, _ string, replacement *domain.HistorySegment, sourceIDs []string) error {
	f.replacements++
	f.replacement = replacement
	f.replacedIDs = append([]string(nil), sourceIDs...)
	return f.replaceErr
}
func (f *fakeHistoryRepo) Maintain(context.Context, string) error { f.lifecycle++; return nil }
func (f *fakeHistoryRepo) ArchiveColdFacts(context.Context, string) (int, error) {
	f.archives++
	return 0, nil
}

type fakeHistorySummarizer struct {
	summary string
	err     error
}

func (f fakeHistorySummarizer) SummarizeHistory(context.Context, []string) (string, error) {
	return f.summary, f.err
}

type fakeHistoryCompressor struct {
	summary string
	err     error
	items   []string
}

func (f *fakeHistoryCompressor) CompressHistory(_ context.Context, items []string) (string, error) {
	f.items = append([]string(nil), items...)
	return f.summary, f.err
}

func historyBatch(id string) *domain.HistoryBatch {
	return &domain.HistoryBatch{ConversationID: "c", UserID: "u", AgentID: "a", Scope: domain.ScopeUser,
		Entries: []domain.HistorySource{{ID: id, Content: id, CreatedAt: time.Unix(1, 0)}}}
}

func historyOverflow() *domain.HistoryOverflowGroup {
	return &domain.HistoryOverflowGroup{ConversationID: "c1", UserID: "u", AgentID: "a", Scope: domain.ScopeUser, Tier: domain.HistoryTierRecent,
		Sources: []domain.HistorySegment{
			{ID: "s1", ConversationID: "c1", Summary: "full one", PeriodStart: time.Unix(3, 0), PeriodEnd: time.Unix(9, 0), SourceStart: "m3", SourceEnd: "m9", SourceIDs: []string{"m3", "m7", "m9"}, Importance: .4, Confidence: .6},
			{ID: "s2", ConversationID: "c1", Summary: "full two", PeriodStart: time.Unix(1, 0), PeriodEnd: time.Unix(4, 0), SourceStart: "m1", SourceEnd: "m4", SourceIDs: []string{"m1", "m4"}, Importance: .8, Confidence: .4},
		}}
}

func TestHistoryWorkerAggregationPersistsEverySourceID(t *testing.T) {
	repo := &fakeHistoryRepo{batches: []*domain.HistoryBatch{{ConversationID: "c", UserID: "u", AgentID: "a", Scope: domain.ScopeUser, Entries: []domain.HistorySource{
		{ID: "m1", Content: "one", CreatedAt: time.Unix(1, 0)},
		{ID: "m2", Content: "two", CreatedAt: time.Unix(1, 0)},
	}}}}
	NewHistoryWorker("tenant", repo, fakeHistorySummarizer{summary: "summary"}, nil, nil).RunOnce(context.Background())
	if repo.upserted == nil || !reflect.DeepEqual(repo.upserted.SourceIDs, []string{"m1", "m2"}) {
		t.Fatalf("persisted source IDs = %#v", repo.upserted)
	}
}

func TestHistoryWorkerRunOnceDrainsAtMostBoundedAggregationBatches(t *testing.T) {
	repo := &fakeHistoryRepo{batches: []*domain.HistoryBatch{historyBatch("1"), historyBatch("2"), historyBatch("3"), historyBatch("4"), historyBatch("5")}}
	w := NewHistoryWorker("tenant", repo, fakeHistorySummarizer{summary: "summary"}, nil, nil)
	w.RunOnce(context.Background())
	if repo.nextCalls != historyAggregationMaxBatchesPerRun || repo.upserts != historyAggregationMaxBatchesPerRun {
		t.Fatalf("got next=%d upserts=%d", repo.nextCalls, repo.upserts)
	}
}

func TestHistoryWorkerRunOnceStopsAggregationDrainOnNilOrError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		batches    []*domain.HistoryBatch
		batchErrs  []error
		wantCalls  int
		wantWrites int
	}{
		{name: "nil", batches: []*domain.HistoryBatch{historyBatch("1"), nil, historyBatch("3")}, wantCalls: 2, wantWrites: 1},
		{name: "error", batches: []*domain.HistoryBatch{historyBatch("1"), nil}, batchErrs: []error{nil, errors.New("db down")}, wantCalls: 2, wantWrites: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeHistoryRepo{batches: tc.batches, batchErrs: tc.batchErrs}
			NewHistoryWorker("tenant", repo, fakeHistorySummarizer{summary: "summary"}, nil, nil).RunOnce(context.Background())
			if repo.nextCalls != tc.wantCalls || repo.upserts != tc.wantWrites {
				t.Fatalf("got next=%d writes=%d", repo.nextCalls, repo.upserts)
			}
		})
	}
}

func TestHistoryWorkerCompressesOverflowAndReplacesExactSources(t *testing.T) {
	repo := &fakeHistoryRepo{overflow: historyOverflow()}
	compressor := &fakeHistoryCompressor{summary: "compressed"}
	NewHistoryWorker("tenant", repo, nil, compressor, nil).RunOnce(context.Background())
	if repo.replacements != 1 || !reflect.DeepEqual(repo.replacedIDs, []string{"s1", "s2"}) {
		t.Fatalf("replacement calls=%d ids=%v", repo.replacements, repo.replacedIDs)
	}
	if !reflect.DeepEqual(compressor.items, []string{"full one", "full two"}) {
		t.Fatalf("compressor received %v", compressor.items)
	}
	h := repo.replacement
	if h == nil || h.ConversationID != "c1" || h.Tier != domain.HistoryTierEarlier || h.PeriodStart != time.Unix(1, 0) || h.PeriodEnd != time.Unix(9, 0) || h.SourceStart != "m1" || h.SourceEnd != "m9" || h.Summary != "compressed" || h.AggregationKey == "" {
		t.Fatalf("bad replacement: %#v", h)
	}
	if !reflect.DeepEqual(h.SourceIDs, []string{"m3", "m7", "m9", "m1", "m4"}) {
		t.Fatalf("flattened source IDs = %v", h.SourceIDs)
	}
}

func TestHistoryWorkerCompressionBreaksExtremaTiesBySourceID(t *testing.T) {
	stamp := time.Unix(10, 0)
	repo := &fakeHistoryRepo{overflow: &domain.HistoryOverflowGroup{
		ConversationID: "c1", UserID: "u", AgentID: "a", Scope: domain.ScopeUser, Tier: domain.HistoryTierRecent,
		Sources: []domain.HistorySegment{
			{ID: "s1", Summary: "one", PeriodStart: stamp, PeriodEnd: stamp, SourceStart: "m-z", SourceEnd: "m-a", SourceIDs: []string{"m-z", "m-a"}},
			{ID: "s2", Summary: "two", PeriodStart: stamp, PeriodEnd: stamp, SourceStart: "m-a", SourceEnd: "m-z", SourceIDs: []string{"m-a", "m-z"}},
		},
	}}
	NewHistoryWorker("tenant", repo, nil, &fakeHistoryCompressor{summary: "compressed"}, nil).RunOnce(context.Background())
	if got := repo.replacement; got == nil || got.SourceStart != "m-a" || got.SourceEnd != "m-z" {
		t.Fatalf("tie-broken extrema = %#v", got)
	}
}

func TestHistoryWorkerMissingExactSourceIDsSkipsCompressionAndReplacement(t *testing.T) {
	group := historyOverflow()
	group.Sources[1].SourceIDs = nil
	repo := &fakeHistoryRepo{overflow: group}
	compressor := &fakeHistoryCompressor{summary: "compressed"}

	NewHistoryWorker("tenant", repo, nil, compressor, nil).RunOnce(context.Background())

	if compressor.items != nil {
		t.Fatalf("compressor must not be called, got %v", compressor.items)
	}
	if repo.replacements != 0 {
		t.Fatalf("replacement must not be called, got %d calls", repo.replacements)
	}
}

func TestHistoryWorkerCompressionFailureOrEmptyLeavesSourcesActive(t *testing.T) {
	for _, tc := range []struct {
		name       string
		compressor HistoryCompressor
	}{
		{name: "nil compressor", compressor: nil},
		{name: "error", compressor: &fakeHistoryCompressor{err: errors.New("llm down")}},
		{name: "empty", compressor: &fakeHistoryCompressor{summary: "  "}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeHistoryRepo{overflow: historyOverflow()}
			NewHistoryWorker("tenant", repo, nil, tc.compressor, nil).RunOnce(context.Background())
			if repo.replacements != 0 {
				t.Fatal("sources must remain active when compression produces no replacement")
			}
		})
	}
}

func TestHistoryWorkerReplacementFailureDoesNotAttemptSeparateArchival(t *testing.T) {
	repo := &fakeHistoryRepo{overflow: historyOverflow(), replaceErr: errors.New("write failed")}
	NewHistoryWorker("tenant", repo, nil, &fakeHistoryCompressor{summary: "compressed"}, nil).RunOnce(context.Background())
	if repo.replacements != 1 {
		t.Fatalf("expected one atomic replacement call, got %d", repo.replacements)
	}
}

func TestHistoryWorkerCompressionReplacementIsIdempotent(t *testing.T) {
	repo := &fakeHistoryRepo{overflow: historyOverflow()}
	w := NewHistoryWorker("tenant", repo, nil, &fakeHistoryCompressor{summary: "compressed"}, nil)
	w.RunOnce(context.Background())
	firstKey := repo.replacement.AggregationKey
	w.RunOnce(context.Background())
	if repo.replacements != 2 || firstKey == "" || repo.replacement.AggregationKey != firstKey {
		t.Fatalf("replacement key changed across retry: first=%q second=%q", firstKey, repo.replacement.AggregationKey)
	}
}

func TestHistoryWorkerSummaryFailureLeavesBatchForRetryAndContinuesMaintenance(t *testing.T) {
	repo := &fakeHistoryRepo{batches: []*domain.HistoryBatch{historyBatch("m1")}}
	w := NewHistoryWorker("tenant", repo, fakeHistorySummarizer{err: errors.New("llm down")}, nil, nil)
	w.RunOnce(context.Background())
	if repo.upserts != 0 || repo.lifecycle != 1 || repo.archives != 1 {
		t.Fatalf("failure must skip write but continue lifecycle: %#v", repo)
	}
}
