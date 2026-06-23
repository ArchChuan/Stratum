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

type stubEmbedder struct {
	embedFunc func(context.Context, string) ([]float32, error)
}

func (e *stubEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return e.embedFunc(ctx, text)
}

func (e *stubEmbedder) Dim() int {
	return 384
}

type stubVectorStore struct {
	upsertFunc func(context.Context, string, []*port.VectorDoc) error
}

func (s *stubVectorStore) Upsert(ctx context.Context, collectionName string, docs []*port.VectorDoc) error {
	return s.upsertFunc(ctx, collectionName, docs)
}

func (s *stubVectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int, filter map[string]interface{}) ([]*port.VectorDoc, error) {
	return nil, nil
}

func (s *stubVectorStore) Delete(ctx context.Context, collectionName string, ids []string) error {
	return nil
}

func (s *stubVectorStore) CreateCollection(ctx context.Context, collectionName string, dimension int) error {
	return nil
}

func TestEmbedWorker_EmbedsFactsWithoutVectors(t *testing.T) {
	fact1, _ := domain.NewFact("", "user1", "agent1", string(domain.ScopeUser), "I like coffee", 0.8, nil)
	fact2, _ := domain.NewFact("", "user1", "agent1", string(domain.ScopeUser), "I prefer tea", 0.7, nil)

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			// Simulate returning facts needing embedding
			return []*domain.MemoryFact{fact1, fact2}, nil
		},
	}

	var embeddedTexts []string
	embedder := &stubEmbedder{
		embedFunc: func(ctx context.Context, text string) ([]float32, error) {
			embeddedTexts = append(embeddedTexts, text)
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}

	var upsertedDocs []*port.VectorDoc
	vectorStore := &stubVectorStore{
		upsertFunc: func(ctx context.Context, collectionName string, docs []*port.VectorDoc) error {
			upsertedDocs = append(upsertedDocs, docs...)
			return nil
		},
	}

	worker := workers.NewEmbedWorker("", repo, embedder, vectorStore, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	worker.Start(ctx)

	require.Len(t, embeddedTexts, 2, "should embed 2 facts")
	require.Contains(t, embeddedTexts, "I like coffee")
	require.Contains(t, embeddedTexts, "I prefer tea")
	require.Len(t, upsertedDocs, 2, "should upsert 2 vectors")
}

func TestEmbedWorker_HandlesEmbedError(t *testing.T) {
	fact, _ := domain.NewFact("", "user1", "agent1", string(domain.ScopeUser), "test", 0.5, nil)

	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			return []*domain.MemoryFact{fact}, nil
		},
	}

	embedder := &stubEmbedder{
		embedFunc: func(ctx context.Context, text string) ([]float32, error) {
			return nil, errors.New("embedding service timeout")
		},
	}

	vectorStore := &stubVectorStore{
		upsertFunc: func(ctx context.Context, collectionName string, docs []*port.VectorDoc) error {
			t.Fatal("should not call upsert on embed error")
			return nil
		},
	}

	worker := workers.NewEmbedWorker("", repo, embedder, vectorStore, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	// Should not panic
	worker.Start(ctx)
}

func TestEmbedWorker_GracefulShutdown(t *testing.T) {
	repo := &stubFactRepo{
		findCandidatesFunc: func(ctx context.Context, tenantID, userID, agentID, content string, minSim, maxCount float64) ([]*domain.MemoryFact, error) {
			return nil, nil
		},
	}

	worker := workers.NewEmbedWorker("", repo, &stubEmbedder{}, &stubVectorStore{}, zap.NewNop())

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
