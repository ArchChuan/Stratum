package application

import (
	"context"
	"encoding/json"
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
func (f *approvalRepoFake) MarkExecuted(_ context.Context, _, _ string) error     { return f.executeErr }
func (f *approvalRepoFake) ClaimExecution(_ context.Context, _, _ string) error   { return f.executeErr }
func (f *approvalRepoFake) ReleaseExecution(_ context.Context, _, _ string) error { return nil }
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
}

func TestToolApprovalServiceDecryptsApprovedPayload(t *testing.T) {
	key := crypto.DeriveAESKey("test-key")
	raw, _ := json.Marshal(ToolApprovalPayload{Query: "resume", Arguments: map[string]any{"id": "o1"}})
	encrypted, _ := crypto.Encrypt(key, string(raw))
	repo := &approvalRepoFake{row: domain.ToolApproval{ID: "approval-1", Status: "approved", EncryptedPayload: encrypted, ExpiresAt: time.Now().Add(time.Minute)}}
	payload, err := NewToolApprovalService(repo, nil, key).ApprovedPayload(context.Background(), "tenant-1", "approval-1")
	require.NoError(t, err)
	require.Equal(t, "resume", payload.Query)
	require.Equal(t, "o1", payload.Arguments["id"])
}

func TestToolApprovalServiceRejectsExpiredApproval(t *testing.T) {
	repo := &approvalRepoFake{row: domain.ToolApproval{ID: "approval-1", Status: "approved", ExpiresAt: time.Now().Add(-time.Minute)}}
	_, err := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("key")).ApprovedPayload(context.Background(), "tenant-1", "approval-1")
	require.ErrorIs(t, err, ErrApprovalExpired)
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
