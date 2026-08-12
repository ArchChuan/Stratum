package application

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/stretchr/testify/require"
)

type approvalRepoFake struct {
	row                                                 domain.ToolApproval
	createErr, decideErr, claimErr, markErr, releaseErr error
	unknownErr                                          error
	getErr                                              error
	getCalls                                            int
	getFailFirst                                        bool // 首次 Get 即失败（单次 Get 路径，如 SetAssignee 前置检查）
	released, outcomeUnknown, decided, claimed          int
	lastListUserID, lastAssignee                        string
	pendingN                                            int // 返回给 ListPending 的审批数（配额测试用）
	voidReasons, invalidateReasons                      []string
	voidErr, invalidateErr                              error // 恢复层终结动作硬失败注入（Join 路径测试）
}

func (f *approvalRepoFake) Create(_ context.Context, _ string, row domain.ToolApproval) (string, error) {
	f.row = row
	if f.createErr != nil {
		return "", f.createErr
	}
	return "approval-1", nil
}
func (f *approvalRepoFake) Get(_ context.Context, _, _ string) (domain.ToolApproval, error) {
	f.getCalls++
	// getErr 默认从第二次 Get 起生效（claim 前读取成功、claim 后行消失）；
	// getFailFirst 强制首次即失败（单次 Get 路径）。
	if f.getErr != nil && (f.getFailFirst || f.getCalls > 1) {
		return f.row, f.getErr
	}
	return f.row, nil
}
func (f *approvalRepoFake) Decide(_ context.Context, _, _, _, _, _ string, _ time.Time) error {
	f.decided++
	return f.decideErr
}
func (f *approvalRepoFake) MarkExecuted(_ context.Context, _, _ string) error { return f.markErr }
func (f *approvalRepoFake) ClaimExecution(_ context.Context, _, _ string) error {
	f.claimed++
	return f.claimErr
}
func (f *approvalRepoFake) ReleaseExecution(_ context.Context, _, _ string) error {
	f.released++
	return f.releaseErr
}
func (f *approvalRepoFake) MarkOutcomeUnknown(_ context.Context, _, _ string) error {
	f.outcomeUnknown++
	return f.unknownErr
}
func (f *approvalRepoFake) ListPending(_ context.Context, _, userID string) ([]domain.ToolApproval, error) {
	f.lastListUserID = userID
	if f.pendingN > 0 {
		out := make([]domain.ToolApproval, f.pendingN)
		for i := range out {
			out[i] = f.row
		}
		return out, nil
	}
	return []domain.ToolApproval{f.row}, nil
}
func (f *approvalRepoFake) ListHistory(_ context.Context, _ string, _, _ int) ([]domain.ToolApproval, int, error) {
	return nil, 0, nil
}
func (f *approvalRepoFake) Invalidate(_ context.Context, _, _, reason string) error {
	f.invalidateReasons = append(f.invalidateReasons, reason)
	return f.invalidateErr
}
func (f *approvalRepoFake) Void(_ context.Context, _, _, reason string) error {
	f.voidReasons = append(f.voidReasons, reason)
	return f.voidErr
}
func (f *approvalRepoFake) UpdateAssignee(_ context.Context, _, _, assignee string) error {
	f.lastAssignee = assignee
	return nil
}
func (f *approvalRepoFake) CascadeByConversation(_ context.Context, _, _ string) error {
	return nil
}

func TestToolApprovalServiceRequestDeniesWhenPendingQuotaExhausted(t *testing.T) {
	repo := &approvalRepoFake{pendingN: constants.MaxPendingApprovalsPerActor}
	svc := NewToolApprovalService(repo, &checkpointFake{}, crypto.DeriveAESKey("test-key"))
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", AgentID: "agent-1", UserID: "user-member",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Query: "delete order", Arguments: map[string]any{},
	})
	require.ErrorIs(t, err, domain.ErrTooManyPendingApprovals, "member 超过 pending 配额必须 fail closed，不得再创建审批行")
	require.Equal(t, "user-member", repo.lastListUserID, "配额按发起者 userID 计数")
}

func TestToolApprovalServiceRequestWithinQuotaCreates(t *testing.T) {
	repo := &approvalRepoFake{pendingN: constants.MaxPendingApprovalsPerActor - 1}
	svc := NewToolApprovalService(repo, &checkpointFake{}, crypto.DeriveAESKey("test-key"))
	id, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", AgentID: "agent-1", UserID: "user-member",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Query: "delete order", Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.Equal(t, "approval-1", id)
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
	require.Contains(t, repo.row.MCPRevisionsDigest, "mcp-revisions:v1:sha256:")
	require.Contains(t, repo.row.KnowledgeRevisionsDigest, "knowledge-revisions:v1:sha256:")
}

