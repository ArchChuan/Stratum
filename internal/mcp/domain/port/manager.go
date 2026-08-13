package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain"
)

// ServerManager is the consumer-side port for MCP server lifecycle operations.
//
// Connect/UpdateServer/Delete take an optional *auditdomain.ResourceChangeAuditEvent;
// when non-nil the audit row is inserted in the SAME transaction as the
// business write (audit failure rolls the change back). Callers must always
// construct the event on user-facing write paths; nil is reserved for
// internal reentrant paths (restore/reconnect).
//
// editors (Connect) carries the granted editor set on the create path and is
// written in the same transaction as the config row; editorActor (Connect and
// UpdateServer) is non-empty when a granted editor performs the update — the
// transaction then re-validates the actor's role and editor membership,
// closing the check-then-write TOCTOU window.
type ServerManager interface {
	Connect(ctx context.Context, cfg *domain.ServerConfig, editors []string, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error
	Disconnect(ctx context.Context, serverID string) error
	Reconnect(ctx context.Context, serverID string) error
	UpdateServer(ctx context.Context, cfg *domain.ServerConfig, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error
	Delete(ctx context.Context, serverID string, audit *auditdomain.ResourceChangeAuditEvent) error
	GetServerConfig(ctx context.Context, serverID string) (*domain.ServerConfig, error)
	// ListPlatformManagedServerIDs 返回平台托管(platform_managed)server ID 全集。
	// 谓词对齐 isPlatformManaged(system_key != '' OR management_mode='platform_managed');
	// 供 wiring 的 SystemResourceGuard 做批量净化与挂载校验,避免逐 server 查询。
	ListPlatformManagedServerIDs(ctx context.Context) ([]string, error)
	ListTools(ctx context.Context, serverID string) ([]*domain.Tool, error)
	ListResources(ctx context.Context, serverID string) ([]*domain.Resource, error)
	GetServerInfo(ctx context.Context, serverID string) *domain.ServerInfo
	GetAllServerInfo(ctx context.Context) []*domain.ServerInfo
	// ListEditors returns the editor ids of a server config, or an empty slice.
	ListEditors(ctx context.Context, tenantID, serverID string) ([]string, error)
	// ReplaceEditors atomically swaps the editor set. Each editor must hold
	// role admin or owner at write time (checked inside the transaction,
	// fail closed); a non-eligible id returns domain.ErrEditorNotEligible.
	// audit, when non-nil, is written in the SAME transaction (audit failure
	// rolls the editor change back).
	ReplaceEditors(ctx context.Context, tenantID, serverID string, editorIDs []string, createdBy string, audit *auditdomain.ResourceChangeAuditEvent) error
	// RemoveTenant disconnects all connections belonging to tenantID.
	RemoveTenant(ctx context.Context, tenantID string) error
	// Quota returns connection accounting for the tenant derived from ctx.
	Quota(ctx context.Context) domain.Quota
}

// ToolRegistry registers live MCP tools discovered from a server. tenantID is
// required: mcp_configs.id is unique only within a tenant schema, and registry
// entries are keyed by tenantID:serverID to prevent cross-tenant collisions.
type ToolRegistry interface {
	RegisterServer(ctx context.Context, tenantID, serverID string) error
	UnregisterServer(tenantID, serverID string) error
}
