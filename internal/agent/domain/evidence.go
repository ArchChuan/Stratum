package domain

import (
	"errors"
	"time"
)

var (
	ErrEvidenceUnavailable = errors.New("trace evidence unavailable")
	ErrEvidenceNotFound    = errors.New("trace evidence not found")
	ErrEvidenceInvalid     = errors.New("trace evidence invalid")
)

type ResourceAssignment struct {
	RevisionID   string `json:"revision_id"`
	ExperimentID string `json:"experiment_id"`
	Variant      string `json:"variant"`
}

type TraceEvidence struct {
	OpikTraceID         string
	TraceID             string
	ExecutionID         string
	AgentID             string
	Status              string
	TotalTokens         int
	CostUSD             float64
	LatencyMs           int64
	SecurityViolation   bool
	ResourceAssignments map[string]ResourceAssignment
	Tools               []ToolObservation
	Events              []AgentTraceEvent
	StartedAt           time.Time
}