func TestToolApprovalServiceRejectsTamperedBinding(t *testing.T) {
	key := crypto.DeriveAESKey("test-key")
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, key)
	payload := ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"order_id": "order-1"}, PinnedSkillRevisions: map[string]string{"skill-1": "revision-1"},
		PinnedMCPRevisions: map[string]string{"orders": "mcp-revision-1"},
		PinnedKnowledgeRevisions: map[string]port.KnowledgeRevisionPin{
			"support": {RevisionID: "knowledge-revision-1", ExperimentID: "experiment-1", Variant: "canary"},
		},
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
		{name: "MCP revisions", mutate: func(row *domain.ToolApproval) { row.MCPRevisionsDigest = "other" }},
		{name: "Knowledge revisions", mutate: func(row *domain.ToolApproval) { row.KnowledgeRevisionsDigest = "other" }},
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

func TestToolApprovalServiceListPendingScopesByRole(t *testing.T) {
	// 回归防护（review blocking：member 横向越权）——角色由 resolver 现查，fake 捕获透传给 repo 的 userID。
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	ctx := context.Background()
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "member"})
	if _, err := svc.ListPending(ctx, "tenant-1", "user-member"); err != nil {
		t.Fatal(err)
	}
	if repo.lastListUserID != "user-member" {
		t.Fatalf("member scope: expected user-member, got %q", repo.lastListUserID)
	}
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	if _, err := svc.ListPending(ctx, "tenant-1", "user-admin"); err != nil {
		t.Fatal(err)
	}
	if repo.lastListUserID != "" {
		t.Fatalf("admin scope: expected empty (all), got %q", repo.lastListUserID)
	}
	// fail closed：未知角色按 member 最小权限
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "stranger"})
	if _, err := svc.ListPending(ctx, "tenant-1", "user-x"); err != nil {
		t.Fatal(err)
	}
	if repo.lastListUserID != "user-x" {
		t.Fatalf("unknown role: expected user-x (least privilege), got %q", repo.lastListUserID)
	}
	// fail closed：resolver 报错 → 拒绝（不默认放行）
	svc.SetTenantRoleResolver(&fakeRoleResolver{err: errors.New("db down")})
	if _, err := svc.ListPending(ctx, "tenant-1", "user-x"); err == nil {
		t.Fatal("expected error when role resolver fails")
	}
}

func TestToolApprovalServiceReportsBindingMismatchFields(t *testing.T) {
	key := crypto.DeriveAESKey("test-key")
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, key)
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"order_id": "order-1"},
	})
	require.NoError(t, err)
	repo.row.Status = string(domain.ToolApprovalApproved)
	repo.row.ExpiresAt = time.Now().Add(time.Minute)
	repo.row.TraceID = "other"
	repo.row.PolicyVersion = "other"

	_, err = svc.ApprovedPayload(context.Background(), "tenant-1", "approval-1")
	require.ErrorIs(t, err, ErrApprovalBindingMismatch)
	require.EqualError(t, err, "tool approval binding mismatch: policy_version,trace_id")
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

func TestToolApprovalServiceNormalizesEmptyRevisionPinsAcrossJSONRoundTrip(t *testing.T) {
	key := crypto.DeriveAESKey("test-key")
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, key)
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "get", RiskLevel: port.ToolRiskUnclassified,
		Arguments:                map[string]any{"id": "o1"},
		PinnedSkillRevisions:     map[string]string{},
		PinnedMCPRevisions:       map[string]string{},
		PinnedKnowledgeRevisions: map[string]port.KnowledgeRevisionPin{},
	})
	require.NoError(t, err)
	repo.row.Status = string(domain.ToolApprovalApproved)
	repo.row.ExpiresAt = time.Now().Add(time.Minute)

	_, err = svc.ApprovedPayload(context.Background(), "tenant-1", "approval-1")
	require.NoError(t, err)
}

func TestToolApprovalServiceRejectsExpiredApproval(t *testing.T) {
	repo := &approvalRepoFake{row: domain.ToolApproval{ID: "approval-1", Status: "approved", ExpiresAt: time.Now().Add(-time.Minute)}}
	_, err := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("key")).ApprovedPayload(context.Background(), "tenant-1", "approval-1")
	require.ErrorIs(t, err, ErrApprovalExpired)
}

