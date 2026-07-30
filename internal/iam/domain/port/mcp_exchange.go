package port

import (
	"context"
	"time"
)

type PlatformMCPBinding struct {
	AgentSystemKey       string
	ServerSystemKey      string
	ServerManagementMode string
	Bound                bool
}

type TenantRoleResolver interface {
	CurrentRole(ctx context.Context, tenantID, userID string) (string, error)
}

type PlatformMCPBindingReader interface {
	ReadPlatformMCPBinding(
		ctx context.Context,
		tenantID, agentID, serverID, toolName string,
	) (PlatformMCPBinding, error)
}

type PlatformMCPApprovalReader interface {
	ValidatePlatformMCPApproval(
		ctx context.Context,
		tenantID, approvalID, toolName, resourceID string,
	) error
}

type InvocationReplayStore interface {
	ConsumeInvocationJTI(ctx context.Context, tenantID, jti string, expiresAt time.Time) (bool, error)
}
