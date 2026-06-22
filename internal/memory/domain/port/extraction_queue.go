package port

import (
	"context"
	"time"
)

// ExtractionTask represents a pending fact/entity extraction job.
type ExtractionTask struct {
	ID         int64
	MessageID  string
	UserID     string
	AgentID    string
	Content    string
	Status     string // pending/processing/completed/failed
	RetryCount int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ExtractionQueue manages async extraction task lifecycle.
type ExtractionQueue interface {
	// Enqueue adds a new extraction task.
	Enqueue(ctx context.Context, task *ExtractionTask) error

	// Dequeue fetches the next pending task (FIFO + retries).
	Dequeue(ctx context.Context) (*ExtractionTask, error)

	// MarkCompleted marks a task as successfully processed.
	MarkCompleted(ctx context.Context, taskID int64) error

	// MarkFailed increments retry count or marks permanently failed.
	MarkFailed(ctx context.Context, taskID int64, errMsg string) error

	// PendingCount returns count of pending tasks for a tenant.
	PendingCount(ctx context.Context, tenantID string) (int, error)

	// DeleteOldCompleted removes completed tasks older than retention days.
	DeleteOldCompleted(ctx context.Context, retentionDays int) (int, error)
}
