package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

type TraceEvidenceProvider interface {
	ListExecutions(context.Context, string, domain.ListOptions) ([]domain.ExecutionRecord, int64, error)
	ToolObservations(context.Context, string, string) ([]domain.ToolObservation, error)
	TraceEvents(context.Context, string, string) ([]domain.AgentTraceEvent, error)
	Resolve(context.Context, string, string) (domain.TraceEvidence, error)
	ResolveBatch(context.Context, string, []string) (map[string]domain.TraceEvidence, error)
}
