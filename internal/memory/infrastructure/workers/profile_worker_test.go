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

type stubEntityRepo struct {
	listProfilesFunc func(context.Context, domain.ScopeFilter, int) ([]*domain.MemoryEntity, error)
	updateFunc       func(context.Context, string, *domain.MemoryEntity) error
}

func (r *stubEntityRepo) Create(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error {
	return nil
}

func (r *stubEntityRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryEntity, error) {
	return nil, nil
}

func (r *stubEntityRepo) Update(ctx context.Context, tenantID string, entity *domain.MemoryEntity) error {
	if r.updateFunc == nil {
		return nil
	}
	return r.updateFunc(ctx, tenantID, entity)
}

func (r *stubEntityRepo) FindByNameAndType(ctx context.Context, tenantID string, filter domain.ScopeFilter, name, entityType string, threshold float64) (*domain.MemoryEntity, error) {
	return nil, domain.ErrEntityNotFound
}

func (r *stubEntityRepo) ListProfiles(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
	if r.listProfilesFunc == nil {
		return nil, nil
	}
	return r.listProfilesFunc(ctx, filter, limit)
}

func (r *stubEntityRepo) CountByUser(ctx context.Context, tenantID, userID string) (int, error) {
	return 0, nil
}

func (r *stubEntityRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	return nil
}

func (r *stubEntityRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	return nil
}

type stubProfiler struct {
	generateFunc func(context.Context, string, string, []string) (string, error)
}

func (p *stubProfiler) GenerateProfile(ctx context.Context, entityName, entityType string, facts []string) (string, error) {
	if p.generateFunc == nil {
		return "", nil
	}
	return p.generateFunc(ctx, entityName, entityType, facts)
}

func TestProfileWorker_RebuildsProfiles(t *testing.T) {
	entity, _ := domain.NewEntity("user1", "agent1", string(domain.ScopeUser), "Alice", "person")
	entity.FactCount = 10
	entity.FactCountSinceRebuild = 6
	entity.LastProfileRebuildAt = time.Now().Add(-8 * 24 * time.Hour)

	var updatedEntity *domain.MemoryEntity

	entityRepo := &stubEntityRepo{
		listProfilesFunc: func(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
			return []*domain.MemoryEntity{entity}, nil
		},
		updateFunc: func(ctx context.Context, tenantID string, e *domain.MemoryEntity) error {
			updatedEntity = e
			return nil
		},
	}

	fact1, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "Alice loves coffee", 0.8, nil)
	fact2, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "Alice works at Acme", 0.7, nil)

	factRepo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*port.SupersedeCandidate, error) {
			return []*port.SupersedeCandidate{
				{Fact: fact1, Similarity: 0.8},
				{Fact: fact2, Similarity: 0.7},
			}, nil
		},
	}

	profiler := &stubProfiler{
		generateFunc: func(ctx context.Context, entityName, entityType string, facts []string) (string, error) {
			require.Equal(t, "Alice", entityName)
			require.Len(t, facts, 2)
			return "Alice: coffee lover, works at Acme", nil
		},
	}

	worker := workers.NewProfileWorker("", entityRepo, factRepo, profiler, zap.NewNop())
	worker.RunOnce(context.Background())

	require.NotNil(t, updatedEntity, "should update entity")
	require.Equal(t, "Alice: coffee lover, works at Acme", updatedEntity.Profile)
	require.Equal(t, 0, updatedEntity.FactCountSinceRebuild, "should reset fact count")
}

func TestProfileWorker_SkipsIfNotNeeded(t *testing.T) {
	entity, _ := domain.NewEntity("user1", "agent1", string(domain.ScopeUser), "Bob", "person")
	entity.FactCountSinceRebuild = 2
	entity.LastProfileRebuildAt = time.Now().Add(-1 * 24 * time.Hour)

	var updated bool

	entityRepo := &stubEntityRepo{
		listProfilesFunc: func(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
			return []*domain.MemoryEntity{entity}, nil
		},
		updateFunc: func(ctx context.Context, tenantID string, e *domain.MemoryEntity) error {
			updated = true
			return nil
		},
	}

	worker := workers.NewProfileWorker("", entityRepo, &stubFactRepo{}, &stubProfiler{}, zap.NewNop())
	worker.RunOnce(context.Background())

	require.False(t, updated, "should not update if rebuild not needed")
}

func TestProfileWorker_HandlesProfilerError(t *testing.T) {
	entity, _ := domain.NewEntity("user1", "agent1", string(domain.ScopeUser), "Charlie", "person")
	entity.FactCountSinceRebuild = 6

	entityRepo := &stubEntityRepo{
		listProfilesFunc: func(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
			return []*domain.MemoryEntity{entity}, nil
		},
		updateFunc: func(ctx context.Context, tenantID string, e *domain.MemoryEntity) error {
			t.Fatal("should not call update on profiler error")
			return nil
		},
	}

	fact, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "test", 0.5, nil)
	factRepo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*port.SupersedeCandidate, error) {
			return []*port.SupersedeCandidate{{Fact: fact, Similarity: 0.7}}, nil
		},
	}

	profiler := &stubProfiler{
		generateFunc: func(ctx context.Context, entityName, entityType string, facts []string) (string, error) {
			return "", errors.New("llm timeout")
		},
	}

	worker := workers.NewProfileWorker("", entityRepo, factRepo, profiler, zap.NewNop())
	worker.RunOnce(context.Background())
}

func TestProfileWorker_GracefulShutdown(t *testing.T) {
	entityRepo := &stubEntityRepo{
		listProfilesFunc: func(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
			return nil, nil
		},
	}

	worker := workers.NewProfileWorker("", entityRepo, &stubFactRepo{}, &stubProfiler{}, zap.NewNop())

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
		// OK
	case <-time.After(1 * time.Second):
		t.Fatal("worker did not stop within 1s")
	}
}
