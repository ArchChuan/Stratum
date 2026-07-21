// Package application exposes execution DTO aliases and checkpoint state contracts.

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

// CheckpointStore persists resumable execution checkpoints.
type CheckpointStore = port.CheckpointRepo
