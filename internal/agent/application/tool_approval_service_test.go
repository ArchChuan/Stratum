package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/stretchr/testify/require"
)

type approvalRepoFake struct {
	row                              domain.ToolApproval
	createErr, decideErr, executeErr error
	released, outcomeUnknown         int
}

func (f *approvalRepoFake) Create(_ context.Context, _ string, row domain.ToolApproval) (string, error) {
	f.row = row
	if f.createErr != nil {
		return "", f.createErr
	}
	return "approval-1", nil
}
func (f *approvalRepoFake) Get(_ context.Context, _, _ string) (domain.ToolApproval, error) {
	return f.row, nil
}
func (f *approvalRepoFake) Decide(_ context.Context, _, _, _, _, _ string, _ time.Time) error {
	return f.decideErr
}
func (f *approvalRepoFake) MarkExecuted(_ context.Context, _, _ string) error   { return f.executeErr }
func (f *approvalRepoFake) ClaimExecution(_ context.Context, _, _ string) error { return f.executeErr }
func (f *approvalRepoFake) ReleaseExecution(_ context.Context, _, _ string) error {
	f.released++
	return nil
}
func (f *approvalRepoFake) MarkOutcomeUnknown(_ context.Context, _, _ string) error {
	f.outcomeUnknown++
	return nil
}
func (f *approvalRepoFake) ListPending(_ context.Context, _ string) ([]domain.ToolApproval, error) {
	return []domain.ToolApproval{f.row}, nil
}

func TestToolApprovalServiceEncryptsPayloadAndCreatesSafeCheckpoint(t *testing.T) {
	repo := &approvalRepoFake{}
	checkpoints := &checkpointFake{}
	svc := NewToolApprovalService(repo, checkpoints, crypto.DeriveAESKey("test-key"))
	id, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Query: "delete order", Arguments: map[string]any{"secret": "do-not-store-plain"},
	})
	require.NoError(t, err)
	require.Equal(t, "approval-1", id)
	require.NotContains(t, repo.row.EncryptedPayload, "do-not-store-plain")
	require.Equal(t, "waiting_approval", checkpoints.row.Status)
	require.JSONEq(t, `{"approval_id":"approval-1"}`, string(checkpoints.row.RuntimeStateJSON))
	require.NotContains(t, string(checkpoints.row.PendingToolCallsJSON), "do-not-store-plain")
	require.NotEmpty(t, repo.row.DecisionID)
	require.Contains(t, repo.row.ArgumentsDigest, "tool-arguments:v1:sha256:")
	require.Contains(t, repo.row.SkillRevisionsDigest, "skill-revisions:v1:sha256:")
}

func TestToolApprovalServiceRejectsTamperedBinding(t *testing.T) {
	key := crypto.DeriveAESKey("test-key")
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, key)
	payload := ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"order_id": "order-1"}, PinnedSkillRevisions: map[string]string{"skill-1": "revision-1"},
		PolicyVersion: "policy-1",
	}
	if _, err := svc.Request(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	repo.row.ID = "approval-1"
	repo.row.Status = "approved"
	repo.row.ExpiresAt = time.Now().Add(time.Minute)

	tests := []struct {
		name   string
		mutate func(*domain.ToolApproval)
	}{
		{name: "decision", mutate: func(row *domain.ToolApproval) { row.DecisionID = "other" }},
		{name: "execution", mutate: func(row *domain.ToolApproval) { row.ExecutionID = "other" }},
		{name: "agent", mutate: func(row *domain.ToolApproval) { row.AgentID = "other" }},
		{name: "user", mutate: func(row *domain.ToolApproval) { row.UserID = "other" }},
		{name: "tool call", mutate: func(row *domain.ToolApproval) { row.ToolCallID = "other" }},
		{name: "server", mutate: func(row *domain.ToolApproval) { row.ServerID = "other" }},
		{name: "tool", mutate: func(row *domain.ToolApproval) { row.ToolName = "other" }},
		{name: "arguments", mutate: func(row *domain.ToolApproval) { row.ArgumentsDigest = "other" }},
		{name: "skill revisions", mutate: func(row *domain.ToolApproval) { row.SkillRevisionsDigest = "other" }},
		{name: "policy", mutate: func(row *domain.ToolApproval) { row.PolicyVersion = "other" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := repo.row
			t.Cleanup(func() { repo.row = original })
			tt.mutate(&repo.row)

			_, err := svc.ApprovedPayload(context.Background(), "tenant-1", "approval-1")
			require.ErrorIs(t, err, ErrApprovalBindingMismatch)
		})
	}
}

