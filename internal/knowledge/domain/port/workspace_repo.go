package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// WorkspaceRepo persists per-tenant knowledge workspaces.
// Write methods take a change-audit event; a nil event skips auditing and is
// reserved for internal reentrant paths (e.g. rollback after collection
// provision failure).
type WorkspaceRepo interface {
	Create(ctx context.Context, tenantID string, ws *domain.Workspace, audit *auditdomain.ResourceChangeAuditEvent) error
	GetByName(ctx context.Context, tenantID, name string) (*domain.Workspace, error)
	GetByID(ctx context.Context, tenantID, id string) (*domain.Workspace, error)
	List(ctx context.Context, tenantID string) ([]*domain.Workspace, error)
	// UpdateWorkspaceAll applies rename, description and config atomically in
	// one transaction with the audit row. renameTo/description may be nil.
	UpdateWorkspaceAll(ctx context.Context, tenantID, name string, renameTo, description *string, cfg domain.WorkspaceConfig, audit *auditdomain.ResourceChangeAuditEvent) error
	Delete(ctx context.Context, tenantID, name string, audit *auditdomain.ResourceChangeAuditEvent) error
	GetConfigForUpload(ctx context.Context, tenantID, name string) (domain.WorkspaceConfig, error)
	GetConfigByID(ctx context.Context, tenantID, id string) (domain.WorkspaceConfig, error)
}
