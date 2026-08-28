package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	createCount                                         int // Create 调用计数，生成递增 approval-N（批量测试断言多条 ID）
	unknownErr                                          error
	getErr                                              error
	getCalls                                            int
	getFailFirst                                        bool // 首次 Get 即失败（单次 Get 路径，如 SetAssignee 前置检查）
	released, outcomeUnknown, decided, claimed          int
	lastListUserID, lastAssignee                        string
	pendingN                                            int // 返回给 ListPending 的审批数（配额测试用）
	voidReasons, invalidateReasons                      []string
	voidErr, invalidateErr                              error // 恢复层终结动作硬失败注入（Join 路径测试）
	// 方案 C / F2 调用审计：记录 InvalidateStaleForTool 与配额/列表查询的实际调用，
	// 供"作废旧 pending 先于创建"与"配额仅计 pending"两个回归断言使用。
	invalidateStaleCalls, listPendingCalls, listActionableCalls int
	invalidateStaleErr                                          error
	cancelErr                                                   error // Cancel 硬失败注入
	cancelled                                                   int
	lastCancelReason                                            string                         // 最近一次 Cancel 的 decision_reason（发起人自撤 vs admin 代撤）
	rowByID                                                     map[string]domain.ToolApproval // 批量测试：Get 按 approvalID 分发（混合状态批次）
}

