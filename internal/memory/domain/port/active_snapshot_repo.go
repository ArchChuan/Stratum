package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

type ActiveSnapshotRepo interface {
	Upsert(ctx context.Context, snapshot *domain.ActiveSnapshot) error
	Get(ctx context.Context, tenantID, userID, agentID string) (*domain.ActiveSnapshot, error)
	Delete(ctx context.Context, tenantID, userID, agentID string) error
}
