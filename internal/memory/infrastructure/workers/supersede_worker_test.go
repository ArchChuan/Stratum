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

type stubFactRepo struct {
	findCandidatesFunc   func(context.Context, string, string, string, float64, float64) ([]*domain.MemoryFact, error)
	updateFunc           func(context.Context, *domain.MemoryFact) error
	deleteOldSoftDeleted func(context.Context, int) (int, error)
}

func (r *stubFactRepo) Create(ctx context.Context, fact *domain.MemoryFact) error {
	return nil
}

func (r *stubFactRepo) GetByID(ctx context.Context, id string) (*domain.MemoryFact, error) {
	return nil, nil
}

func (r *stubFactRepo) Update(ctx context.Context, fact *domain.MemoryFact) error {
	return r.updateFunc(ctx, fact)
}

func (r *stubFactRepo) ListActive(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error) {
	if r.findCandidatesFunc != nil {
		// Return a new fact that will trigger candidate search
		newFact, _ := domain.NewFact("user1", "agent1", string(domain.ScopeUser), "I like coffee now", 0.8, nil)
		return []*domain.MemoryFact{newFact}, nil
	}
	return nil, nil
}

func (r *stubFactRepo) SearchByContent(ctx context.Context, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error) {
	return nil, nil
}

func (r *stubFactRepo) FindSupersedeCandidates(ctx context.Context, userID, agentID, content string, minSimilarity, maxCount float64) ([]*domain.MemoryFact, error) {
	return r.findCandidatesFunc(ctx, userID, agentID, content, minSimilarity, maxCount)
}

func (r *stubFactRepo) CountByUser(ctx context.Context, userID string) (int, error) {
	return 0, nil
}

func (r *stubFactRepo) DeleteOldSoftDeleted(ctx context.Context, retentionDays int) (int, error) {
	if r.deleteOldSoftDeleted != nil {
		return r.deleteOldSoftDeleted(ctx, retentionDays)
	}
	return 0, nil
}

func (r *stubFactRepo) CountActive(ctx context.Context, tenantID string) (int, error) {
	return 0, nil
}

func (r *stubFactRepo) CountSuperseded(ctx context.Context, tenantID string) (int, error) {
	return 0, nil
}

type stubSuperseder struct {
	judgeFunc func(context.Context, string, string) (*port.SupersedeJudgment, error)
}

func (s *stubSuperseder) JudgeSupersede(ctx context.Context, oldFact, newFact string) (*port.SupersedeJudgment, error) {
	return s.judgeFunc(ctx, oldFact, newFact)
}

func TestSupersedeWorker_MarksSuperseeded(t *testing.T) {
	oldFact, _ := domain.NewFact("user1", "agent1", string(domain.ScopeUser), "I like tea", 0.7, nil)
	var updatedFact *domain.MemoryFact

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			return []*domain.MemoryFact{oldFact}, nil
		},
		updateFunc: func(ctx context.Context, fact *domain.MemoryFact) error {
			updatedFact = fact
			return nil
		},
	}

	superseder := &stubSuperseder{
		judgeFunc: func(ctx context.Context, old, new string) (*port.SupersedeJudgment, error) {
			return &port.SupersedeJudgment{Supersedes: true, Reason: "preference changed"}, nil
		},
	}

	worker := workers.NewSupersedeWorker(repo, superseder, zap.NewNop())
	worker.RunOnce(context.Background())

	require.NotNil(t, updatedFact, "should update fact")
	require.Equal(t, "superseded", updatedFact.Status, "should mark as superseded")
}

func TestSupersedeWorker_KeepsOnNoSupersede(t *testing.T) {
	oldFact, _ := domain.NewFact("user1", "agent1", string(domain.ScopeUser), "I like coffee", 0.8, nil)
	var updated bool

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			return []*domain.MemoryFact{oldFact}, nil
		},
		updateFunc: func(ctx context.Context, fact *domain.MemoryFact) error {
			updated = true
			return nil
		},
	}

	superseder := &stubSuperseder{
		judgeFunc: func(ctx context.Context, old, new string) (*port.SupersedeJudgment, error) {
			return &port.SupersedeJudgment{Supersedes: false, Reason: "different topics"}, nil
		},
	}

	worker := workers.NewSupersedeWorker(repo, superseder, zap.NewNop())
	worker.RunOnce(context.Background())

	require.False(t, updated, "should not update if no supersede")
}

func TestSupersedeWorker_RecoversPanic(t *testing.T) {
	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			panic("database exploded")
		},
	}

	worker := workers.NewSupersedeWorker(repo, &stubSuperseder{}, zap.NewNop())
	// Should not panic
	worker.RunOnce(context.Background())
}

func TestSupersedeWorker_HandlesJudgeError(t *testing.T) {
	oldFact, _ := domain.NewFact("user1", "agent1", string(domain.ScopeUser), "test", 0.5, nil)

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			return []*domain.MemoryFact{oldFact}, nil
		},
	}

	superseder := &stubSuperseder{
		judgeFunc: func(ctx context.Context, old, new string) (*port.SupersedeJudgment, error) {
			return nil, errors.New("llm timeout")
		},
	}

	worker := workers.NewSupersedeWorker(repo, superseder, zap.NewNop())
	// Should not panic, just log error
	worker.RunOnce(context.Background())
}

func TestSupersedeWorker_GracefulShutdown(t *testing.T) {
	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			return nil, nil
		},
	}

	worker := workers.NewSupersedeWorker(repo, &stubSuperseder{}, zap.NewNop())

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
