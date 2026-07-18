package persistence

import (
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestSupersedeQueryUsesContinuousUserScopeParameters(t *testing.T) {
	query, args := supersedeQuery(domain.ScopeFilter{UserID: "user-1", IncludeUserScope: true}, "content", 0.6, 10)
	if strings.Contains(query, "$5") || !strings.Contains(query, "similarity(content, $2) > $3") || !strings.Contains(query, "LIMIT $4") {
		t.Fatalf("user query has non-contiguous placeholders: %s", query)
	}
	if len(args) != 4 || args[0] != "user-1" || args[1] != "content" || args[2] != 0.6 || args[3] != 10 {
		t.Fatalf("user args = %#v, want user/content/threshold/limit", args)
	}
}

func TestSupersedeQueryUsesAgentOwnershipParameter(t *testing.T) {
	filter := domain.ScopeFilter{UserID: "user-1", AgentID: "agent-1", IncludeAgentScope: true}
	query, args := supersedeQuery(filter, "content", 0.6, 10)
	if !strings.Contains(query, "agent_id = $3") || !strings.Contains(query, "similarity(content, $2) > $4") || !strings.Contains(query, "LIMIT $5") {
		t.Fatalf("agent query lost ownership parameter: %s", query)
	}
	if len(args) != 5 || args[2] != "agent-1" || args[3] != 0.6 || args[4] != 10 {
		t.Fatalf("agent args = %#v, want user/content/agent/threshold/limit", args)
	}
}

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
