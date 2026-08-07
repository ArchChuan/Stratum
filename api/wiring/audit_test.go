package wiring

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/application"
	"github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

func TestBuildAudit_NilDB_ReturnsNil(t *testing.T) {
	a := buildAudit(nil, zap.NewNop())
	if a != nil {
		t.Fatal("buildAudit(nil) should return nil")
	}
}

func TestNewAuditCleanupWorker_Construction(t *testing.T) {
	logger := zap.NewNop()
	svc := application.NewAuditService(&stubAuditRepo{}, nil, logger)
	defer svc.Stop(context.Background())

	w := NewAuditCleanupWorker(svc, svc, logger)
	if w == nil {
		t.Fatal("cleanup worker is nil")
	}
	if w.interval != time.Duration(constants.AuditCleanupInterval)*time.Hour {
		t.Errorf("interval=%v, want %dh", w.interval, constants.AuditCleanupInterval)
	}
}

type stubAuditRepo struct{}

func (stubAuditRepo) InsertBatch(_ context.Context, _ []domain.AuditEvent) error { return nil }
func (stubAuditRepo) Query(_ context.Context, _ domain.AuditFilter) ([]domain.AuditEvent, error) {
	return nil, nil
}
func (stubAuditRepo) GetByID(_ context.Context, _, _ string) (*domain.AuditEvent, error) {
	return nil, nil
}
func (stubAuditRepo) DeleteOlderThan(_ context.Context, _ time.Time) error { return nil }
