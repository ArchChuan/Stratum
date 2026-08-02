// Package domain defines the collaboration bounded context types.
// Full implementation in Phase 2 (T6).
package domain

import (
	"encoding/json"
	"time"
)

// CollabStrategy is the plan generation strategy.
type CollabStrategy string

const (
	CollabSequential   CollabStrategy = "sequential"
	CollabParallel     CollabStrategy = "parallel"
	CollabSwarm        CollabStrategy = "swarm"
	CollabPipeline     CollabStrategy = "pipeline"
	CollabHierarchical CollabStrategy = "hierarchical"
)

// CollabStatus is the lifecycle of a collaboration.
type CollabStatus string

const (
	CollabCreated   CollabStatus = "created"
	CollabRunning   CollabStatus = "running"
	CollabCompleted CollabStatus = "completed"
	CollabFailed    CollabStatus = "failed"
	CollabCanceled  CollabStatus = "canceled"
)

// TaskStatus is the lifecycle of a single task step.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskClaimed   TaskStatus = "claimed"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
)

// Collaboration is the root aggregate for a multi-agent plan.
type Collaboration struct {
	ID              string         `json:"id"`
	TenantID        string         `json:"tenant_id"`
	TaskDescription string         `json:"task_description"`
	Strategy        CollabStrategy `json:"strategy"`
	Status          CollabStatus   `json:"status"`
	CreatedAt       time.Time      `json:"created_at"`
	StartedAt       *time.Time     `json:"started_at,omitempty"`
	CompletedAt     *time.Time     `json:"completed_at,omitempty"`
}

// TaskStep is a single agent task within a collaboration plan.
type TaskStep struct {
	ID             string         `json:"id"`
	PlanID         string         `json:"plan_id"`
	AgentID        string         `json:"agent_id"`
	Dependencies   []string       `json:"dependencies"`
	Status         TaskStatus     `json:"status"`
	Input          map[string]any `json:"input"`
	Output         map[string]any `json:"output"`
	Delegation     string         `json:"delegation"` // mirrors DelegationPolicy
	ClaimedBy      string         `json:"claimed_by"`
	LeaseExpiresAt *time.Time     `json:"lease_expires_at,omitempty"`
	RetryCount     int            `json:"retry_count"`
	MaxRetries     int            `json:"max_retries"`
	Error          string         `json:"error,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// SharedContext provides collaboration-global state with optimistic locking.
type SharedContext struct {
	PlanID  string          `json:"plan_id"`
	Data    json.RawMessage `json:"data"`
	Version int             `json:"version"`
}