func TestToolApprovalServiceReportsUnknownOutcomeDistinctly(t *testing.T) {
	repo := &approvalRepoFake{row: domain.ToolApproval{
		ID: "approval-1", Status: string(domain.ToolApprovalOutcomeUnknown), ExpiresAt: time.Now().Add(time.Minute),
	}}
	_, err := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("key")).ApprovedPayload(
		context.Background(), "tenant-1", "approval-1",
	)
	require.ErrorIs(t, err, ErrApprovalOutcomeUnknown)
}

type failingMCPExecutor struct {
	err error
}

type revisionCapturingMCPExecutor struct {
	revisionID string
	risk       port.ToolRiskLevel
}

func (e *revisionCapturingMCPExecutor) ExecuteMCPTool(
	context.Context, string, string, map[string]any,
) (port.MCPToolResult, error) {
	return port.MCPToolResult{}, errors.New("mutable MCP execution must not be used")
}

func (e *revisionCapturingMCPExecutor) ExecuteMCPToolRevision(
	_ context.Context, _, _, revisionID string, risk port.ToolRiskLevel, _ map[string]any,
) (port.MCPToolResult, error) {
	e.revisionID = revisionID
	e.risk = risk
	return port.MCPToolResult{StructuredContent: map[string]any{"ok": true}}, nil
}

func TestToolApprovalExecutionUsesPinnedMCPRevision(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	payload := ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"id": "order-1"}, PinnedMCPRevisions: map[string]string{"orders": "mcp-revision-1"},
	}
	_, err := svc.Request(context.Background(), payload)
	require.NoError(t, err)
	repo.row.ID = "approval-1"
	repo.row.Status = "approved"
	repo.row.ExpiresAt = time.Now().Add(time.Minute)
	executor := &revisionCapturingMCPExecutor{}

	_, err = svc.ExecuteApproved(
		context.Background(), "tenant-1", "approval-1", "orders", "delete", payload.Arguments, executor,
	)

	require.NoError(t, err)
	require.Equal(t, "mcp-revision-1", executor.revisionID)
	require.Equal(t, port.ToolRiskDestructive, executor.risk)
}

func (e failingMCPExecutor) ExecuteMCPTool(context.Context, string, string, map[string]any) (port.MCPToolResult, error) {
	return port.MCPToolResult{}, e.err
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

func TestToolApprovalPersistenceFailuresPropagate(t *testing.T) {
	t.Run("approval create", func(t *testing.T) {
		persistErr := errors.New("approval create failed")
		repo := &approvalRepoFake{createErr: persistErr}
		_, err := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("key")).Request(
			context.Background(), ToolApprovalPayload{TenantID: "tenant-1", Arguments: map[string]any{}},
		)
		require.ErrorIs(t, err, persistErr)
	})

	t.Run("checkpoint upsert", func(t *testing.T) {
		persistErr := errors.New("checkpoint upsert failed")
		checkpoints := &checkpointFake{err: persistErr}
		_, err := NewToolApprovalService(&approvalRepoFake{}, checkpoints, crypto.DeriveAESKey("key")).Request(
			context.Background(), ToolApprovalPayload{
				TenantID: "tenant-1", ExecutionID: "exec-1", Arguments: map[string]any{},
			},
		)
		require.ErrorIs(t, err, persistErr)
	})

	t.Run("execution claim before side effect", func(t *testing.T) {
		persistErr := errors.New("claim failed")
		repo, svc, payload := approvedToolApprovalFixture(t)
		repo.claimErr = persistErr
		executor := &countingMCPExecutor{}
		_, err := svc.ExecuteApproved(
			context.Background(), "tenant-1", "approval-1", "orders", "delete", payload.Arguments, executor,
		)
		require.ErrorIs(t, err, persistErr)
		require.Zero(t, executor.calls)
	})

	t.Run("executed status after side effect", func(t *testing.T) {
		persistErr := errors.New("mark executed failed")
		repo, svc, payload := approvedToolApprovalFixture(t)
		repo.markErr = persistErr
		executor := &countingMCPExecutor{}
		_, err := svc.ExecuteApproved(
			context.Background(), "tenant-1", "approval-1", "orders", "delete", payload.Arguments, executor,
		)
		require.ErrorIs(t, err, persistErr)
		require.Equal(t, 1, executor.calls)
	})

	t.Run("unknown outcome status", func(t *testing.T) {
		persistErr := errors.New("mark unknown failed")
		repo, svc, payload := approvedToolApprovalFixture(t)
		repo.unknownErr = persistErr
		_, err := svc.ExecuteApproved(
			context.Background(), "tenant-1", "approval-1", "orders", "delete", payload.Arguments,
			failingMCPExecutor{err: errors.New("untyped transport error")},
		)
		require.ErrorIs(t, err, persistErr)
	})
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
	err error
}

