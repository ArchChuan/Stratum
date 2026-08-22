package port

import (
	"context"
	"time"
)

// ExtractionTask represents a pending fact/entity extraction job. JSON tags
// keep the NATS transport payload stable across publish/consume.
type ExtractionTask struct {
	ID             int64     `json:"id,omitempty"`
	TenantID       string    `json:"tenant_id"`
	MessageID      string    `json:"message_id"`
	UserID         string    `json:"user_id"`
	AgentID        string    `json:"agent_id,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Scope          string    `json:"scope"`
	Content        string    `json:"content"` // JSON-encoded []MessageDTO
	Status         string    `json:"status,omitempty"`
	RetryCount     int       `json:"retry_count,omitempty"`
	TraceID        string    `json:"trace_id,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	UpdatedAt      time.Time `json:"updated_at,omitempty"`
}

// ExtractionQueue enqueues fact/entity extraction jobs. 任务传输层已收口到
// NATS JetStream（memory.extraction.{tenant}）；生命周期（重试/DLQ）由
// JetStream ack/MaxDeliver 与 memory.dlq 承载，不再由队列自管。
type ExtractionQueue interface {
	Enqueue(ctx context.Context, tenantID string, task *ExtractionTask) error
}
