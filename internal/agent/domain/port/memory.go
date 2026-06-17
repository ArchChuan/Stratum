package port

import "context"

type MemoryRecaller interface {
	Recall(ctx context.Context, tenantID, userID, query string, limit int) ([]string, error)
}

type MemoryWriter interface {
	Write(ctx context.Context, tenantID, userID, content string, importance float32) error
}