func (f *checkpointFake) Upsert(_ context.Context, _ string, row domain.AgentExecutionCheckpoint) error {
	f.row = row
	return f.err
}
func (f *checkpointFake) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, errors.New("unused")
}
func (f *checkpointFake) MarkCompleted(context.Context, string, string) error        { return nil }
func (f *checkpointFake) UpdateStatus(context.Context, string, string, string) error { return nil }
func (f *checkpointFake) DeleteExpired(context.Context, string) (int64, error)       { return 0, nil }

type fakeRoleResolver struct {
	role  string
	roles map[string]string // userID → role，按用户覆盖 role
	err   error
}

func (f *fakeRoleResolver) ResolveTenantRole(_ context.Context, _ string, userID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.roles != nil {
		if r, ok := f.roles[userID]; ok {
			return r, nil
		}
	}
	return f.role, nil
}

type fakeApprovalExecutor struct {
	output map[string]any
	err    error
	got    *port.ApprovalActionRequest
}

func (f *fakeApprovalExecutor) ExecuteApprovalAction(_ context.Context, req port.ApprovalActionRequest) (map[string]any, error) {
	f.got = &req
	return f.output, f.err
}

func mustEncrypt(t *testing.T, payload ToolApprovalPayload) string {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	encrypted, err := crypto.Encrypt(crypto.DeriveAESKey("test"), string(raw))
	require.NoError(t, err)
	return encrypted
}

func TestDecideRejectsSelfDecision(t *testing.T) {
	now := time.Now()
	repo := &approvalRepoFake{row: domain.ToolApproval{
		ID: "a1", UserID: "user-1", Status: "pending", ExpiresAt: now.Add(time.Minute),
		EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool}),
	}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	err := svc.Decide(context.Background(), "tenant-1", "a1", "approved", "user-1", "")
	require.ErrorIs(t, err, domain.ErrApprovalSelfDecision)
}

func TestDecideRejectsNonAdminActor(t *testing.T) {
	now := time.Now()
	repo := &approvalRepoFake{row: domain.ToolApproval{
		ID: "a1", UserID: "user-1", Status: "pending", ExpiresAt: now.Add(time.Minute),
		EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool}),
	}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "member"})
	err := svc.Decide(context.Background(), "tenant-1", "a1", "approved", "user-2", "")
	require.ErrorIs(t, err, domain.ErrApprovalRoleDenied)
}

// D8 软绑定：assignee 仅优先级提示，非 assignee 的 admin 仍可处理（不阻塞）。
func TestDecideAllowsNonAssigneeAdmin(t *testing.T) {
	now := time.Now()
	repo := &approvalRepoFake{row: domain.ToolApproval{
		ID: "a1", UserID: "user-1", AssignedApprover: "user-3", Status: "pending",
		ExpiresAt:        now.Add(time.Minute),
		EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool}),
	}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	err := svc.Decide(context.Background(), "tenant-1", "a1", "approved", "user-2", "")
	require.NoError(t, err)
	require.Equal(t, 1, repo.decided)
}

func TestDecideApprovesMatchingAssignee(t *testing.T) {
	now := time.Now()
	repo := &approvalRepoFake{row: domain.ToolApproval{
		ID: "a1", UserID: "user-1", AssignedApprover: "user-3", Status: "pending",
		ExpiresAt:        now.Add(time.Minute),
		EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool}),
	}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "owner"})
	err := svc.Decide(context.Background(), "tenant-1", "a1", "approved", "user-3", "")
	require.NoError(t, err)
	require.Equal(t, 1, repo.decided)
}

func TestDecideRejectsExpiredPending(t *testing.T) {
	now := time.Now()
	repo := &approvalRepoFake{row: domain.ToolApproval{
		ID: "a1", UserID: "user-1", Status: "pending",
		ExpiresAt:        now.Add(-time.Minute),
		EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool}),
	}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	err := svc.Decide(context.Background(), "tenant-1", "a1", "approved", "user-2", "")
	require.ErrorIs(t, err, ErrApprovalExpired)
	require.Zero(t, repo.decided)
}

