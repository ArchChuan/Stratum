package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// activeExecChatStub 是 GetActiveExecution 的 ChatRepo fake：按用例控制会话
// 归属与读取错误（GetConversation 语义：不存在返回 domain.ErrNotFound）。
type activeExecChatStub struct {
	conv *domain.ChatConversation
	err  error
}

func (s activeExecChatStub) CreateConversation(context.Context, string, string, string, string, string) (*domain.ChatConversation, error) {
	return nil, nil
}
func (s activeExecChatStub) GetConversation(context.Context, string, string) (*domain.ChatConversation, error) {
	return s.conv, s.err
}
func (s activeExecChatStub) ListConversations(context.Context, string, string, string) ([]*domain.ChatConversation, error) {
	return nil, nil
}
func (s activeExecChatStub) RenameConversation(context.Context, string, string, string, string) error {
	return nil
}
func (s activeExecChatStub) DeleteConversation(context.Context, string, string, string) error {
	return nil
}
func (s activeExecChatStub) AddMessage(context.Context, string, *domain.ChatMessage) error {
	return nil
}
func (s activeExecChatStub) ListMessages(context.Context, string, string, string) ([]*domain.ChatMessage, error) {
	return nil, nil
}
func (s activeExecChatStub) CleanupExpired(context.Context, string) error        { return nil }
func (s activeExecChatStub) DeleteByAgent(context.Context, string, string) error { return nil }

// activeExecCheckpointStub 是 CheckpointRepo fake：控制活跃查询结果与错误，
// 并记录 Upsert 调用供 ensureInitialCheckpoint 断言。
type activeExecCheckpointStub struct {
	cp       *domain.AgentExecutionCheckpoint
	err      error
	upserted []domain.AgentExecutionCheckpoint
}

