package domain

import "testing"

func TestAgentRevisionContentHashIsDeterministicAcrossBindingOrder(t *testing.T) {
	left := AgentRevision{
		AgentID: "agent-1", Type: ReActAgent, SystemPrompt: "be precise", Model: "qwen-plus", MaxIterations: 8,
		ModelParameters: ModelParameters{MaxContextTokens: 2048},
		Bindings: []AgentBinding{
			{Kind: AgentBindingSkill, ID: "skill-b", Enabled: true},
			{Kind: AgentBindingMCP, ID: "mcp:server:tool", Enabled: false},
			{Kind: AgentBindingKnowledge, ID: "workspace-a", Enabled: true},
		},
	}
	right := left
	right.Bindings = []AgentBinding{left.Bindings[2], left.Bindings[0], left.Bindings[1]}

	leftHash, err := left.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := right.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("binding order changed content hash: %s != %s", leftHash, rightHash)
	}
}

func TestAgentRevisionApplyCandidateRejectsNewBindingsAndAllowsExistingEnablement(t *testing.T) {
	baseline := AgentRevision{
		AgentID: "agent-1", Type: ReActAgent, SystemPrompt: "baseline", Model: "qwen-plus", MaxIterations: 5,
		Bindings: []AgentBinding{{Kind: AgentBindingSkill, ID: "skill-1", Enabled: true}},
	}
	if _, err := baseline.ApplyCandidate(AgentCandidatePatch{
		Bindings: []AgentBinding{{Kind: AgentBindingSkill, ID: "skill-2", Enabled: true}},
	}); err == nil {
		t.Fatal("expected new binding to be rejected")
	}

	candidate, err := baseline.ApplyCandidate(AgentCandidatePatch{
		SystemPrompt: "candidate", MaxIterations: 7,
		Bindings: []AgentBinding{{Kind: AgentBindingSkill, ID: "skill-1", Enabled: false}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.SystemPrompt != "candidate" || candidate.MaxIterations != 7 || candidate.Bindings[0].Enabled {
		t.Fatalf("unexpected candidate: %#v", candidate)
	}
}

func TestAgentRevisionApplyCandidateRejectsPermissionWidening(t *testing.T) {
	baseline := AgentRevision{AgentID: "agent-1", Type: ReActAgent, SystemPrompt: "baseline", Model: "qwen-plus", MaxIterations: 5}
	if _, err := baseline.ApplyCandidate(AgentCandidatePatch{Permissions: []string{"network"}}); err == nil {
		t.Fatal("expected permission widening to be rejected")
	}
}

func TestAgentRevisionApplyCandidateRejectsNoOp(t *testing.T) {
	baseline := AgentRevision{AgentID: "agent-1", Type: ReActAgent, SystemPrompt: "baseline", Model: "qwen-plus", MaxIterations: 5}
	if _, err := baseline.ApplyCandidate(AgentCandidatePatch{}); err == nil {
		t.Fatal("expected empty candidate to be rejected")
	}
}

func TestAgentRevisionValidateRequiresExplicitAgentType(t *testing.T) {
	revision := AgentRevision{AgentID: "agent-1", SystemPrompt: "baseline", Model: "qwen-plus", MaxIterations: 5}
	if err := revision.Validate(); err == nil {
		t.Fatal("expected missing agent type to be rejected")
	}
}