func (f *approvalRepoFake) Create(_ context.Context, _ string, row domain.ToolApproval) (string, error) {
	f.row = row
	if f.createErr != nil {
		return "", f.createErr
	}
	f.createCount++
	return fmt.Sprintf("approval-%d", f.createCount), nil
}
func (f *approvalRepoFake) Get(_ context.Context, _, approvalID string) (domain.ToolApproval, error) {
	f.getCalls++
	// 批量测试：rowByID 命中按 approvalID 分发；未命中回退单行桩（存量测试不受影响）。
	if f.rowByID != nil {
		if row, ok := f.rowByID[approvalID]; ok {
			return row, nil
		}
	}
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
	f.listPendingCalls++
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
func (f *approvalRepoFake) ListHistory(_ context.Context, _, userID string, _, _ int) ([]domain.ToolApproval, int, error) {
	f.lastListUserID = userID
	if f.row.ID != "" {
		return []domain.ToolApproval{f.row}, 1, nil
	}
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
func (f *approvalRepoFake) ListByExecution(context.Context, string, string) ([]domain.ToolApproval, error) {
	return nil, nil
}
func (f *approvalRepoFake) ListActionable(_ context.Context, _, userID string) ([]domain.ToolApproval, error) {
	f.listActionableCalls++
	f.lastListUserID = userID
	if f.row.ID != "" {
		return []domain.ToolApproval{f.row}, nil
	}
	return nil, nil
}
func (f *approvalRepoFake) InvalidateStaleForTool(_ context.Context, _, _, _, _ string) (int64, error) {
	f.invalidateStaleCalls++
	return 0, f.invalidateStaleErr
}
func (f *approvalRepoFake) ExpireStale(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (f *approvalRepoFake) Cancel(_ context.Context, _, _, _, reason string, _ time.Time) error {
	f.cancelled++
	f.lastCancelReason = reason
	return f.cancelErr
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
	require.JSONEq(t, `{"approval_ids":["approval-1"],"approval_id":"approval-1"}`, string(checkpoints.row.RuntimeStateJSON))
	require.NotContains(t, string(checkpoints.row.PendingToolCallsJSON), "do-not-store-plain")
	require.JSONEq(t, `[{"approval_id":"approval-1","tool_call_id":"call-1","server_id":"orders","tool_name":"delete","risk_level":"destructive"}]`, string(checkpoints.row.PendingToolCallsJSON))
	require.NotEmpty(t, repo.row.DecisionID)
	require.Contains(t, repo.row.ArgumentsDigest, "tool-arguments:v1:sha256:")
	require.Contains(t, repo.row.SkillRevisionsDigest, "skill-revisions:v1:sha256:")
	require.Contains(t, repo.row.MCPRevisionsDigest, "mcp-revisions:v1:sha256:")
	require.Contains(t, repo.row.KnowledgeRevisionsDigest, "knowledge-revisions:v1:sha256:")
}

func TestToolApprovalServiceRequestBatchCreatesAllAndSingleCheckpoint(t *testing.T) {
	repo := &approvalRepoFake{}
	checkpoints := &checkpointFake{}
	svc := NewToolApprovalService(repo, checkpoints, crypto.DeriveAESKey("test-key"))
	ids, err := svc.RequestBatch(context.Background(), []ToolApprovalPayload{
		{
			TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
			ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
			Arguments: map[string]any{"order_id": "order-1"},
		},
		{
			TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
			ToolCallID: "call-2", ServerID: "customers", ToolName: "purge", RiskLevel: port.ToolRiskDestructive,
			Arguments: map[string]any{"customer_id": "customer-1"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"approval-1", "approval-2"}, ids)
	require.Equal(t, 2, repo.createCount, "批内每条都落库")
	require.Equal(t, 1, checkpoints.upsertCount, "批量审批只写一次 checkpoint，防止互相覆盖")
	require.JSONEq(t, `{"approval_ids":["approval-1","approval-2"],"approval_id":"approval-1"}`, string(checkpoints.row.RuntimeStateJSON))
	require.JSONEq(t, `[
		{"approval_id":"approval-1","tool_call_id":"call-1","server_id":"orders","tool_name":"delete","risk_level":"destructive"},
		{"approval_id":"approval-2","tool_call_id":"call-2","server_id":"customers","tool_name":"purge","risk_level":"destructive"}
	]`, string(checkpoints.row.PendingToolCallsJSON))
}

func TestToolApprovalServiceRequestBatchQuotaCountsWholeBatch(t *testing.T) {
	repo := &approvalRepoFake{pendingN: constants.MaxPendingApprovalsPerActor - 1}
	svc := NewToolApprovalService(repo, &checkpointFake{}, crypto.DeriveAESKey("test-key"))
	// 现有 pending 距上限只差 1，批量 2 条：quota 按 len(batch) 一次抵扣，不得逐条
	// 创建（第 2 条起误报配额）。
	_, err := svc.RequestBatch(context.Background(), []ToolApprovalPayload{
		{TenantID: "tenant-1", ExecutionID: "exec-1", AgentID: "agent-1", UserID: "user-member",
			ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
			Arguments: map[string]any{}},
		{TenantID: "tenant-1", ExecutionID: "exec-1", AgentID: "agent-1", UserID: "user-member",
			ToolCallID: "call-2", ServerID: "customers", ToolName: "purge", RiskLevel: port.ToolRiskDestructive,
			Arguments: map[string]any{}},
	})
	require.ErrorIs(t, err, domain.ErrTooManyPendingApprovals)
	require.Zero(t, repo.createCount, "配额不满足时整批不得创建任何审批行")
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
	row         domain.AgentExecutionCheckpoint
	err         error
	upsertCount int // Upsert 调用次数（批量审批只写一次 checkpoint 断言用）
}

func (f *checkpointFake) Upsert(_ context.Context, _ string, row domain.AgentExecutionCheckpoint) error {
	f.row = row
	f.upsertCount++
	return f.err
}
func (f *checkpointFake) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, errors.New("unused")
}
func (f *checkpointFake) MarkCompleted(context.Context, string, string) error        { return nil }
func (f *checkpointFake) UpdateStatus(context.Context, string, string, string) error { return nil }
func (f *checkpointFake) DeleteExpired(context.Context, string) (int64, error)       { return 0, nil }
func (f *checkpointFake) GetLatestActiveByConversation(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, errors.New("unused")
}
func (f *checkpointFake) UpdateStatusFrom(context.Context, string, string, string, string) error {
	return nil
}
func (f *checkpointFake) AdvanceRunGeneration(context.Context, string, string, int) error { return nil }
func (f *checkpointFake) Terminate(context.Context, string, string, string) error         { return nil }

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

// fakeActorNameResolver 固定返回预置昵称映射（M5 昵称解析测试）。
type fakeActorNameResolver struct {
	names map[string]string
}

func (r *fakeActorNameResolver) ResolveActorNames(_ context.Context, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		if n, ok := r.names[id]; ok {
			out[id] = n
		}
	}
	return out, nil
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

// M5/D4 放宽：member 历史仅看自己发起的（service 注入 userID=actor 过滤，不再拒绝）；
// 详情仅归属自己可看，非归属统一 404（关闭存在性 oracle）；SetAssignee 仍要求 admin/owner。
func TestMemberHistoryScopedAndDetailOwnedOnly(t *testing.T) {
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{role: "member"})
	// member 历史：userID=actor 透传 repo（过滤本人发起），total 与列表由 store 层保持一致。
	if _, _, err := svc.ListHistory(context.Background(), "tenant-1", 1, 20, "user-1"); err != nil {
		t.Fatalf("member history should pass through with userID scope, got %v", err)
	}
	require.Equal(t, "user-1", repo.lastListUserID, "member 历史必须按 userID=actor 过滤")
	// member 详情：非归属（row.UserID 为空）统一 404（关闭存在性 oracle）。
	if _, err := svc.ApprovalDetail(context.Background(), "tenant-1", "a1", "user-1"); !errors.Is(err, domain.ErrApprovalNotFound) {
		t.Fatalf("expected ErrApprovalNotFound for member non-owned detail, got %v", err)
	}
	// member 详情：归属自己（row.UserID==actor）放行。
	repo.row.UserID = "user-1"
	if _, err := svc.ApprovalDetail(context.Background(), "tenant-1", "a1", "user-1"); err != nil {
		t.Fatalf("member owned detail should be allowed, got %v", err)
	}
	// SetAssignee 仍要求 admin/owner（member 拒绝，fail closed）。
	if err := svc.SetAssignee(context.Background(), "tenant-1", "a1", "user-3", "user-1"); !errors.Is(err, domain.ErrApprovalRoleDenied) {
		t.Fatalf("expected ErrApprovalRoleDenied for member assignee, got %v", err)
	}
}

// M5：admin/owner 历史不过滤（userID="" 全租户工作台）；昵称解析注入后填充展示名。
func TestHistoryAdminScopesAllAndResolvesNames(t *testing.T) {
	repo := &approvalRepoFake{}
	repo.row = domain.ToolApproval{
		ID: "a1", UserID: "user-1", AssignedApprover: "user-2", DecidedBy: "user-2",
		EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: domain.SubjectKindMCPTool}),
	}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test"))
	svc.SetTenantRoleResolver(&fakeRoleResolver{roles: map[string]string{"user-admin": "admin"}})
	svc.SetActorNameResolver(&fakeActorNameResolver{names: map[string]string{
		"user-1": "张三", "user-2": "李四",
	}})
	rows, _, err := svc.ListHistory(context.Background(), "tenant-1", 1, 20, "user-admin")
	if err != nil {
		t.Fatalf("admin history should be scoped to whole tenant, got %v", err)
	}
	require.Empty(t, repo.lastListUserID, "admin/owner 历史必须全租户（userID 为空）")
	// 昵称解析：列表行填充展示名（张三/李四），而非 raw id。
	require.Len(t, rows, 1)
	require.Equal(t, "张三", rows[0].UserDisplayName)
	require.Equal(t, "李四", rows[0].AssignedApproverName)
	require.Equal(t, "李四", rows[0].DecidedByName)
	// 详情同样填充昵称。
	detail, err := svc.ApprovalDetail(context.Background(), "tenant-1", "a1", "user-admin")
	require.NoError(t, err)
	require.Equal(t, "张三", detail.UserDisplayName)
	require.Equal(t, "李四", detail.DecidedByName)
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

// ── 主动取消审批（pending→cancelled）：CancelApproval 授权与 oracle ──
// 校验顺序（与 ApprovalDetail 同构，关闭存在性 oracle）：角色(fail closed)→Get→归属校验
// →pendingPayload(状态/过期/解密)→CAS。非发起人 member 对「存在 pending/已决定/不存在」的
// ID 一律 ErrApprovalNotFound（无 404/409/410 状态分化）；admin/owner 可代撤（reason 区分）。

// newApprovalServiceWithRow 构造带一行 pending 审批（UserID=user-1）的 service+repo。
// 调用方可改 repo.row.Status/ExpiresAt 模拟各种状态。
func newApprovalServiceWithRow(t *testing.T, resolver *fakeRoleResolver) (*ToolApprovalService, *approvalRepoFake) {
	t.Helper()
	repo := &approvalRepoFake{}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	if resolver != nil {
		svc.SetTenantRoleResolver(resolver)
	}
	_, err := svc.Request(context.Background(), ToolApprovalPayload{
		TenantID: "tenant-1", ExecutionID: "exec-1", TraceID: "trace-1", AgentID: "agent-1", UserID: "user-1",
		ToolCallID: "call-1", ServerID: "orders", ToolName: "delete", RiskLevel: port.ToolRiskDestructive,
		Arguments: map[string]any{"order_id": "order-1"},
	})
	require.NoError(t, err)
	return svc, repo
}

func TestCancelApprovalInitiatorCanCancelOwn(t *testing.T) {
	svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{roles: map[string]string{
		"user-1": "member", "user-other": "member",
	}})
	// 发起人本人（user-1=row.UserID）取消 → 放行，reason 标记 initiator。
	require.NoError(t, svc.CancelApproval(context.Background(), "tenant-1", "approval-1", "user-1"))
	require.Equal(t, 1, repo.cancelled)
	require.Equal(t, "cancelled_by_initiator", repo.lastCancelReason)
}

func TestCancelApprovalNonInitiatorMemberAlwaysNotFound(t *testing.T) {
	// 非发起人 member：存在且 pending / 已决定 / 不存在 → 一律 ErrApprovalNotFound
	// （无状态分化，关闭存在性 oracle）。
	svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{roles: map[string]string{
		"user-1": "member", "user-other": "member",
	}})
	actor := "user-other"

	t.Run("pending existing", func(t *testing.T) {
		err := svc.CancelApproval(context.Background(), "tenant-1", "approval-1", actor)
		require.ErrorIs(t, err, domain.ErrApprovalNotFound)
		require.Equal(t, 0, repo.cancelled, "非发起人 member 先被归属校验拒绝，不触达 CAS")
	})

	t.Run("already decided", func(t *testing.T) {
		repo.row.Status = string(domain.ToolApprovalApproved)
		err := svc.CancelApproval(context.Background(), "tenant-1", "approval-1", actor)
		require.ErrorIs(t, err, domain.ErrApprovalNotFound, "归属校验先于状态检查，不得泄漏 AlreadyDecided")
		require.Equal(t, 0, repo.cancelled)
	})

	t.Run("missing", func(t *testing.T) {
		missing := &approvalRepoFake{getErr: domain.ErrApprovalNotFound, getFailFirst: true}
		msvc := NewToolApprovalService(missing, nil, crypto.DeriveAESKey("test-key"))
		msvc.SetTenantRoleResolver(&fakeRoleResolver{role: "member"})
		err := msvc.CancelApproval(context.Background(), "tenant-1", "missing", actor)
		require.ErrorIs(t, err, domain.ErrApprovalNotFound)
		require.Equal(t, 0, missing.cancelled)
	})
}

