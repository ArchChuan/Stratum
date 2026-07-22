package port

import "context"

// ToolUserScope is the user-specific authorization boundary. It may only
// narrow permissions already granted by the tenant, Agent, and active Skill.
type ToolUserScope struct {
	UserActive bool
	AllowsTool bool
}

type ToolUserScopeResolver interface {
	ResolveToolUserScope(
		ctx context.Context,
		tenantID, userID, agentID, toolID string,
	) (ToolUserScope, error)
}
