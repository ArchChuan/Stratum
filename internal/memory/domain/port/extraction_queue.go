package port

import (
	"context"
	"time"
)

// ExtractionTask represents a pending fact/entity extraction job.
type ExtractionTask struct {
	ID             int64
	TenantID       string
	MessageID      string
	UserID         string
	AgentID        string
	ConversationID string
	Scope          string
	Content        string // JSON-encoded []MessageDTO
	Status         string
	RetryCount     int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ExtractionQueue manages async extraction task lifecycle.
type ExtractionQueue interface {
	Enqueue(ctx context.Context, tenantID string, task *ExtractionTask) error
	Dequeue(ctx context.Context, tenantID string) (*ExtractionTask, error)
	MarkCompleted(ctx context.Context, tenantID string, taskID int64) error
	MarkFailed(ctx context.Context, tenantID string, taskID int64, errMsg string) error
	DeleteOldCompleted(ctx context.Context, tenantID string, retentionDays int) (int, error)
}
