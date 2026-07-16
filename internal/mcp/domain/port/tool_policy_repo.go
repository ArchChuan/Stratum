package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

type ToolPolicyRepo interface {
	Get(ctx context.Context, serverID, toolName string) (domain.ToolPolicy, bool, error)
	List(ctx context.Context) ([]domain.ToolPolicy, error)
	Upsert(ctx context.Context, policy domain.ToolPolicy) error
}
