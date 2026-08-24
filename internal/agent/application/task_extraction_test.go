package application

import (
	"context"
	"sync"
	"testing"
	"time"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
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

// TestRegistryHydratesTaskStore 守卫 wiring 装配断链：Registry 注入 TaskStore
// 后，Get/hydrate 返回的 agent 必须携带该仓库，否则 persistTaskSnapshot 与
// 恢复链路在生产恒早退（agent_tasks 永不落库）。
func TestRegistryHydratesTaskStore(t *testing.T) {
	repo := &registryAgentRepoFake{cfg: &domain.AgentConfig{ID: "agent-1", Name: "a"}}
	registry := NewRegistry(repo, zap.NewNop())
	taskStore := &mockTaskRepo{}
	registry.SetTaskStore(taskStore)

	got, ok, err := registry.Get(context.Background(), "agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("agent not found")
	}
	base, isBase := got.(*BaseAgent)
	if !isBase {
		t.Fatalf("expected *BaseAgent, got %T", got)
	}
	if base.TaskStore == nil {
		t.Fatal("TaskStore not hydrated into agent")
	}
	if base.TaskStore != taskStore {
		t.Fatal("TaskStore mismatch: expected registry-injected repo")
	}

	// 未注入时保持 nil（旧行为不回归）。
	registry2 := NewRegistry(repo, zap.NewNop())
	got2, _, err2 := registry2.Get(context.Background(), "agent-1")
	if err2 != nil {
		t.Fatal(err2)
	}
	if _, isBase := got2.(*BaseAgent); isBase && got2.(*BaseAgent).TaskStore != nil {
		t.Fatal("TaskStore should stay nil without SetTaskStore")
	}
}

type registryAgentRepoFake struct {
	cfg *domain.AgentConfig
}

func (r *registryAgentRepoFake) Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error) {
	if r.cfg == nil || r.cfg.ID != id {
		return nil, false, nil
	}
	return r.cfg, true, nil
}

func (r *registryAgentRepoFake) GetAll(ctx context.Context) ([]*domain.AgentConfig, error) {
	if r.cfg == nil {
		return nil, nil
	}
	return []*domain.AgentConfig{r.cfg}, nil
}

func (r *registryAgentRepoFake) Register(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editors []string) error {
	return nil
}

func (r *registryAgentRepoFake) Remove(ctx context.Context, id string, audit *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}

func (r *registryAgentRepoFake) Update(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editorActor string, replaceParams bool) error {
	return nil
}
