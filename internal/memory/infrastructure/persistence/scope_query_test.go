package persistence

import (
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestSupersedeScopeClauseSeparatesUserAndAgentOwnership(t *testing.T) {
	user := supersedeScopeClause(domain.ScopeFilter{IncludeUserScope: true})
	agent := supersedeScopeClause(domain.ScopeFilter{AgentID: "agent-a", IncludeAgentScope: true})
	if !strings.Contains(user, "scope = 'user'") || strings.Contains(user, "agent_id") {
		t.Fatalf("user clause crosses ownership: %q", user)
	}
	if !strings.Contains(agent, "scope = 'agent'") || !strings.Contains(agent, "agent_id = $3") || strings.Contains(agent, "scope = 'user'") {
		t.Fatalf("agent clause crosses ownership: %q", agent)
	}
}

func TestEntityScopeClauseSeparatesUserAndAgentOwnership(t *testing.T) {
	user := entityScopeClause(domain.ScopeFilter{IncludeUserScope: true})
	agent := entityScopeClause(domain.ScopeFilter{AgentID: "agent-a", IncludeAgentScope: true})
	if user != "scope = 'user'" {
		t.Fatalf("user clause = %q", user)
	}
	if agent != "scope = 'agent' AND agent_id = $5" {
		t.Fatalf("agent clause = %q", agent)
	}
}
