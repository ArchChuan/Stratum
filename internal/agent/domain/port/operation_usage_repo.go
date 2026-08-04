package port

import (
	"context"
	"time"
)

// DailyOperationUsage aggregates a gated operation's consumption for one day.
type DailyOperationUsage struct {
	CostUSD    float64
	Executions int
}

// OperationUsageRepo persists per-day usage counters for gated operations.
// Rows are upserted per (agent, op_type, usage_date).
type OperationUsageRepo interface {
	AddUsage(ctx context.Context, tenantID, agentID string, opType OperationType, day time.Time, costUSD float64, executions int) error
	DailyUsage(ctx context.Context, tenantID, agentID string, opType OperationType, day time.Time) (DailyOperationUsage, error)
}
