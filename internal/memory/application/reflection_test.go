package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type stubReflector struct {
	entries []*port.ReflectionEntry
	err     error
	called  bool
}

func (s *stubReflector) Reflect(context.Context, string, domain.TrajectorySkeleton, string) ([]*port.ReflectionEntry, error) {
	s.called = true
	return s.entries, s.err
}

func reflectionTask(t *testing.T, steps int, explicit bool) *port.ReflectionTask {
	t.Helper()
	calls := make([]ToolCallInput, 0, steps)
	for i := 0; i < steps; i++ {
		calls = append(calls, ToolCallInput{ToolName: "search", Status: domain.TrajectoryStepStatusSuccess})
	}
	sk, err := BuildTrajectorySkeleton("exec-1", "goal", "result", "", calls)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(sk)
	if err != nil {
		t.Fatal(err)
	}
	return &port.ReflectionTask{
		TenantID: "tenant1", UserID: "user1", AgentID: "agent1", ConversationID: "conv1",
		Scope: "user", ExecutionID: "exec-1", Skeleton: raw, ExplicitMemory: explicit,
	}
}

// TestReflectAndPersist_SkipsLowValueTask 验证触发 gate：单次成功查询不调用
// 反思模型、不产生任何写入。
func TestReflectAndPersist_SkipsLowValueTask(t *testing.T) {
	ctx := context.Background()
	svc := NewMemoryService(new(MockFactRepo), new(MockEntityRepo), new(MockExtractionQueue), nil, nil, nil, nil, nil)
	ref := &stubReflector{entries: []*port.ReflectionEntry{{Content: "x", Importance: 0.9, FactType: "other"}}}
	svc.SetTrajectoryReflector(ref)

	if err := svc.ReflectAndPersist(ctx, reflectionTask(t, 1, false)); err != nil {
		t.Fatal(err)
	}
	if ref.called {
		t.Fatal("reflector must not be called for low-value task")
	}
}

// TestReflectAndPersist_ExplicitMemoryTriggers 验证显式"记住"档位在工具数
// 不足时仍触发反思。
func TestReflectAndPersist_ExplicitMemoryTriggers(t *testing.T) {
	ctx := context.Background()
	svc := NewMemoryService(new(MockFactRepo), new(MockEntityRepo), new(MockExtractionQueue), nil, nil, nil, nil, nil)
	ref := &stubReflector{entries: []*port.ReflectionEntry{{Content: "x", Importance: 0.9, FactType: "other"}}}
	svc.SetTrajectoryReflector(ref)

	if err := svc.ReflectAndPersist(ctx, reflectionTask(t, 1, true)); err != nil {
		t.Fatal(err)
	}
	if !ref.called {
		t.Fatal("reflector must be called for explicit memory instruction")
	}
}

// TestReflectAndPersist_EvidenceMismatchDropped 验证证据门：反思条目携带的
// execution_id 与任务不一致时丢弃，不产生写入。
func TestReflectAndPersist_EvidenceMismatchDropped(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	svc := NewMemoryService(factRepo, new(MockEntityRepo), new(MockExtractionQueue), nil, nil, nil, nil, nil)
	ref := &stubReflector{entries: []*port.ReflectionEntry{
		{Content: "hallucinated", Importance: 0.9, FactType: "other", Evidence: port.ReflectionEvidence{ExecutionID: "other-exec"}},
	}}
	svc.SetTrajectoryReflector(ref)

	if err := svc.ReflectAndPersist(ctx, reflectionTask(t, 3, false)); err != nil {
		t.Fatal(err)
	}
	factRepo.AssertNotCalled(t, "FindSupersedeCandidates", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestReflectAndPersist_PersistsValidEntry 验证 happy path：反思候选过证据门后
// 复用事实持久化链入库（supersede → 写入 → 向量）。
func TestReflectAndPersist_PersistsValidEntry(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	vectorStore := new(MockVectorStore)
	embedClient := new(MockEmbedClient)
	svc := NewMemoryService(factRepo, entityRepo, new(MockExtractionQueue), vectorStore, nil, embedClient, nil, nil)

	ref := &stubReflector{entries: []*port.ReflectionEntry{{
		Content:    "调用搜索工具前应先确认查询词",
		Importance: 0.8,
		FactType:   "skill",
		Evidence:   port.ReflectionEvidence{ExecutionID: "exec-1"},
	}}}
	svc.SetTrajectoryReflector(ref)

	factRepo.On("FindSupersedeCandidates", ctx, "tenant1", mock.Anything, "调用搜索工具前应先确认查询词", mock.Anything, mock.Anything).
		Return([]*port.SupersedeCandidate{}, nil)
	factRepo.On("CreateExtracted", ctx, "tenant1", mock.AnythingOfType("*port.ExtractedFactWrite")).
		Return(&domain.MemoryFact{ID: "fact-1", Content: "调用搜索工具前应先确认查询词"}, true, nil)
	embedClient.On("Embed", ctx, "调用搜索工具前应先确认查询词").
		Return([]float32{0.1, 0.2, 0.3}, nil)
	vectorStore.On("Upsert", ctx, "memory_facts_tenant1_text_embedding_v3", mock.AnythingOfType("[]*port.VectorDoc")).
		Return(nil)

	if err := svc.ReflectAndPersist(ctx, reflectionTask(t, 3, false)); err != nil {
		t.Fatal(err)
	}
	factRepo.AssertExpectations(t)
	embedClient.AssertExpectations(t)
	vectorStore.AssertExpectations(t)
}
