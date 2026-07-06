package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// WorkspaceRepo persists per-tenant knowledge workspaces.
type WorkspaceRepo interface {
	Create(ctx context.Context, tenantID string, ws *domain.Workspace) error
	GetByName(ctx context.Context, tenantID, name string) (*domain.Workspace, error)
	List(ctx context.Context, tenantID string) ([]*domain.Workspace, error)
	UpdateDescriptionAndConfig(ctx context.Context, tenantID, name string, description *string, cfg domain.WorkspaceConfig) error
	UpdateName(ctx context.Context, tenantID, oldName, newName string) error
	Delete(ctx context.Context, tenantID, name string) error
	GetConfigForUpload(ctx context.Context, tenantID, name string) (domain.WorkspaceConfig, error)
	GetConfigByID(ctx context.Context, tenantID, id string) (domain.WorkspaceConfig, error)
}
