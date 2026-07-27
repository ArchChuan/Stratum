package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProposalTransitionTable(t *testing.T) {
	cases := []struct {
		from, to ProposalStatus
		allowed  bool
	}{
		{StatusDraft, StatusReadyForReview, true},
		{StatusReadyForReview, StatusConfirmed, true},
		{StatusConfirmed, StatusApplying, true},
		{StatusApplying, StatusApplied, true},
		{StatusReadyForReview, StatusStale, true},
		{StatusApplying, StatusUnknownOutcome, true},
		{StatusDraft, StatusCancelled, true},
		{StatusReadyForReview, StatusCancelled, true},
		{StatusApplied, StatusApplying, false},
		{StatusStale, StatusConfirmed, false},
		{StatusUnknownOutcome, StatusApplying, false},
		{StatusCancelled, StatusReadyForReview, false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.allowed, CanTransition(tc.from, tc.to), "%s -> %s", tc.from, tc.to)
	}
}

func TestResourceChangeProposalValidateEnvelope(t *testing.T) {
	now := time.Now().UTC()
	validCreate := ResourceChangeProposal{
		ID: "proposal-1", TenantID: "tenant-1", ProposerID: "user-1",
		ResourceKind: ResourceAgent, Operation: OperationCreate,
		Payload: json.RawMessage(`{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`),
		Status:  StatusDraft, ExpiresAt: now.Add(time.Hour),
	}
	require.NoError(t, validCreate.Validate(now))

	tests := []struct {
		name   string
		mutate func(*ResourceChangeProposal)
	}{
		{"expired", func(p *ResourceChangeProposal) { p.ExpiresAt = now.Add(-time.Second) }},
		{"create with resource id", func(p *ResourceChangeProposal) { p.ResourceID = "agent-1" }},
		{"update without resource id", func(p *ResourceChangeProposal) { p.Operation = OperationUpdate }},
		{"update without baseline", func(p *ResourceChangeProposal) { p.Operation = OperationUpdate; p.ResourceID = "agent-1" }},
		{"prohibited operation", func(p *ResourceChangeProposal) { p.Operation = ProposalOperation("delete") }},
		{"unsupported resource", func(p *ResourceChangeProposal) { p.ResourceKind = ResourceKind("workflow") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			proposal := validCreate
			tc.mutate(&proposal)
			require.Error(t, proposal.Validate(now))
		})
	}

	validUpdate := validCreate
	validUpdate.Operation = OperationUpdate
	validUpdate.ResourceID = "agent-1"
	validUpdate.BaselineFingerprint = "sha256:baseline"
	require.NoError(t, validUpdate.Validate(now))
}

func TestDecodeProposalPayloadStrictly(t *testing.T) {
	tests := []struct {
		name      string
		kind      ResourceKind
		operation ProposalOperation
		payload   string
		wantErr   bool
	}{
		{"agent", ResourceAgent, OperationCreate, `{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096}`, false},
		{"skill draft", ResourceSkillDraft, OperationUpdate, `{"name":"skill","description":"desc","instructions":"do work","temperature":0.3}`, false},
		{"mcp", ResourceMCPConfig, OperationCreate, `{"name":"docs","version":"1","transport":"streamable_http","url":"https://example.test/mcp","timeoutSec":30}`, false},
		{"knowledge", ResourceKnowledgeWorkspace, OperationCreate, `{"name":"docs","description":"official docs","embeddingModel":"text-embedding-v3"}`, false},
		{"unknown field", ResourceAgent, OperationCreate, `{"name":"agent","description":"desc","model":"qwen-plus","maxIterations":5,"maxContextTokens":4096,"systemPrompt":"override"}`, true},
		{"secret env", ResourceMCPConfig, OperationCreate, `{"name":"docs","version":"1","transport":"stdio","command":"server","timeoutSec":30,"env":{"TOKEN":"secret"}}`, true},
		{"secret headers", ResourceMCPConfig, OperationUpdate, `{"name":"docs","version":"1","transport":"streamable_http","url":"https://example.test/mcp","timeoutSec":30,"headers":{"Authorization":"Bearer secret"}}`, true},
		{"trailing object", ResourceKnowledgeWorkspace, OperationCreate, `{"name":"docs","description":"official docs","embeddingModel":"embed"}{}`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeProposalPayload(tc.kind, tc.operation, json.RawMessage(tc.payload))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