func TestCancelApprovalAdminOwnerCanCancelAny(t *testing.T) {
	for _, role := range []string{"admin", "owner"} {
		t.Run(role, func(t *testing.T) {
			svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{role: role})
			require.NoError(t, svc.CancelApproval(context.Background(), "tenant-1", "approval-1", "user-admin"))
			require.Equal(t, 1, repo.cancelled)
			require.Equal(t, "cancelled_by_approver", repo.lastCancelReason, "admin 代撤须与发起人自撤可区分")
		})
	}
}

func TestCancelApprovalNonPendingAlreadyDecided(t *testing.T) {
	// 发起人取消已决定（approved）→ pendingPayload 报 ErrApprovalAlreadyDecided，不落 CAS。
	svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{role: "member"})
	repo.row.Status = string(domain.ToolApprovalApproved)
	err := svc.CancelApproval(context.Background(), "tenant-1", "approval-1", "user-1")
	require.ErrorIs(t, err, domain.ErrApprovalAlreadyDecided)
	require.Equal(t, 0, repo.cancelled)
}

func TestCancelApprovalExpiredPending(t *testing.T) {
	svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{role: "member"})
	repo.row.ExpiresAt = time.Now().Add(-time.Minute)
	err := svc.CancelApproval(context.Background(), "tenant-1", "approval-1", "user-1")
	require.ErrorIs(t, err, ErrApprovalExpired)
	require.Equal(t, 0, repo.cancelled)
}

