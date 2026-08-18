package application

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// noAnswerMetrics captures IncNoAnswer invocations for assertion.
type noAnswerMetrics struct {
	observability.NoopMetrics
	reasons []string // tenant:reason
}

func (m *noAnswerMetrics) IncNoAnswer(tenantID, reason string) {
	m.reasons = append(m.reasons, tenantID+":"+reason)
}

func TestQueryNoAnswerAccessRestricted(t *testing.T) {
	vectors := NewMockVectorStore()
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.9},
	})
	ws := &domain.Workspace{ID: "workspace-1", Name: "docs", CreatedBy: "owner-1"}
	s := vectorRAGService(vectors)
	s.SetWorkspaceRepo(&deleteWorkspaceRepo{workspace: ws})
	s.SetTenantRoleResolver(stubTenantRole{role: "member"})
	// member 可见集为空：即使向量库有命中，也不允许触碰 Milvus。
	s.SetDocRepo(&recordingDocRepo{ids: []string{}})

	got, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "vector",
		ViewerID: "user-1", TopK: 2, EmbeddingModel: "embedding-3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoAnswer == nil || got.NoAnswer.Reason != NoAnswerAccessRestricted {
		t.Fatalf("noAnswer = %+v, want access_restricted", got.NoAnswer)
	}
	if len(got.Sources) != 0 {
		t.Fatalf("sources must stay empty: %+v", got.Sources)
	}
	if got.BestScore != 0 || got.CandidateCount != 0 {
		t.Fatalf("stats must be zero without retrieval: best=%f count=%d", got.BestScore, got.CandidateCount)
	}
}

func TestQueryNoAnswerNoSources(t *testing.T) {
	// 空向量库：检索无命中。
	s := vectorRAGService(NewMockVectorStore())
	got, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "vector",
		ViewerID: "user-1", TopK: 2, EmbeddingModel: "embedding-3", SkipAccessCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoAnswer == nil || got.NoAnswer.Reason != NoAnswerNoSources {
		t.Fatalf("noAnswer = %+v, want no_sources", got.NoAnswer)
	}
	if got.BestScore != 0 || got.CandidateCount != 0 {
		t.Fatalf("empty pool stats = best %f count %d, want 0", got.BestScore, got.CandidateCount)
	}
}

func TestQueryNoAnswerThresholdFiltered(t *testing.T) {
	vectors := NewMockVectorStore()
	// MockVectorStore.Score 是 L2 距离，入池后经 l2ToSim（1/(1+d)）转换：
	// 1.5/2.0/3.0 -> 0.4/0.333/0.25，全部低于阈值 0.5。
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 1.5},
		{ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 2.0},
		{ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 3.0},
	})
	s := vectorRAGService(vectors)
	got, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "vector",
		ViewerID: "user-1", TopK: 2, EmbeddingModel: "embedding-3", SkipAccessCheck: true,
		ScoreThreshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoAnswer == nil || got.NoAnswer.Reason != NoAnswerThresholdFiltered {
		t.Fatalf("noAnswer = %+v, want threshold_filtered", got.NoAnswer)
	}
	if got.NoAnswer.RetrievedCount != 3 || got.NoAnswer.FilteredCount != 3 {
		t.Fatalf("counts = (%d,%d), want (3,3)", got.NoAnswer.RetrievedCount, got.NoAnswer.FilteredCount)
	}
	// BestScore 必须在过滤前采集：入口池最高 sim 0.4 而非阈值 0.5。
	if got.NoAnswer.BestScore != 0.4 || got.BestScore != 0.4 {
		t.Fatalf("bestScore = %f/%f, want 0.4 (pre-filter pool max)", got.NoAnswer.BestScore, got.BestScore)
	}
}

func TestQueryNoAnswerUnsupportedMode(t *testing.T) {
	// graph 模式（AllowedQueryModes 含 graph 但检索器未实现）落 default case。
	s := vectorRAGService(NewMockVectorStore())
	got, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "graph",
		ViewerID: "user-1", TopK: 2, EmbeddingModel: "embedding-3", SkipAccessCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoAnswer == nil || got.NoAnswer.Reason != NoAnswerUnsupportedMode {
		t.Fatalf("noAnswer = %+v, want unsupported_mode", got.NoAnswer)
	}
}

func TestQueryNoAnswerMetricEmitted(t *testing.T) {
	vectors := NewMockVectorStore()
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.9},
	})
	metrics := &noAnswerMetrics{}
	s := vectorRAGService(vectors)
	s.SetMetrics(metrics)

	got, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "vector",
		ViewerID: "user-1", TopK: 2, EmbeddingModel: "embedding-3", SkipAccessCheck: true,
		ScoreThreshold: 0.95,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoAnswer == nil {
		t.Fatal("want no-answer result")
	}
	if len(metrics.reasons) != 1 || metrics.reasons[0] != "tenant-1:threshold_filtered" {
		t.Fatalf("metrics = %v, want [tenant-1:threshold_filtered]", metrics.reasons)
	}
}

// TestQueryBestScoreAlwaysFilled 是 P0 断点回归：有答案路径也必须常驻
// BestScore（校准数据源），且与 NoAnswer nil 解耦。
func TestQueryBestScoreAlwaysFilled(t *testing.T) {
	vectors := NewMockVectorStore()
	// L2 距离 0.9/0.1 -> sim 0.526/0.909。
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.9},
		{ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.1},
	})
	s := vectorRAGService(vectors)
	got, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "vector",
		ViewerID: "user-1", TopK: 2, EmbeddingModel: "embedding-3", SkipAccessCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoAnswer != nil {
		t.Fatalf("noAnswer = %+v, want nil (has answer)", got.NoAnswer)
	}
	if len(got.Sources) != 2 {
		t.Fatalf("sources = %+v", got.Sources)
	}
	if got.BestScore != l2ToSim(0.1) {
		t.Fatalf("bestScore = %f, want %f (pool max on the answer path)", got.BestScore, l2ToSim(0.1))
	}
	if got.CandidateCount != 2 {
		t.Fatalf("candidateCount = %d, want 2", got.CandidateCount)
	}
	// 阈值过滤后推导 max(score) 恒 >= threshold —— 必须用入口池统计，验证
	// 截断后的 sources 分数不再是 BestScore。
	vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
		{ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.9},
		{ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.1},
		{ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 0.01},
	})
	got2, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "vector",
		ViewerID: "user-1", TopK: 1, EmbeddingModel: "embedding-3", SkipAccessCheck: true,
		ScoreThreshold: 0.5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got2.BestScore != l2ToSim(0.01) {
		t.Fatalf("bestScore after threshold = %f, want %f (entry pool, not filtered sources)", got2.BestScore, l2ToSim(0.01))
	}
	if len(got2.Sources) != 1 || got2.NoAnswer != nil {
		t.Fatalf("sources=%+v noAnswer=%+v, want one source and nil signal", got2.Sources, got2.NoAnswer)
	}
}

func TestQueryNoAnswerNilMetricsSafe(t *testing.T) {
	// NewRAGService 不注入 metrics：无答案路径必须 nil-safe，不得 panic。
	s := NewRAGService(&mockEmbedder{dim: 3}, NewMockVectorStore(), zap.NewNop())
	got, err := s.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "q", Mode: "vector",
		ViewerID: "user-1", TopK: 2, EmbeddingModel: "embedding-3", SkipAccessCheck: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NoAnswer == nil {
		t.Fatal("want no-answer signal from empty store")
	}
}
