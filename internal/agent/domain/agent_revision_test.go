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

func TestAgentRevisionRejectsUnboundedContextTokens(t *testing.T) {
	revision := AgentRevision{AgentID: "agent-1", Type: ReActAgent, SystemPrompt: "baseline",
		Model: "qwen-plus", MaxIterations: 5, ModelParameters: ModelParameters{MaxContextTokens: 1_000_001}}
	if err := revision.Validate(); err == nil {
		t.Fatal("expected excessive context token limit to be rejected")
	}
}

func TestAgentRevisionValidatesModelParameters(t *testing.T) {
	tests := []struct {
		name    string
		params  ModelParameters
		wantErr bool
	}{
		{name: "zero values are unset", params: ModelParameters{}},
		{name: "temperature bounds inclusive", params: ModelParameters{Temperature: 0, MaxTokens: 0}},
		{name: "temperature at max", params: ModelParameters{Temperature: 2, MaxTokens: 0}},
		{name: "max_tokens at max", params: ModelParameters{MaxTokens: 131072}},
		{name: "temperature below min rejected", params: ModelParameters{Temperature: -0.1}, wantErr: true},
		{name: "temperature above max rejected", params: ModelParameters{Temperature: 2.1}, wantErr: true},
		{name: "max_tokens below zero rejected", params: ModelParameters{MaxTokens: -1}, wantErr: true},
		{name: "max_tokens above max rejected", params: ModelParameters{MaxTokens: 131073}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			revision := AgentRevision{AgentID: "agent-1", Type: ReActAgent, SystemPrompt: "baseline",
				Model: "qwen-plus", MaxIterations: 5, ModelParameters: tc.params}
			err := revision.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestAgentRevisionContentHashStableWhenParametersUnsetAndChangesWhenSet(t *testing.T) {
	base := AgentRevision{AgentID: "agent-1", Type: ReActAgent, SystemPrompt: "be precise",
		Model: "qwen-plus", MaxIterations: 8, ModelParameters: ModelParameters{MaxContextTokens: 2048}}
	baseHash, err := base.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	// Zero-valued new fields must not change the hash of an existing revision.
	withZeroFields := base
	withZeroFields.ModelParameters.Temperature = 0
	withZeroFields.ModelParameters.MaxTokens = 0
	zeroHash, err := withZeroFields.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if baseHash != zeroHash {
		t.Fatalf("zero-valued parameters changed content hash: %s != %s", baseHash, zeroHash)
	}
	// Setting values must change the hash, deterministically across rounds.
	withValues := base
	withValues.ModelParameters.Temperature = 0.9
	withValues.ModelParameters.MaxTokens = 2048
	valueHash1, err := withValues.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	valueHash2, err := withValues.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if valueHash1 != valueHash2 {
		t.Fatalf("content hash not deterministic: %s != %s", valueHash1, valueHash2)
	}
	if valueHash1 == baseHash {
		t.Fatal("setting temperature/max_tokens must change content hash")
	}
}
