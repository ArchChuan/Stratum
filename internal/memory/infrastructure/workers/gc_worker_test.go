package workers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

func TestGCWorker_RunOnce_NoPanic(t *testing.T) {
	repo := &stubFactRepo{}
	worker := workers.NewGCWorker("", repo, zap.NewNop())
	worker.RunOnce(context.Background())
}

func TestGCWorker_PurgeSuperseded_DrainsInBatches(t *testing.T) {
	// Simulate a backlog of 250 superseded facts; batch size is 100, so the
	// worker must call PurgeSuperseded 3 times (100, 100, 50) and stop once a
	// short batch signals the backlog is drained.
	remaining := 250
	calls := 0
	var lastCutoff time.Time
	repo := &stubFactRepo{
		purgeFunc: func(_ context.Context, _ string, cutoff time.Time, limit int) ([]string, error) {
			calls++
			lastCutoff = cutoff
			n := remaining
			if n > limit {
				n = limit
			}
			remaining -= n
			return make([]string, n), nil
		},
	}
	worker := workers.NewGCWorker("tenant-a", repo, zap.NewNop())
	worker.RunOnce(context.Background())

	if calls != 3 {
		t.Fatalf("expected 3 purge batches, got %d", calls)
	}
	if remaining != 0 {
		t.Fatalf("expected backlog fully drained, %d left", remaining)
	}
	// Cutoff must be in the past (retention window applied), never in the future.
	if !lastCutoff.Before(time.Now()) {
		t.Fatalf("cutoff %v should predate now", lastCutoff)
	}
}

func TestGCWorker_PurgeSuperseded_StopsOnShortBatch(t *testing.T) {
	// A single short batch (fewer rows than the limit) means no more work.
	calls := 0
	repo := &stubFactRepo{
		purgeFunc: func(_ context.Context, _ string, _ time.Time, _ int) ([]string, error) {
			calls++
			return make([]string, 5), nil
		},
	}
	worker := workers.NewGCWorker("tenant-a", repo, zap.NewNop())
	worker.RunOnce(context.Background())

	if calls != 1 {
		t.Fatalf("expected exactly 1 purge call on short batch, got %d", calls)
	}
}

func TestGCWorker_GracefulShutdown(t *testing.T) {
	repo := &stubFactRepo{}
	worker := workers.NewGCWorker("", repo, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("worker did not stop within 1s")
	}
}

type stubVectorStore struct {
	deletedFactIDs  [][]string
	deletedEntryIDs [][]string
	err             error
}

func (s *stubVectorStore) Upsert(context.Context, string, []*port.VectorDoc) error { return nil }
func (s *stubVectorStore) Search(context.Context, string, []float32, int, port.VectorSearchFilter) ([]*port.VectorDoc, error) {
	return nil, nil
}
func (s *stubVectorStore) Delete(context.Context, string, []string) error { return nil }
func (s *stubVectorStore) DeleteAllByUser(context.Context, string, string) error {
	return nil
}
func (s *stubVectorStore) DeleteAllByAgent(context.Context, string, string) error {
	return nil
}
func (s *stubVectorStore) DeleteEntryVectors(_ context.Context, _ string, ids []string) error {
	if s.err != nil {
		return s.err
	}
	s.deletedEntryIDs = append(s.deletedEntryIDs, ids)
	return nil
}
func (s *stubVectorStore) DeleteFactVectors(_ context.Context, _ string, ids []string) error {
	if s.err != nil {
		return s.err
	}
	s.deletedFactIDs = append(s.deletedFactIDs, ids)
	return nil
}
func (s *stubVectorStore) CreateCollection(context.Context, string, int) error { return nil }

type stubMemoryRepo struct {
	expiredBatches [][]string
	pos            int
	deletedIDs     [][]string
	deleteErr      error
}