func TestDecideFailClosedWithoutRoleResolver(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	err := svc.Decide(context.Background(), "tenant-1", "a1", "approved", "user-2", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "role resolver unavailable")
}

func TestMemberHistoryAndDetailDenied(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "member"})
	if _, _, err := svc.ListHistory(context.Background(), "tenant-1", 1, 20, "user-1"); !errors.Is(err, domain.ErrApprovalRoleDenied) {
		t.Fatalf("expected ErrApprovalRoleDenied for member history, got %v", err)
	}
	if _, err := svc.ApprovalDetail(context.Background(), "tenant-1", "a1", "user-1"); !errors.Is(err, domain.ErrApprovalRoleDenied) {
		t.Fatalf("expected ErrApprovalRoleDenied for member detail, got %v", err)
	}
	if err := svc.SetAssignee(context.Background(), "tenant-1", "a1", "user-3", "user-1"); !errors.Is(err, domain.ErrApprovalRoleDenied) {
		t.Fatalf("expected ErrApprovalRoleDenied for member assignee, got %v", err)
	}
}

func TestSetAssigneeRequiresAdminTarget(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{roles: map[string]string{
		"user-admin": "admin", "user-m2": "member",
	}})
	// admin 指定 member 为新审批人 → 拒绝
	err := svc.SetAssignee(context.Background(), "tenant-1", "a1", "user-m2", "user-admin")
	require.ErrorIs(t, err, domain.ErrApprovalAssigneeInvalid)
	require.Empty(t, repo.lastAssignee)
}

// 回归（review minor 3rd）：不存在的审批 → ErrApprovalNotFound（404），不折叠成 AlreadyDecided。
func TestSetAssigneeMissingApprovalNotFound(t *testing.T) {
	repo := &approvalRepoFake{getErr: errors.New("not found"), getFailFirst: true}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{roles: map[string]string{
		"user-owner": "owner", "user-admin2": "admin",
	}})
	err := svc.SetAssignee(context.Background(), "tenant-1", "missing", "user-admin2", "user-owner")
	require.Error(t, err)
	require.NotErrorIs(t, err, domain.ErrApprovalAlreadyDecided)
	require.Contains(t, err.Error(), "not found")
}

func TestSetAssigneeHappyPath(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{roles: map[string]string{
		"user-owner": "owner", "user-admin2": "admin",
	}})
	err := svc.SetAssignee(context.Background(), "tenant-1", "a1", "user-admin2", "user-owner")
	require.NoError(t, err)
	require.Equal(t, "user-admin2", repo.lastAssignee)
}

func TestRequestRejectsNonAdminAssignee(t *testing.T) {
	svc := NewToolApprovalService(&approvalRepoFake{}, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "member"})
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool,
		AssignedApprover: "user-m2",
	})
	require.ErrorIs(t, err, domain.ErrApprovalAssigneeInvalid)
}

func TestApprovalDetailRedactsPayload(t *testing.T) {
	now := time.Now()
	repo := &approvalRepoFake{row: domain.ToolApproval{
		ID: "a1", SubjectKind: domain.SubjectKindMCPTool, ToolName: "bash", ServerID: "srv-1",
		RiskLevel: "high", Status: "approved", UserID: "user-1", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
		EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{
			UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool,
			Arguments: map[string]any{"command": "ls", "apiKey": "sk-123"},
		}),
	}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "owner"})
	detail, err := svc.ApprovalDetail(context.Background(), "tenant-1", "a1", "user-owner")
	require.NoError(t, err)
	require.Equal(t, "a1", detail.ID)
	require.Equal(t, "ls", detail.Payload["command"])
	require.Equal(t, "***", detail.Payload["apiKey"])
}

// 通过 Request 构造已批准审批（digest/tenant 与 payload 自动一致），再改 status/decided_by。
func approvedRow(t *testing.T, payload ToolApprovalPayload) *approvalRepoFake {
	t.Helper()
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	id, err := svc.Request(context.Background(), payload)
	require.NoError(t, err)
	repo.row.ID = id
	repo.row.Status = "approved"
	repo.row.DecidedBy = "user-admin"
	repo.row.ExpiresAt = time.Now().Add(time.Minute)
	return repo
}

