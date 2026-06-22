package workers

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// SupersedeWorker periodically checks for superseded facts.
type SupersedeWorker struct {
	factRepo port.FactRepo
	judge    port.LLMSuperseder
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewSupersedeWorker creates a supersede worker.
func NewSupersedeWorker(repo port.FactRepo, judge port.LLMSuperseder, logger *zap.Logger) *SupersedeWorker {
	return &SupersedeWorker{
		factRepo: repo,
		judge:    judge,
		logger:   logger,
		stopCh:   make(chan struct{}),
	}
}

// Start begins periodic supersede checking until ctx is cancelled or Stop is called.
func (w *SupersedeWorker) Start(ctx context.Context) {
	w.logger.Info("memory.supersede_worker.start")
	ticker := time.NewTicker(constants.MemoryProfileInterval) // Reuse profile interval for now
	defer ticker.Stop()

	// Run once immediately
	w.RunOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory.supersede_worker.context_cancelled")
			return
		case <-w.stopCh:
			w.logger.Info("memory.supersede_worker.stopped")
			return
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single supersede check pass with panic recovery.
func (w *SupersedeWorker) RunOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.supersede_worker.panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	start := time.Now()

	// Find recent active facts that might have supersede candidates
	// Using empty filter to check across all active facts (simplified for v1)
	recentFacts, err := w.factRepo.ListActive(ctx, domain.ScopeFilter{}, constants.MemorySupersedeBatchSize)
	if err != nil {
		w.logger.Error("memory.supersede_worker.list_active_failed", zap.Error(err))
		return
	}

	if len(recentFacts) == 0 {
		return
	}

	supersededCount := 0
	for _, fact := range recentFacts {
		// Find candidates that this fact might supersede
		candidates, err := w.factRepo.FindSupersedeCandidates(
			ctx,
			fact.UserID,
			fact.AgentID,
			fact.Content,
			constants.MemorySupersedeCandidateMin,
			float64(constants.MemorySupersedeCandidateMax),
		)
		if err != nil {
			w.logger.Warn("memory.supersede_worker.find_candidates_failed",
				zap.String("fact_id", fact.ID),
				zap.Error(err))
			continue
		}

		for _, candidate := range candidates {
			// Skip self
			if candidate.ID == fact.ID {
				continue
			}

			// Skip already superseded
			if candidate.Status == "superseded" {
				continue
			}

			judgment, err := w.judge.JudgeSupersede(ctx, candidate.Content, fact.Content)
			if err != nil {
				w.logger.Warn("memory.supersede_worker.judge_failed",
					zap.String("old_fact_id", candidate.ID),
					zap.String("new_fact_id", fact.ID),
					zap.Error(err))
				continue
			}

			if judgment.Supersedes {
				if err := candidate.MarkSuperseded(fact.ID); err != nil {
					w.logger.Error("memory.supersede_worker.mark_failed",
						zap.String("fact_id", candidate.ID),
						zap.Error(err))
					continue
				}

				if err := w.factRepo.Update(ctx, candidate); err != nil {
					w.logger.Error("memory.supersede_worker.update_failed",
						zap.String("fact_id", candidate.ID),
						zap.Error(err))
					continue
				}

				supersededCount++
				w.logger.Info("memory.supersede_worker.superseded",
					zap.String("old_fact_id", candidate.ID),
					zap.String("new_fact_id", fact.ID),
					zap.String("reason", judgment.Reason))
			}
		}
	}

	if supersededCount > 0 {
		w.logger.Info("memory.supersede_worker.batch_complete",
			zap.Int("superseded_count", supersededCount),
			zap.Int64("latency_ms", time.Since(start).Milliseconds()))
	}
}

// Stop gracefully stops the worker (idempotent).
func (w *SupersedeWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