func (s *activeExecCheckpointStub) Upsert(_ context.Context, _ string, cp domain.AgentExecutionCheckpoint) error {
	s.upserted = append(s.upserted, cp)
	return nil
}
func (s *activeExecCheckpointStub) GetLatest(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (s *activeExecCheckpointStub) MarkCompleted(context.Context, string, string) error { return nil }
func (s *activeExecCheckpointStub) UpdateStatus(context.Context, string, string, string) error {
	return nil
}
func (s *activeExecCheckpointStub) DeleteExpired(context.Context, string) (int64, error) {
	return 0, nil
}
func (s *activeExecCheckpointStub) GetLatestActiveByConversation(context.Context, string, string) (*domain.AgentExecutionCheckpoint, error) {
	return s.cp, s.err
}
func (s *activeExecCheckpointStub) UpdateStatusFrom(context.Context, string, string, string, string) error {
	return nil
}
func (s *activeExecCheckpointStub) AdvanceRunGeneration(context.Context, string, string, int) error {
	return nil
}
func (s *activeExecCheckpointStub) Terminate(context.Context, string, string, string) error {
	return nil
}

func newActiveExecService(chat ChatStore, cp CheckpointStore, role string) *AgentService {
	return NewAgentService(AgentServiceDeps{
		ChatStore:          chat,
		CheckpointStore:    cp,
		TenantRoleResolver: stubTenantRole{role: role},
		Logger:             zap.NewNop(),
	})
}

func TestGetActiveExecution_OwnerReadsFreshRunning(t *testing.T) {
	now := time.Now()
	chat := activeExecChatStub{conv: &domain.ChatConversation{ID: "conv-1", UserID: "user-1"}}
	cp := &activeExecCheckpointStub{cp: &domain.AgentExecutionCheckpoint{
		ExecutionID: "exec-1", AgentID: "agent-1", Status: "running",
		UserQuery: "帮我查一下订单", UpdatedAt: now,
	}}
	svc := newActiveExecService(chat, cp, "member")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.NoError(t, err)
	require.NotNil(t, active)
	require.Equal(t, "exec-1", active.ExecutionID)
	require.Equal(t, "running", active.Status)
	// B1：user_query 从 checkpoint 新列读取，不解析 messages snapshot。
	require.Equal(t, "帮我查一下订单", active.UserQuery)
	require.Equal(t, "", active.ApprovalID)
}

func TestGetActiveExecution_WaitingApprovalExposesApprovalID(t *testing.T) {
	now := time.Now()
	chat := activeExecChatStub{conv: &domain.ChatConversation{ID: "conv-1", UserID: "user-1"}}
	cp := &activeExecCheckpointStub{cp: &domain.AgentExecutionCheckpoint{
		ExecutionID: "exec-1", AgentID: "agent-1", Status: "waiting_approval",
		RuntimeStateJSON: []byte(`{"approval_id":"approval-9"}`),
		UpdatedAt:        now,
	}}
	svc := newActiveExecService(chat, cp, "member")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.NoError(t, err)
	require.NotNil(t, active)
	require.Equal(t, "waiting_approval", active.Status)
	require.Equal(t, "approval-9", active.ApprovalID)
}

func TestGetActiveExecution_MemberNonOwnerClosed(t *testing.T) {
	// 非归属 member：统一 404-none，关闭存在性 oracle。
	chat := activeExecChatStub{conv: &domain.ChatConversation{ID: "conv-1", UserID: "owner-1"}}
	cp := &activeExecCheckpointStub{cp: &domain.AgentExecutionCheckpoint{ExecutionID: "exec-1", Status: "running"}}
	svc := newActiveExecService(chat, cp, "member")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.NoError(t, err)
	require.Nil(t, active)
}

func TestGetActiveExecution_AdminCrossOwnerAllowed(t *testing.T) {
	chat := activeExecChatStub{conv: &domain.ChatConversation{ID: "conv-1", UserID: "owner-1"}}
	cp := &activeExecCheckpointStub{cp: &domain.AgentExecutionCheckpoint{
		ExecutionID: "exec-1", AgentID: "agent-1", Status: "running", UserQuery: "q",
	}}
	svc := newActiveExecService(chat, cp, "admin")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.NoError(t, err)
	require.NotNil(t, active)
	require.Equal(t, "exec-1", active.ExecutionID)
}

func TestGetActiveExecution_ConversationMissingIsNone(t *testing.T) {
	chat := activeExecChatStub{err: domain.ErrNotFound}
	cp := &activeExecCheckpointStub{}
	svc := newActiveExecService(chat, cp, "member")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.NoError(t, err)
	require.Nil(t, active)
}

func TestGetActiveExecution_NoCheckpointIsNone(t *testing.T) {
	chat := activeExecChatStub{conv: &domain.ChatConversation{ID: "conv-1", UserID: "user-1"}}
	cp := &activeExecCheckpointStub{cp: nil, err: nil}
	svc := newActiveExecService(chat, cp, "member")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.NoError(t, err)
	require.Nil(t, active)
}

func TestGetActiveExecution_ConversationReadErrorPropagates(t *testing.T) {
	// SECURITY-MEDIUM-1：瞬态 DB 读取失败必须作为错误上抛（500），
	// 不得折叠成 404-none 让前端发起重复执行。
	chat := activeExecChatStub{err: errors.New("connection reset")}
	cp := &activeExecCheckpointStub{}
	svc := newActiveExecService(chat, cp, "member")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.Error(t, err)
	require.Nil(t, active)
}

func TestGetActiveExecution_CheckpointReadErrorPropagates(t *testing.T) {
	// SECURITY-MEDIUM-1：checkpoint 读失败同样 500，不折叠 404。
	chat := activeExecChatStub{conv: &domain.ChatConversation{ID: "conv-1", UserID: "user-1"}}
	cp := &activeExecCheckpointStub{err: errors.New("connection reset")}
	svc := newActiveExecService(chat, cp, "member")

	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.Error(t, err)
	require.Nil(t, active)
}

func TestGetActiveExecution_StoreNotWiredIsNone(t *testing.T) {
	// 未装配 store：降级为无活跃执行，不 panic。
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	active, err := svc.GetActiveExecution(context.Background(), "t1", "conv-1", "user-1")
	require.NoError(t, err)
	require.Nil(t, active)
}

func TestEnsureInitialCheckpoint_WritesRunningInitWithQuery(t *testing.T) {
	cp := &activeExecCheckpointStub{}
	svc := newActiveExecService(nil, cp, "member")
	meta := ExecMeta{TenantID: "t1", TraceID: "trace-1"} // ExecutionID 空 = 全新执行
	req := ExecRequest{Query: "查一下今天的报表", ConversationID: "conv-1", UserID: "user-1"}

	svc.ensureInitialCheckpoint(context.Background(), meta, req, "agent-1", "exec-1")

	require.Len(t, cp.upserted, 1)
	got := cp.upserted[0]
	require.Equal(t, "exec-1", got.ExecutionID)
	require.Equal(t, "running", got.Status)
	require.Equal(t, "init", got.ResumeReason)
	require.Equal(t, "查一下今天的报表", got.UserQuery)
	require.Equal(t, 1, got.RunGeneration)
	require.Equal(t, "user-1", got.UserID)
}

func TestEnsureInitialCheckpoint_ContinuationDoesNotTouchCheckpoint(t *testing.T) {
	cp := &activeExecCheckpointStub{}
	svc := newActiveExecService(nil, cp, "member")
	meta := ExecMeta{TenantID: "t1", TraceID: "trace-1", ExecutionID: "exec-1"} // 续接
	req := ExecRequest{Query: "继续", ConversationID: "conv-1", UserID: "user-1"}

	svc.ensureInitialCheckpoint(context.Background(), meta, req, "agent-1", "exec-1")

	require.Len(t, cp.upserted, 0)
}

func TestEnsureInitialCheckpoint_NoStoreIsNoop(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{Logger: zap.NewNop()})
	meta := ExecMeta{TenantID: "t1", TraceID: "trace-1"}
	req := ExecRequest{Query: "q", UserID: "user-1"}
	svc.ensureInitialCheckpoint(context.Background(), meta, req, "agent-1", "exec-1") // 不 panic
}
