// Package application exposes the execution-history store contract
// consumed by handlers. Postgres adapter is in
// internal/agent/infrastructure/persistence (PgExecutionStore).

package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// Type aliases keep handler/test source-compat after canonical hoisting.
type (
	ExecutionRecord = domain.ExecutionRecord
	ListOptions     = domain.ListOptions
)

// ExecutionStore persists and retrieves agent execution history.
type ExecutionStore interface {
	Insert(ctx context.Context, r ExecutionRecord) error
	List(ctx context.Context, opts ListOptions) ([]ExecutionRecord, int64, error)
}
