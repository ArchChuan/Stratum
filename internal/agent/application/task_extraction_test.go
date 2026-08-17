package application

import (
	"context"
	"sync"
	"testing"
	"time"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// mockTaskRepo 记录调用并返回可控结果，供挂点测试。
type mockTaskRepo struct {
	mu                 sync.Mutex
	claimCalls         int
	saveCalls          int
	claimedGen         int64
	claimedTask        *domain.Task
	claimOK            bool
	claimErr           error
	saveErr            error
	latestActive       *domain.Task
	latestErr          error
	detachCalls        int
	deleteExpired      int64
	deleteExpiredCalls int
}

func (m *mockTaskRepo) Claim(ctx context.Context, tenantID, taskID, conversationID string, lease time.Duration) (*domain.Task, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimCalls++
	if m.claimErr != nil {
		return nil, false, m.claimErr
	}
	if !m.claimOK {
		return nil, false, nil
	}
	t := *m.claimedTask
	t.Generation = m.claimedGen
	return &t, true, nil
}

func (m *mockTaskRepo) Save(ctx context.Context, tenantID string, task domain.Task, expectedGeneration int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saveCalls++
	return m.saveErr
}

func (m *mockTaskRepo) Get(ctx context.Context, tenantID, taskID string) (*domain.Task, error) {
	return nil, nil
}

func (m *mockTaskRepo) GetLatestActiveForOwner(ctx context.Context, tenantID, agentID, userID string) (*domain.Task, error) {
	return m.latestActive, m.latestErr
}

func (m *mockTaskRepo) DetachConversation(ctx context.Context, tenantID, conversationID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detachCalls++
	return nil
}

func (m *mockTaskRepo) DeleteExpired(ctx context.Context, tenantID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteExpiredCalls++
	return m.deleteExpired, nil
}

func TestPersistTaskSnapshotUpdatesClaimedTask(t *testing.T) {
	repo := &mockTaskRepo{
		claimOK: true, claimedGen: 3,
		claimedTask: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1", Generation: 2},
	}
	agent := &BaseAgent{Logger: zap.NewNop(), TaskStore: repo}
	result := &AgentResult{Metadata: map[string]interface{}{}}
	plan := &domain.Plan{ID: "plan-1", Status: domain.PlanStatusActive,
		Nodes: []domain.PlanNode{{ID: "n1", Goal: "迁移", Status: domain.PlanNodeStatusSucceeded},
			{ID: "n2", Goal: "验证", Status: domain.PlanNodeStatusPending}}}
	state := reActStateForTest(plan, false)

	agent.persistTaskSnapshot(context.Background(), agentExecContextForTest(), state, result)

	repo.mu.Lock()
	defer repo.mu.Unlock()
	if repo.saveCalls != 1 {
		t.Fatalf("save calls: got %d want 1", repo.saveCalls)
	}
	if _, ok := result.Metadata[constants.TaskMetadataKey]; !ok {
		t.Fatal("task snapshot should be written to result.Metadata")
	}
}

func TestPersistTaskSnapshotCompleteFlag(t *testing.T) {
	repo := &mockTaskRepo{claimOK: true, claimedGen: 1,
		claimedTask: &domain.Task{ID: "plan-1", Generation: 0}}
	agent := &BaseAgent{Logger: zap.NewNop(), TaskStore: repo}
	result := &AgentResult{}
	plan := &domain.Plan{ID: "plan-1", Status: domain.PlanStatusActive,
		Nodes: []domain.PlanNode{{ID: "n1", Goal: "迁移", Status: domain.PlanNodeStatusPending}}}
	state := reActStateForTest(plan, true)

	agent.persistTaskSnapshot(context.Background(), agentExecContextForTest(), state, result)

	if got := result.Metadata[constants.TaskMetadataCompleteKey]; got != true {
		t.Fatalf("complete flag: got %v want true", got)
	}
}

func TestPersistTaskSnapshotNilStoreNoop(t *testing.T) {
	agent := &BaseAgent{Logger: zap.NewNop()} // TaskStore nil
	result := &AgentResult{}
	plan := &domain.Plan{ID: "plan-1", Status: domain.PlanStatusActive,
		Nodes: []domain.PlanNode{{ID: "n1", Goal: "迁移", Status: domain.PlanNodeStatusSucceeded}}}
	state := reActStateForTest(plan, false)
	agent.persistTaskSnapshot(context.Background(), agentExecContextForTest(), state, result) // 不 panic
}

func reActStateForTest(plan *domain.Plan, complete bool) agentgraph.ReActState {
	return agentgraph.ReActState{ActivePlan: plan, TaskCompleteRequested: complete}
}

func agentExecContextForTest() agentExecContext {
	return agentExecContext{
		agentID: "agent-1",
		cfg:     &ExecutionConfig{TenantID: "tenant-1", UserID: "user-1", ExecutionID: "exec-1", ConversationID: "11111111-1111-1111-1111-111111111111"},
	}
}
