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
func (f *fakeHistoryRepo) Upsert(context.Context, string, *domain.HistorySegment) error {
	f.upserts++
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
	return &domain.HistoryOverflowGroup{UserID: "u", AgentID: "a", Scope: domain.ScopeUser, Tier: domain.HistoryTierRecent,
		Sources: []domain.HistorySegment{
			{ID: "s1", ConversationID: "c1", Summary: "full one", PeriodStart: time.Unix(1, 0), PeriodEnd: time.Unix(2, 0), SourceStart: "m1", SourceEnd: "m2", Importance: .4, Confidence: .6},
			{ID: "s2", ConversationID: "c2", Summary: "full two", PeriodStart: time.Unix(3, 0), PeriodEnd: time.Unix(4, 0), SourceStart: "m3", SourceEnd: "m4", Importance: .8, Confidence: .4},
		}}
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
	if h == nil || h.Tier != domain.HistoryTierEarlier || h.SourceStart != "m1" || h.SourceEnd != "m4" || h.Summary != "compressed" || h.AggregationKey == "" {
		t.Fatalf("bad replacement: %#v", h)
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