func TestCancelApprovalRoleResolverFailClosed(t *testing.T) {
	svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{err: errors.New("db down")})
	err := svc.CancelApproval(context.Background(), "tenant-1", "approval-1", "user-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "db down")
	require.Equal(t, 0, repo.cancelled, "resolver 失败必须 fail closed，不得继续校验或 CAS")
}

func TestCancelApprovalRepoHardFailPropagates(t *testing.T) {
	svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{role: "member"})
	repo.cancelErr = errors.New("db down")
	err := svc.CancelApproval(context.Background(), "tenant-1", "approval-1", "user-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "db down")
	require.Equal(t, 1, repo.cancelled, "CAS 硬失败须透传（已调 Cancel）")
}

// ── 终态续跑载荷读取：TerminalResumePayload 按 row.Status 显式枚举 ──
// 显式终态 cancelled/rejected 放行（已过期也放行，无过期门控 H1）；时钟过期分支对
// pending/approved 视作终态放行（回归修复：过期不再 410 卡死会话）；未过期 pending/
// approved 保持 202。

func TestTerminalResumePayloadTerminalStates(t *testing.T) {
	tests := []struct {
		name     string
		status   domain.ToolApprovalStatus
		expires  time.Time
		wantStat string
	}{
		{name: "cancelled unexpired", status: domain.ToolApprovalCancelled, expires: time.Now().Add(time.Minute), wantStat: string(domain.ToolApprovalCancelled)},
		{name: "cancelled expired still allowed", status: domain.ToolApprovalCancelled, expires: time.Now().Add(-time.Hour), wantStat: string(domain.ToolApprovalCancelled)},
		{name: "rejected expired still allowed", status: domain.ToolApprovalRejected, expires: time.Now().Add(-time.Hour), wantStat: string(domain.ToolApprovalRejected)},
		{name: "pending expired terminal", status: domain.ToolApprovalPending, expires: time.Now().Add(-time.Minute), wantStat: string(domain.ToolApprovalExpired)},
		{name: "approved expired terminal", status: domain.ToolApprovalApproved, expires: time.Now().Add(-time.Minute), wantStat: string(domain.ToolApprovalExpired)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{role: "member"})
			repo.row.Status = string(tc.status)
			repo.row.ExpiresAt = tc.expires
			payload, status, err := svc.TerminalResumePayload(context.Background(), "tenant-1", "approval-1")
			require.NoError(t, err, "终态行必须放行（H1：不过期门控），工具反正被 ApprovedPayload 拒绝")
			require.Equal(t, tc.wantStat, status)
			require.Equal(t, "user-1", payload.UserID, "解密 payload 须与 row.UserID 一致")
		})
	}
}

