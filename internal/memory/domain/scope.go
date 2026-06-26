package domain

import "fmt"

// Scope represents memory scope (user-level or agent-private)
type Scope string

const (
	ScopeUser  Scope = "user"
	ScopeAgent Scope = "agent"
)

// ValidateScope checks if the scope string is valid
func ValidateScope(s string) error {
	if s != string(ScopeUser) && s != string(ScopeAgent) {
		return fmt.Errorf("invalid scope %q: must be user or agent", s)
	}
	return nil
}

// ScopeFilter encapsulates scope filtering logic for queries
type ScopeFilter struct {
	TenantID          string
	UserID            string
	AgentID           string
	IncludeUserScope  bool
	IncludeAgentScope bool
}

// BuildScopeFilter constructs a filter based on read_scope configuration
// readScope: "user" sees user-scoped and agent-scoped memories for the user
// readScope: "agent" sees only this agent's private memories
func BuildScopeFilter(tenantID, userID, agentID, readScope string) ScopeFilter {
	filter := ScopeFilter{
		TenantID: tenantID,
		UserID:   userID,
		AgentID:  agentID,
	}
	switch readScope {
	case "user":
		filter.IncludeUserScope = true
		filter.IncludeAgentScope = true
	case "agent":
		filter.IncludeUserScope = false
		filter.IncludeAgentScope = true
	}
	return filter
}
