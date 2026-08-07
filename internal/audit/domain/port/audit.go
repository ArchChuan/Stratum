package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/audit/domain"
)

// AuditRecorder accepts audit events asynchronously (fire-and-forget).
// Implementations must be safe for concurrent use and must never block
// the caller on a full buffer (drop or log-warn).
type AuditRecorder interface {
	Record(ctx context.Context, event domain.AuditEvent) error
}

// AuditQueryService reads audit events for human and automated review.
type AuditQueryService interface {
	Query(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error)
	// GetByID returns a single event, scoped to tenantID. tenantID must not be
	// empty: cross-tenant reads are refused by the implementation.
	GetByID(ctx context.Context, tenantID string, id string) (*domain.AuditEvent, error)
}
