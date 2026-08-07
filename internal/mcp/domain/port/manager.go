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
type ServerManager interface {
	Connect(ctx context.Context, cfg *domain.ServerConfig, audit *auditdomain.ResourceChangeAuditEvent) error
	Disconnect(ctx context.Context, serverID string) error
	Reconnect(ctx context.Context, serverID string) error
	UpdateServer(ctx context.Context, cfg *domain.ServerConfig, audit *auditdomain.ResourceChangeAuditEvent) error
	Delete(ctx context.Context, serverID string, audit *auditdomain.ResourceChangeAuditEvent) error
	GetServerConfig(ctx context.Context, serverID string) (*domain.ServerConfig, error)
	ListTools(ctx context.Context, serverID string) ([]*domain.Tool, error)
	ListResources(ctx context.Context, serverID string) ([]*domain.Resource, error)
	GetServerInfo(ctx context.Context, serverID string) *domain.ServerInfo
	GetAllServerInfo(ctx context.Context) []*domain.ServerInfo
	// RemoveTenant disconnects all connections belonging to tenantID.
	RemoveTenant(ctx context.Context, tenantID string) error
	// Quota returns connection accounting for the tenant derived from ctx.
	Quota(ctx context.Context) domain.Quota
}

// ToolRegistry registers live MCP tools discovered from a server.
type ToolRegistry interface {
	RegisterServer(ctx context.Context, serverID string) error
	UnregisterServer(serverID string) error
}