func TestTerminalResumePayloadNonTerminalStates(t *testing.T) {
	tests := []struct {
		name    string
		status  domain.ToolApprovalStatus
		expires time.Time
		wantErr error
	}{
		{name: "pending unexpired keeps waiting", status: domain.ToolApprovalPending, expires: time.Now().Add(time.Minute), wantErr: ErrApprovalNotApproved},
		{name: "invalidated", status: domain.ToolApprovalInvalidated, expires: time.Now().Add(time.Minute), wantErr: domain.ErrApprovalInvalidated},
		{name: "voided unexpired", status: domain.ToolApprovalVoided, expires: time.Now().Add(time.Minute), wantErr: ErrApprovalNotApproved},
		{name: "executing never released", status: domain.ToolApprovalExecuting, expires: time.Now().Add(time.Minute), wantErr: ErrApprovalNotApproved},
		{name: "executed never released", status: domain.ToolApprovalExecuted, expires: time.Now().Add(time.Minute), wantErr: ErrApprovalNotApproved},
		{name: "approved not terminal", status: domain.ToolApprovalApproved, expires: time.Now().Add(time.Minute), wantErr: ErrApprovalNotApproved},
		{name: "outcome unknown", status: domain.ToolApprovalOutcomeUnknown, expires: time.Now().Add(-time.Hour), wantErr: ErrApprovalOutcomeUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{role: "member"})
			repo.row.Status = string(tc.status)
			repo.row.ExpiresAt = tc.expires
			_, _, err := svc.TerminalResumePayload(context.Background(), "tenant-1", "approval-1")
			require.ErrorIs(t, err, tc.wantErr, "非终态状态必须报对应错误，绝不放行")
		})
	}
}

func TestTerminalResumePayloadBindingMismatch(t *testing.T) {
	svc, repo := newApprovalServiceWithRow(t, &fakeRoleResolver{role: "member"})
	repo.row.Status = string(domain.ToolApprovalCancelled)
	repo.row.ToolName = "other-tool" // 篡改绑定字段 → mismatch
	_, _, err := svc.TerminalResumePayload(context.Background(), "tenant-1", "approval-1")
	require.ErrorIs(t, err, ErrApprovalBindingMismatch)
	require.Contains(t, err.Error(), "tool_name")
}

func TestTerminalResumePayloadNotFound(t *testing.T) {
	repo := &approvalRepoFake{getErr: domain.ErrApprovalNotFound, getFailFirst: true}
	svc := NewToolApprovalService(repo, nil, crypto.DeriveAESKey("test-key"))
	_, _, err := svc.TerminalResumePayload(context.Background(), "tenant-1", "missing")
	require.ErrorIs(t, err, domain.ErrApprovalNotFound)
}
