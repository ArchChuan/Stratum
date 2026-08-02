package wiring

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/application"
	"github.com/byteBuilderX/stratum/internal/audit/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// Audit groups the audit recorder, query service, and cleanup worker.
type Audit struct {
	Recorder     *application.AuditService
	QueryService *application.AuditService
}

func buildAudit(db *pgxpool.Pool, logger *zap.Logger) *Audit {
	if db == nil {
		return nil
	}
	repo := persistence.NewPgAuditRepo(db)
	// Use NoopMetrics for now — metrics provider is injected later at the
	// agent level. Audit metrics use the platform-level provider wired separately.
	svc := application.NewAuditService(repo, nil, logger)
	return &Audit{
		Recorder:     svc,
		QueryService: svc,
	}
}

// AuditCleanupWorker runs periodic retention cleanup.
type AuditCleanupWorker struct {
	svc      *application.AuditService
	querySvc *application.AuditService
	interval time.Duration
	logger   *zap.Logger
}

// NewAuditCleanupWorker creates a retention cleanup worker.
func NewAuditCleanupWorker(svc, querySvc *application.AuditService, logger *zap.Logger) *AuditCleanupWorker {
	return &AuditCleanupWorker{
		svc:      svc,
		querySvc: querySvc,
		interval: time.Duration(constants.AuditCleanupInterval) * time.Hour,
		logger:   logger,
	}
}

// Run starts the periodic retention cleanup loop.
func (w *AuditCleanupWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			before := time.Now().Add(-time.Duration(constants.AuditRetentionDays) * 24 * time.Hour)
			if err := w.svc.DeleteOlderThan(ctx, before); err != nil {
				w.logger.Warn("audit cleanup: delete older failed", zap.Error(err))
			} else {
				w.logger.Debug("audit cleanup: completed", zap.Time("before", before))
			}
		case <-ctx.Done():
			return
		}
	}
}
