package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
)

type InvitationRepo interface {
	Create(ctx context.Context, invitation domain.TenantInvitation) error
	ConsumeAndJoin(ctx context.Context, in domain.InvitationJoinInput) (*domain.InvitationJoinResult, error)
	ConsumeAndJoinExisting(ctx context.Context, in domain.ExistingInvitationJoinInput) (*domain.InvitationJoinResult, error)
}