func TestToolApprovalServiceDecryptsApprovedPayload(t *testing.T) {
	key := crypto.DeriveAESKey("test-key")
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, key)
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "get", RiskLevel: port.ToolRiskUnclassified,
		Query: "resume", Arguments: map[string]any{"id": "o1"},
	})
	require.NoError(t, err)
	repo.row.ID = "approval-1"
	repo.row.Status = "approved"
	repo.row.ExpiresAt = time.Now().Add(time.Minute)

	payload, err := svc.ApprovedPayload(context.Background(), "tenant-1", "approval-1")
	require.NoError(t, err)
	require.Equal(t, "resume", payload.Query)
	require.Equal(t, "o1", payload.Arguments["id"])
}

func TestToolApprovalServiceRejectsExpiredApproval(t *testing.T) {
	repo := &approvalRepoFake{row: domain.ToolApproval{ID: "approval-1", Status: "approved", ExpiresAt: time.Now().Add(-time.Minute)}}
	_, err := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("key")).ApprovedPayload(context.Background(), "tenant-1", "approval-1")
	require.ErrorIs(t, err, ErrApprovalExpired)
}

type failingMCPExecutor struct {
	err error
}

func (e failingMCPExecutor) ExecuteMCPTool(context.Context, string, string, map[string]any) (any, error) {
	return nil, e.err
}

func TestToolApprovalExecutionClassifiesUnknownOutcome(t *testing.T) {
	repo, svc, payload := approvedToolApprovalFixture(t)
	execErr := errors.New("response timed out after request dispatch")

	_, err := svc.ExecuteApproved(
		context.Background(), "tenant-1", "approval-1", "orders", "delete", payload.Arguments,
		failingMCPExecutor{err: &port.MCPToolExecutionError{Outcome: port.ToolExecutionOutcomeUnknown, Err: execErr}},
	)

	require.ErrorIs(t, err, execErr)
	require.Equal(t, 1, repo.outcomeUnknown)
	require.Zero(t, repo.released)
}

func TestToolApprovalExecutionReleasesOnlyDefinitePreExecutionFailure(t *testing.T) {
	repo, svc, payload := approvedToolApprovalFixture(t)
	execErr := errors.New("client not found")

	_, err := svc.ExecuteApproved(
		context.Background(), "tenant-1", "approval-1", "orders", "delete", payload.Arguments,
		failingMCPExecutor{err: &port.MCPToolExecutionError{Outcome: port.ToolExecutionOutcomeNotSent, Err: execErr}},
	)

	require.ErrorIs(t, err, execErr)
	require.Equal(t, 1, repo.released)
	require.Zero(t, repo.outcomeUnknown)
}

func approvedToolApprovalFixture(t *testing.T) (*approvalRepoFake, *ToolApprovalService, ToolApprovalPayload) {
	t.Helper()
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	payload := ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"id": "order-1"},
	}
	_, err := svc.Request(context.Background(), payload)
	require.NoError(t, err)
	repo.row.ID = "approval-1"
	repo.row.Status = "approved"
	repo.row.ExpiresAt = time.Now().Add(time.Minute)
	return repo, svc, payload
}

func TestApprovedToolResumeErrorRequiresPinnedCallToBeConsumed(t *testing.T) {
	require.ErrorIs(t, approvedToolResumeError(false, nil), ErrApprovedToolNotReplayed)
	require.NoError(t, approvedToolResumeError(true, nil))

	runErr := errors.New("agent failed")
	require.ErrorIs(t, approvedToolResumeError(false, runErr), runErr)
}

type checkpointFake struct {
	row domain.AgentExecutionCheckpoint
}

func (f *checkpointFake) Upsert(_ context.Context, _ string, row domain.AgentExecutionCheckpoint) error {
	f.row = row
	return nil
}
func (f *checkpointFake) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, errors.New("unused")
}
func (f *checkpointFake) MarkCompleted(context.Context, string, string) error { return nil }