func TestExecuteApprovedActionSuccess(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindEvaluationAction,
		Arguments: map[string]any{"evaluation_id": "ev-1"},
	})
	executor := &fakeApprovalExecutor{output: map[string]any{"ok": true}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	out, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-admin", executor)
	require.NoError(t, err)
	require.Equal(t, true, out["ok"])
	require.Equal(t, "tenant-1", executor.got.TenantID)
	require.Equal(t, domain.SubjectKindEvaluationAction, executor.got.SubjectKind)
	require.Equal(t, "user-1", executor.got.ActorID)
	require.Equal(t, "user-admin", executor.got.DecidedBy)
	require.Equal(t, map[string]any{"evaluation_id": "ev-1"}, executor.got.Arguments)
}

// 执行者角色现查（review security MEDIUM）：member 无法执行已批准审批，fail closed。
func TestExecuteApprovedActionDeniesNonAdminActor(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindEvaluationAction,
		Arguments: map[string]any{"operation": "pause_experiment"},
	})
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "member"})
	_, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-member", &fakeApprovalExecutor{})
	require.ErrorIs(t, err, domain.ErrApprovalRoleDenied)
	require.Zero(t, repo.claimed) // 未 claim = 未消费，审批保持 approved
	require.Zero(t, repo.released)
	require.Zero(t, repo.outcomeUnknown)
}

func TestExecuteApprovedActionMarksUnknownOnSideEffectFailure(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindMCPPolicy,
		Arguments: map[string]any{},
	})
	executor := &fakeApprovalExecutor{err: errors.New("provider rejected after partial apply")}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	_, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-admin", executor)
	require.Error(t, err)
	require.Equal(t, 1, repo.outcomeUnknown)
	require.Zero(t, repo.released)
}

func TestExecuteApprovedActionReleasesPreExecutionFailure(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindMCPServer,
		Arguments: map[string]any{},
	})
	executor := &fakeApprovalExecutor{err: &port.ApprovalActionNotExecutedError{Err: errors.New("target unreachable")}}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	_, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-admin", executor)
	require.Error(t, err)
	require.Equal(t, 1, repo.released)
	require.Zero(t, repo.outcomeUnknown)
}

func TestExecuteApprovedActionNilExecutorFailsClosed(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool,
	})
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	_, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-admin", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "executor not configured")
}

func TestExecuteApprovedActionRejectsInvalidated(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool,
	})
	repo.row.Status = string(domain.ToolApprovalInvalidated)
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	_, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-admin", &fakeApprovalExecutor{})
	require.ErrorIs(t, err, domain.ErrApprovalInvalidated)
}

// 回归（review major 2）：claim 后 Get 失败（行消失）→ 标记 unknown_outcome，不卡死 executing。
func TestExecuteApprovedActionMarksUnknownOnPostClaimGetFailure(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindMCPServer,
		Arguments: map[string]any{},
	})
	getErr := errors.New("row vanished after claim")
	repo.getErr = getErr
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	_, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-admin", &fakeApprovalExecutor{})
	require.ErrorIs(t, err, getErr)
	require.Equal(t, 1, repo.outcomeUnknown)
	require.Zero(t, repo.released)
	// MarkOutcomeUnknown 成功时不得出现 "%!w(<nil>)" 伪错误文本。
	require.NotContains(t, err.Error(), "%!w(<nil>)")
}

// 回归（review major 3）：执行器成功但 MarkExecuted 持久化失败 → 副作用已发生，
// 标记 unknown_outcome 避免永久卡死 executing。
func TestExecuteApprovedActionMarksUnknownOnMarkExecutedFailure(t *testing.T) {
	repo := approvedRow(t, ToolApprovalPayload{
		TenantID: "tenant-1", UserID: "user-1", SubjectKind: domain.SubjectKindMCPPolicy,
		Arguments: map[string]any{},
	})
	markErr := errors.New("mark executed db hiccup")
	repo.markErr = markErr
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "admin"})
	executor := &fakeApprovalExecutor{output: map[string]any{"ok": true}}
	_, err := svc.ExecuteApprovedAction(context.Background(), "tenant-1", repo.row.ID, "user-admin", executor)
	require.ErrorIs(t, err, markErr)
	require.Equal(t, 1, repo.outcomeUnknown)
	require.Zero(t, repo.released)
	require.NotNil(t, executor.got) // 副作用确实已执行
}