func (s *stubMemoryRepo) ListExpired(context.Context, string, time.Time, time.Time, int) ([]string, error) {
	if s.pos >= len(s.expiredBatches) {
		return nil, nil
	}
	ids := s.expiredBatches[s.pos]
	s.pos++
	return ids, nil
}
func (s *stubMemoryRepo) DeleteByIDs(_ context.Context, _ string, ids []string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deletedIDs = append(s.deletedIDs, ids)
	return nil
}
func (s *stubMemoryRepo) Add(context.Context, *domain.MemoryEntry) error { return nil }
func (s *stubMemoryRepo) Get(context.Context, string, string) (*domain.MemoryEntry, error) {
	return nil, nil
}
func (s *stubMemoryRepo) Search(context.Context, string, string, string, int) ([]*domain.MemoryEntry, error) {
	return nil, nil
}
func (s *stubMemoryRepo) Delete(context.Context, string, string) error { return nil }
func (s *stubMemoryRepo) ClearSession(context.Context, string, string) error {
	return nil
}
func (s *stubMemoryRepo) DeleteAllByUser(context.Context, string, string) error {
	return nil
}
func (s *stubMemoryRepo) DeleteAllByAgent(context.Context, string, string) error {
	return nil
}
func (s *stubMemoryRepo) Stats(context.Context, string) (*domain.MemoryStats, error) {
	return nil, nil
}
func (s *stubMemoryRepo) GetSummary(context.Context, string, string) (string, error) {
	return "", nil
}

func TestGCWorker_PurgeExpiredEntries_DeletesVectorsThenRows(t *testing.T) {
	memoryRepo := &stubMemoryRepo{expiredBatches: [][]string{{"e1", "e2"}, nil}}
	vectors := &stubVectorStore{}
	worker := workers.NewGCWorker("tenant-a", &stubFactRepo{}, zap.NewNop()).
		WithMemoryRepo(memoryRepo).WithVectorStore(vectors)

	worker.RunOnce(context.Background())

	require.Equal(t, [][]string{{"e1", "e2"}}, vectors.deletedEntryIDs)
	require.Equal(t, [][]string{{"e1", "e2"}}, memoryRepo.deletedIDs)
}

func TestGCWorker_PurgeExpiredEntries_AbortsOnVectorFailure(t *testing.T) {
	memoryRepo := &stubMemoryRepo{expiredBatches: [][]string{{"e1"}}}
	vectors := &stubVectorStore{err: errors.New("milvus unavailable")}
	worker := workers.NewGCWorker("tenant-a", &stubFactRepo{}, zap.NewNop()).
		WithMemoryRepo(memoryRepo).WithVectorStore(vectors)

	worker.RunOnce(context.Background())

	// 向量删除失败必须中止，PG 行保留作为事实源，下轮幂等重试。
	require.Empty(t, memoryRepo.deletedIDs)
}

func TestGCWorker_PurgeSuperseded_DeletesVectorsForPurgedFacts(t *testing.T) {
	repo := &stubFactRepo{
		purgeFunc: func(context.Context, string, time.Time, int) ([]string, error) {
			return []string{"f1", "f2"}, nil
		},
	}
	vectors := &stubVectorStore{}
	worker := workers.NewGCWorker("tenant-a", repo, zap.NewNop()).WithVectorStore(vectors)

	worker.RunOnce(context.Background())

	require.Equal(t, [][]string{{"f1", "f2"}}, vectors.deletedFactIDs)
}

func TestGCWorker_PurgeSuperseded_VectorFailureDoesNotBlockRowPurge(t *testing.T) {
	purged := 0
	repo := &stubFactRepo{
		purgeFunc: func(context.Context, string, time.Time, int) ([]string, error) {
			purged++
			return []string{"f1"}, nil
		},
	}
	vectors := &stubVectorStore{err: errors.New("milvus unavailable")}
	worker := workers.NewGCWorker("tenant-a", repo, zap.NewNop()).WithVectorStore(vectors)

	worker.RunOnce(context.Background())

	// 行已过保留期必须继续清理（否则永久堆积）；向量删除失败 ERROR 暴露，
	// 召回侧状态过滤保证残留向量不可见。
	require.Equal(t, 1, purged, "行清理不能被向量删除失败阻断")
	require.Nil(t, vectors.deletedFactIDs)
}
