package application_test

import (
	"context"
	"sync"
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/application"
	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// executeStore 包装 memoryStore，补上 Execute 主循环所需的
// EventRepository 与 runController（ControlRun），并记录审批创建调用，
// 用于断言 agent 审批暂停不创建 workflow_approvals。
type executeStore struct {
	*memoryStore
	mu                 sync.Mutex
	events             []domain.Event
	approvalCalls      int
	publishedVersionID string
}

func (s *executeStore) AppendEvent(_ context.Context, _ string, event domain.Event) (domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return event, nil
}

func (s *executeStore) ListEvents(_ context.Context, _, _ string, _ int64, _ int) ([]domain.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.Event(nil), s.events...), nil
}

func (s *executeStore) ControlRun(ctx context.Context, tenantID, runID string, expected int64, status domain.RunStatus, reason string, _ domain.Event) error {
	// 走 memoryStore.GetRun/UpdateRun（自带锁 + 值拷贝）完成 CAS 转换：不直接修改
	// map 内共享的 *domain.Run，避免与 watchBatchControl 轮询的 GetRun 并发读写
	// 同一对象触发 -race 数据竞争（GetRun 拷贝与原地写无共同锁）。
	run, err := s.memoryStore.GetRun(ctx, tenantID, runID)
	if err != nil {
		return err
	}
	if run.Generation != expected {
		return domain.ErrGenerationConflict
	}
	if !controlTransitionAllowed(run.Status, status) {
		return domain.ErrInvalidTransition
	}
	run.Status = status
	if status == domain.RunStatusPaused {
		run.PauseReason = reason
	}
	run.Generation++
	return s.memoryStore.UpdateRun(ctx, tenantID, run)
}

// controlTransitionAllowed 复刻 PgStore.ControlRun 的状态机门禁，防止 fake 与真实
// store 语义漂移。paused 允许来自 pause_requested 与 running：后者是 agent 原生
// 审批暂停（无 pause_requested 前置，直接 running→paused 收敛），与 store 修复后
// 的 WHEN 'paused' CASE 保持一致。
func controlTransitionAllowed(current, target domain.RunStatus) bool {
	switch target {
	case domain.RunStatusPauseRequested:
		return current == domain.RunStatusQueued || current == domain.RunStatusRunning
	case domain.RunStatusCancelRequested:
		return current == domain.RunStatusQueued || current == domain.RunStatusRunning ||
			current == domain.RunStatusPauseRequested || current == domain.RunStatusPaused ||
			current == domain.RunStatusManualIntervention
	case domain.RunStatusQueued:
		return current == domain.RunStatusPaused || current == domain.RunStatusManualIntervention
	case domain.RunStatusPaused:
		return current == domain.RunStatusPauseRequested || current == domain.RunStatusRunning
	case domain.RunStatusCanceled:
		return current == domain.RunStatusCancelRequested
	case domain.RunStatusManualIntervention:
		return current == domain.RunStatusRunning
	}
	return false
}

func (s *executeStore) CreateApproval(_ context.Context, _ string, _ domain.Approval, _ domain.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvalCalls++
	return nil
}

func (s *executeStore) ListApprovals(_ context.Context, _, _ string, _ bool) ([]domain.Approval, error) {
	return nil, nil
}

// approvalRequiredRegistry 首次调用模拟 adapter 已翻译好的 agent 原生审批暂停
// （Paused + agent_approval_required），恢复后重跑返回成功输出。
type approvalRequiredRegistry struct {
	mu    sync.Mutex
	calls int
}

func (r *approvalRequiredRegistry) Execute(_ context.Context, request port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 {
		return port.NodeExecutionResult{Paused: true, ErrorCode: "agent_approval_required"}, nil
	}
	return port.NodeExecutionResult{Output: "done", TraceID: "trace-" + request.Node.ID}, nil
}

// blockingAgentRegistry 阻塞直到批上下文被取消（模拟运行中的长耗 agent 节点），
// 返回 context.Canceled 走 commitCanceledOutcome 的暂停边界收敛。
type blockingAgentRegistry struct {
	once    sync.Once
	started chan struct{}
}

func (r *blockingAgentRegistry) Execute(ctx context.Context, _ port.NodeExecutionRequest) (port.NodeExecutionResult, error) {
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	return port.NodeExecutionResult{}, ctx.Err()
}

// approvalResolverFake 可脚本化 agent 审批是否已全部终态（done）。
type approvalResolverFake struct{ done bool }

func (r approvalResolverFake) ResolveAgentApproval(_ context.Context, _, _ string) (bool, error) {
	return r.done, nil
}

// newExecuteService 构造可执行单 agent 节点 workflow 的 RunService。
func newExecuteService(t *testing.T, executors port.NodeExecutorRegistry) (*executeStore, *application.RunService) {
	t.Helper()
	store := &executeStore{memoryStore: newMemoryStore()}
	definitions := application.NewDefinitionService(store, store, (&ids{}).NewID)
	def, err := definitions.Create(context.Background(), "t1", application.CreateDefinitionCommand{
		Name: "Pause", Spec: domain.Spec{Nodes: []domain.Node{{ID: "one", Type: domain.NodeTypeAgent, AgentID: "agent-1"}}},
	}, "u-1")
	require.NoError(t, err)
	version, err := definitions.Publish(context.Background(), "t1", def.ID, "u-1")
	require.NoError(t, err)
	store.publishedVersionID = version.ID
	runs := application.NewRunServiceWithRegistry(store, store, executors, (&ids{}).NewID, zap.NewNop())
	return store, runs
}

