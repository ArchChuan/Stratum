// Package application exposes the execution-history store contract
// consumed by handlers. Postgres adapter is in
// internal/agent/infrastructure/persistence (PgExecutionStore).

package application

import (
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// Type aliases keep handler/test source-compat after canonical hoisting.
type (
	ExecutionRecord = domain.ExecutionRecord
	ListOptions     = domain.ListOptions
)

// ExecutionStore is an alias for port.ExecutionRepo. Canonical definition
// lives in internal/agent/domain/port/repository.go.
type ExecutionStore = port.ExecutionRepo
