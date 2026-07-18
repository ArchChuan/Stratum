package workers_test

import (
	"context"
	"errors"
	"testing"
	"time"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

type stubFactRepo struct {
	findCandidatesFunc func(context.Context, string, string, string, string, float64, float64) ([]*port.SupersedeCandidate, error)
	updateFunc         func(context.Context, string, *domain.MemoryFact) error
	purgeFunc          func(context.Context, string, time.Time, int) (int, error)
}

func (r *stubFactRepo) Create(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
	return nil
}

func (r *stubFactRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.MemoryFact, error) {
	return nil, nil
}

func (r *stubFactRepo) Update(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
	if r.updateFunc == nil {
		return nil
	}
	return r.updateFunc(ctx, tenantID, fact)
}

func (r *stubFactRepo) ListActive(ctx context.Context, tenantID string, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error) {
	if r.findCandidatesFunc != nil {
		newFact, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "I like coffee now", 0.8, nil)
		return []*domain.MemoryFact{newFact}, nil
	}
	return nil, nil
}

func (r *stubFactRepo) SearchByContent(ctx context.Context, tenantID string, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error) {
	return nil, nil
}

func (r *stubFactRepo) FindSupersedeCandidates(ctx context.Context, tenantID string, filter domain.ScopeFilter, content string, minSimilarity, maxCount float64) ([]*port.SupersedeCandidate, error) {
	if r.findCandidatesFunc == nil {
		return nil, nil
	}
	return r.findCandidatesFunc(ctx, tenantID, filter.UserID, filter.AgentID, content, minSimilarity, maxCount)
}

func (r *stubFactRepo) CountByUser(ctx context.Context, tenantID, userID string) (int, error) {
	return 0, nil
}

func (r *stubFactRepo) Delete(ctx context.Context, tenantID, id string) error {
	return nil
}

func (r *stubFactRepo) DeleteAllByUser(ctx context.Context, tenantID, userID string) ([]string, error) {
	return nil, nil
}

func (r *stubFactRepo) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) ([]string, error) {
	return nil, nil
}

func (r *stubFactRepo) PurgeSuperseded(ctx context.Context, tenantID string, olderThan time.Time, limit int) (int, error) {
	if r.purgeFunc != nil {
		return r.purgeFunc(ctx, tenantID, olderThan, limit)
	}
	return 0, nil
}

type stubSuperseder struct {
	judgeFunc func(context.Context, string, string) (*port.SupersedeJudgment, error)
}

func (s *stubSuperseder) JudgeSupersede(ctx context.Context, oldFact, newFact string) (*port.SupersedeJudgment, error) {
	if s.judgeFunc == nil {
		return &port.SupersedeJudgment{Supersedes: false}, nil
	}
	return s.judgeFunc(ctx, oldFact, newFact)
}

func TestSupersedeWorker_MarksSuperseeded(t *testing.T) {
	oldFact, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "I like tea", 0.7, nil)
	var updatedFact *domain.MemoryFact

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*port.SupersedeCandidate, error) {
			return []*port.SupersedeCandidate{{Fact: oldFact, Similarity: 0.75}}, nil
		},
		updateFunc: func(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
			updatedFact = fact
			return nil
		},
	}

	superseder := &stubSuperseder{
		judgeFunc: func(ctx context.Context, old, new string) (*port.SupersedeJudgment, error) {
			return &port.SupersedeJudgment{Supersedes: true, Reason: "preference changed"}, nil
		},
	}

	worker := workers.NewSupersedeWorker("", repo, superseder, zap.NewNop())
	worker.RunOnce(context.Background())

	require.NotNil(t, updatedFact, "should update fact")
	require.Equal(t, "superseded", updatedFact.Status, "should mark as superseded")
}

func TestSupersedeWorker_KeepsOnNoSupersede(t *testing.T) {
	oldFact, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "I like coffee", 0.8, nil)
	var updated bool

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*port.SupersedeCandidate, error) {
			return []*port.SupersedeCandidate{{Fact: oldFact, Similarity: 0.75}}, nil
		},
		updateFunc: func(ctx context.Context, tenantID string, fact *domain.MemoryFact) error {
			updated = true
			return nil
		},
	}

	superseder := &stubSuperseder{
		judgeFunc: func(ctx context.Context, old, new string) (*port.SupersedeJudgment, error) {
			return &port.SupersedeJudgment{Supersedes: false, Reason: "different topics"}, nil
		},
	}

	worker := workers.NewSupersedeWorker("", repo, superseder, zap.NewNop())
	worker.RunOnce(context.Background())

	require.False(t, updated, "should not update if no supersede")
}

func TestSupersedeWorker_RecoversPanic(t *testing.T) {
	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*port.SupersedeCandidate, error) {
			panic("database exploded")
		},
	}

	worker := workers.NewSupersedeWorker("", repo, &stubSuperseder{}, zap.NewNop())
	worker.RunOnce(context.Background())
}

func TestSupersedeWorker_HandlesJudgeError(t *testing.T) {
	oldFact, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "test", 0.5, nil)

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*port.SupersedeCandidate, error) {
			return []*port.SupersedeCandidate{{Fact: oldFact, Similarity: 0.75}}, nil
		},
	}

	superseder := &stubSuperseder{
		judgeFunc: func(ctx context.Context, old, new string) (*port.SupersedeJudgment, error) {
			return nil, errors.New("llm timeout")
		},
	}

	worker := workers.NewSupersedeWorker("", repo, superseder, zap.NewNop())
	worker.RunOnce(context.Background())
}

func TestSupersedeWorkerRecoversWhenResolvingClientBecomesAvailable(t *testing.T) {
	oldFact, _ := domain.NewFact("", "user1", "agent1", "", string(domain.ScopeUser), "I like tea", 0.7, nil)
	updates := 0
	repo := &stubFactRepo{
		findCandidatesFunc: func(context.Context, string, string, string, string, float64, float64) ([]*port.SupersedeCandidate, error) {
			return []*port.SupersedeCandidate{{Fact: oldFact, Similarity: 0.75}}, nil
		},
		updateFunc: func(context.Context, string, *domain.MemoryFact) error {
			updates++
			return nil
		},
	}
	available := false
	resolver := func(context.Context, string) (workers.TenantLLMClient, error) {
		if !available {
			return nil, errors.New("temporarily unavailable")
		}
		return completionClientFunc(func(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
			return &llmdomain.CompletionResponse{Content: `{"supersedes":true,"reason":"updated"}`}, nil
		}), nil
	}
	worker := workers.NewSupersedeWorker("tenant-1", repo, workers.NewResolvingLLMSuperseder("tenant-1", resolver), zap.NewNop())

	worker.RunOnce(context.Background())
	require.Zero(t, updates)
	available = true
	worker.RunOnce(context.Background())
	require.Equal(t, 1, updates)
}

func TestSupersedeWorker_GracefulShutdown(t *testing.T) {
	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*port.SupersedeCandidate, error) {
			return nil, nil
		},
	}

	worker := workers.NewSupersedeWorker("", repo, &stubSuperseder{}, zap.NewNop())

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
