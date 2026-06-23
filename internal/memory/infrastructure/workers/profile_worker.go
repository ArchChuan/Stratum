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

// ProfileWorker periodically rebuilds entity profiles.
type ProfileWorker struct {
	tenantID   string
	entityRepo port.EntityRepo
	factRepo   port.FactRepo
	profiler   port.EntityProfiler
	logger     *zap.Logger
	stopCh     chan struct{}
	stopOnce   sync.Once
}

// NewProfileWorker creates a profile worker for a specific tenant.
func NewProfileWorker(tenantID string, entityRepo port.EntityRepo, factRepo port.FactRepo, profiler port.EntityProfiler, logger *zap.Logger) *ProfileWorker {
	return &ProfileWorker{
		tenantID:   tenantID,
		entityRepo: entityRepo,
		factRepo:   factRepo,
		profiler:   profiler,
		logger:     logger,
		stopCh:     make(chan struct{}),
	}
}

// Start begins periodic profile rebuilding until ctx is cancelled or Stop is called.
func (w *ProfileWorker) Start(ctx context.Context) {
	w.logger.Info("memory.profile_worker.start")
	ticker := time.NewTicker(constants.MemoryProfileInterval)
	defer ticker.Stop()

	// Run once immediately
	w.RunOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("memory.profile_worker.context_cancelled")
			return
		case <-w.stopCh:
			w.logger.Info("memory.profile_worker.stopped")
			return
		case <-ticker.C:
			w.RunOnce(ctx)
		}
	}
}

// RunOnce performs a single profile rebuild pass with panic recovery.
func (w *ProfileWorker) RunOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.profile_worker.panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	start := time.Now()

	// Find entities needing profile rebuild
	entities, err := w.entityRepo.ListProfiles(ctx, domain.ScopeFilter{TenantID: w.tenantID, IncludeUserScope: true, IncludeAgentScope: true}, constants.MemoryProfileBatchSize)
	if err != nil {
		w.logger.Error("memory.profile_worker.list_profiles_failed", zap.Error(err))
		return
	}

	if len(entities) == 0 {
		return
	}

	rebuildCount := 0
	for _, entity := range entities {
		if !entity.ShouldRebuildProfile() {
			continue
		}

		if err := w.rebuildProfile(ctx, entity); err != nil {
			w.logger.Warn("memory.profile_worker.rebuild_failed",
				zap.String("entity_id", entity.ID),
				zap.String("entity_name", entity.Name),
				zap.Error(err))
			continue
		}
		rebuildCount++
	}

	if rebuildCount > 0 {
		w.logger.Info("memory.profile_worker.batch_complete",
			zap.Int("rebuild_count", rebuildCount),
			zap.Int64("latency_ms", time.Since(start).Milliseconds()))
	}
}

// rebuildProfile rebuilds profile for a single entity.
func (w *ProfileWorker) rebuildProfile(ctx context.Context, entity *domain.MemoryEntity) error {
	// Find facts mentioning this entity
	// Using FindSupersedeCandidates with entity name as content (workaround)
	facts, err := w.factRepo.FindSupersedeCandidates(
		ctx,
		w.tenantID,
		entity.UserID,
		entity.AgentID,
		entity.Name,
		0.5,
		50,
	)
	if err != nil {
		return err
	}

	if len(facts) == 0 {
		// No facts found, skip rebuild
		return nil
	}

	// Extract fact contents
	factContents := make([]string, len(facts))
	for i, fact := range facts {
		factContents[i] = fact.Content
	}

	// Generate profile
	profile, err := w.profiler.GenerateProfile(ctx, entity.Name, entity.EntityType, factContents)
	if err != nil {
		return err
	}

	// Update entity
	entity.Profile = profile
	entity.LastProfileRebuildAt = time.Now()
	entity.FactCountSinceRebuild = 0
	entity.UpdatedAt = time.Now()

	if err := w.entityRepo.Update(ctx, entity); err != nil {
		return err
	}

	w.logger.Debug("memory.profile_worker.profile_rebuilt",
		zap.String("entity_id", entity.ID),
		zap.String("entity_name", entity.Name),
		zap.Int("fact_count", len(facts)))

	return nil
}

// Stop gracefully stops the worker (idempotent).
func (w *ProfileWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}