func (s *executeStore) start(ctx context.Context, t *testing.T, runs *application.RunService) *domain.Run {
	t.Helper()
	run, _, err := runs.Start(ctx, "t1", application.StartRunCommand{VersionID: s.publishedVersionID, Input: map[string]any{"task": "hello"}, IdempotencyKey: "pause-" + s.publishedVersionID})
	require.NoError(t, err)
	return run
}

func TestExecuteAgentApprovalPendingPausesRunWithoutWorkflowApprovals(t *testing.T) {
	store, runs := newExecuteService(t, &approvalRequiredRegistry{})
	run := store.start(context.Background(), t, runs)
	// 首次执行：agent 原生审批待决 → 节点 paused 且 run 收敛到 paused。
	require.NoError(t, runs.Execute(context.Background(), "t1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "t1", run.ID, adminActor())
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusPaused, got.Status)
	require.Len(t, attempts, 1)
	require.Equal(t, domain.AttemptStatusPaused, attempts[0].Status)
	require.Equal(t, "agent_approval_required", attempts[0].ErrorCode)
	// 复用 agent 原生 tool_approvals 审批流：不创建 workflow_approvals 包装行。
	store.mu.Lock()
	approvalCalls := store.approvalCalls
	store.mu.Unlock()
	require.Zero(t, approvalCalls, "agent 审批暂停不得创建 workflow_approvals")
}

func TestExecuteAgentApprovalReconcileResumesWhenApproved(t *testing.T) {
	store, runs := newExecuteService(t, &approvalRequiredRegistry{})
	runs.SetAgentApprovalResolver(approvalResolverFake{done: true})
	run := store.start(context.Background(), t, runs)
	require.NoError(t, runs.Execute(context.Background(), "t1", run.ID))

	// 审批已终态：resume（paused→queued）后 reconcile 把 paused attempt 转
	// RetryWait（RetryAt=nil 立即重跑），重跑同一节点续跑并完成。
	fresh, err := store.GetRun(context.Background(), "t1", run.ID)
	require.NoError(t, err)
	require.NoError(t, store.ControlRun(context.Background(), "t1", run.ID, fresh.Generation, domain.RunStatusQueued, "", domain.Event{}))
	require.NoError(t, runs.Execute(context.Background(), "t1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "t1", run.ID, adminActor())
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusCompleted, got.Status)
	succeeded := false
	for _, attempt := range attempts {
		if attempt.Status == domain.AttemptStatusSucceeded {
			succeeded = true
		}
	}
	require.True(t, succeeded, "审批通过后重跑节点应成功")
}

func TestExecuteAgentApprovalReconcileWaitsWhilePending(t *testing.T) {
	store, runs := newExecuteService(t, &approvalRequiredRegistry{})
	runs.SetAgentApprovalResolver(approvalResolverFake{done: false})
	run := store.start(context.Background(), t, runs)
	require.NoError(t, runs.Execute(context.Background(), "t1", run.ID))

	// 审批仍 pending：resume 后 reconcile 把 attempt 转 RetryWait + 未来轮询，
	// run 收敛回 queued，等下一 tick 再判，绝不提前放行重跑。
	fresh, err := store.GetRun(context.Background(), "t1", run.ID)
	require.NoError(t, err)
	require.NoError(t, store.ControlRun(context.Background(), "t1", run.ID, fresh.Generation, domain.RunStatusQueued, "", domain.Event{}))
	require.NoError(t, runs.Execute(context.Background(), "t1", run.ID))
	got, attempts, err := runs.Get(context.Background(), "t1", run.ID, adminActor())
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusQueued, got.Status)
	require.Len(t, attempts, 1)
	require.Equal(t, domain.AttemptStatusRetryWait, attempts[0].Status)
	require.NotNil(t, attempts[0].RetryAt, "审批未决应安排未来轮询，不得立即重跑")
}

func TestExecuteBatchPauseRequestedCancelsRunningAgentNode(t *testing.T) {
	agent := &blockingAgentRegistry{started: make(chan struct{})}
	store, runs := newExecuteService(t, agent)
	run := store.start(context.Background(), t, runs)

	execDone := make(chan error, 1)
	go func() { execDone <- runs.Execute(context.Background(), "t1", run.ID) }()
	<-agent.started // 等 agent 节点真正进入执行，确保走批内检测而非边界收敛。

	// 执行人在运行中心暂停：批内 watchBatchControl 轮询到 pause_requested
	// → 取消批上下文 → 运行中 agent 节点 ctx 取消 → checkpoint 保留可恢复 → run paused。
	fresh, err := store.GetRun(context.Background(), "t1", run.ID)
	require.NoError(t, err)
	require.NoError(t, store.ControlRun(context.Background(), "t1", run.ID, fresh.Generation, domain.RunStatusPauseRequested, "user paused", domain.Event{}))
	require.NoError(t, <-execDone)

	got, attempts, err := runs.Get(context.Background(), "t1", run.ID, adminActor())
	require.NoError(t, err)
	require.Equal(t, domain.RunStatusPaused, got.Status)
	require.Len(t, attempts, 1)
	require.Equal(t, domain.AttemptStatusRetryWait, attempts[0].Status)
	require.Nil(t, attempts[0].RetryAt, "暂停边界留 RetryAt=nil，resume 后立即重跑同 executionID 经 checkpoint 续跑")
}
