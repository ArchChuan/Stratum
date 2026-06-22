package workers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// EmbedWorker periodically embeds facts and upserts to vector store.
type EmbedWorker struct {
	factRepo port.FactRepo
	embed    port.Embedder
	store    port.VectorStore
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewEmbedWorker creates an embed worker.
func NewEmbedWorker(repo port.FactRepo, embed port.Embedder, store port.VectorStore, logger *zap.Logger) *EmbedWorker {
	return &EmbedWorker{
		factRepo: repo,
		embed:    embed,
		store:    store,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// Start begins periodic embedding until ctx is cancelled or Stop is called.
func (w *EmbedWorker) Start(ctx context.Context) {
	w.logger.Info("memory.embed_worker.start")
	ticker := time.NewTicker(constants.MemoryEmbedInterval)
	defer ticker.Stop()

	// Run once immediately
	w.RunOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory.embed_worker.context_cancelled")
			return
		case <-w.stopCh:
			w.logger.Info("memory.embed_worker.stopped")
			return
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single embedding pass with panic recovery.
func (w *EmbedWorker) RunOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.embed_worker.panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	start := time.Now()

	// Find facts needing embedding
	// Using FindSupersedeCandidates with empty content as a workaround to get recent facts
	// In production, we'd add FactRepo.FindMissingEmbeddings method
	facts, err := w.factRepo.FindSupersedeCandidates(ctx, "", "", "", 0, float64(constants.MemoryEmbedBatchSize))
	if err != nil {
		w.logger.Error("memory.embed_worker.find_facts_failed", zap.Error(err))
		return
	}

	if len(facts) == 0 {
		return
	}

	// Embed and upsert
	embedCount := 0
	for _, fact := range facts {
		if err := w.embedAndUpsert(ctx, fact); err != nil {
			w.logger.Warn("memory.embed_worker.embed_failed",
				zap.String("fact_id", fact.ID),
				zap.Error(err))
			continue
		}
		embedCount++
	}

	if embedCount > 0 {
		w.logger.Info("memory.embed_worker.batch_complete",
			zap.Int("embed_count", embedCount),
			zap.Int64("latency_ms", time.Since(start).Milliseconds()))
	}
}

// embedAndUpsert embeds a single fact and upserts to vector store.
func (w *EmbedWorker) embedAndUpsert(ctx context.Context, fact *domain.MemoryFact) error {
	vector, err := w.embed.Embed(ctx, fact.Content)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}

	collectionName := fmt.Sprintf("memory_facts_%s", fact.UserID)
	doc := &port.VectorDoc{
		ID:        fact.ID,
		Embedding: vector,
		Metadata: map[string]interface{}{
			"user_id":    fact.UserID,
			"agent_id":   fact.AgentID,
			"content":    fact.Content,
			"importance": fact.Importance,
		},
	}

	if err := w.store.Upsert(ctx, collectionName, []*port.VectorDoc{doc}); err != nil {
		return fmt.Errorf("upsert vector: %w", err)
	}

	w.logger.Debug("memory.embed_worker.fact_embedded",
		zap.String("fact_id", fact.ID))
	return nil
}

// Stop gracefully stops the worker (idempotent).
func (w *EmbedWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
