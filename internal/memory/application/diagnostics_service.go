package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

// DiagnosticsService aggregates memory system metrics for admin monitoring.
type DiagnosticsService struct {
	factRepo   port.FactRepo
	entityRepo port.EntityRepo
	queue      port.ExtractionQueue
}

// NewDiagnosticsService creates a DiagnosticsService.
func NewDiagnosticsService(factRepo port.FactRepo, entityRepo port.EntityRepo, queue port.ExtractionQueue) *DiagnosticsService {
	return &DiagnosticsService{
		factRepo:   factRepo,
		entityRepo: entityRepo,
		queue:      queue,
	}
}

// Diagnostics holds memory system metrics.
type Diagnostics struct {
	ActiveFactCount   int           `json:"active_fact_count"`
	SupersededCount   int           `json:"superseded_count"`
	QueueLag          int           `json:"queue_lag"`
	TopEntities       []EntityCount `json:"top_entities"`
	FrecencyHistogram []int         `json:"frecency_histogram"`
}

// EntityCount holds entity name and fact count.
type EntityCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// GetDiagnostics aggregates memory metrics for a tenant.
func (s *DiagnosticsService) GetDiagnostics(ctx context.Context, tenantID string) (*Diagnostics, error) {
	// Count active facts (not superseded)
	activeFacts, err := s.factRepo.CountActive(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Count superseded facts
	supersededFacts, err := s.factRepo.CountSuperseded(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Get queue lag (pending messages)
	queueLag, err := s.queue.PendingCount(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// Get top entities by fact count
	topEntities, err := s.entityRepo.TopByFactCount(ctx, tenantID, 10)
	if err != nil {
		return nil, err
	}

	entities := make([]EntityCount, len(topEntities))
	for i, e := range topEntities {
		entities[i] = EntityCount{Name: e.Name, Count: e.Count}
	}

	// Frecency histogram (stub for now - would need custom query)
	frecencyHistogram := []int{0, 0, 0, 0, 0}

	return &Diagnostics{
		ActiveFactCount:   activeFacts,
		SupersededCount:   supersededFacts,
		QueueLag:          queueLag,
		TopEntities:       entities,
		FrecencyHistogram: frecencyHistogram,
	}, nil
}
