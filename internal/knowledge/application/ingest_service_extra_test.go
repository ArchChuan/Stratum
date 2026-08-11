package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// mockChunkRepo 实现 port.ChunkRepo；DeleteByWorkspace 可脚本化错误。
type mockChunkRepo struct {
	deleteErr error
	deleted   []struct{ tenantID, workspaceID string }
}

var _ knowledgeport.ChunkRepo = (*mockChunkRepo)(nil)

func (m *mockChunkRepo) InsertBatch(context.Context, string, string, []domain.Chunk) error {
	return nil
}

func (m *mockChunkRepo) InsertParentBatch(context.Context, string, string, []knowledgeport.ParentChunk) error {
	return nil
}

func (m *mockChunkRepo) GetParentByID(context.Context, string, string, string) (*knowledgeport.ParentChunk, error) {
	return nil, nil
}

func (m *mockChunkRepo) GetChunksByIDs(context.Context, string, string, []string) ([]domain.Chunk, error) {
	return nil, nil
}

func (m *mockChunkRepo) KeywordSearch(context.Context, string, string, string, []string, int) ([]domain.Chunk, error) {
	return nil, nil
}

func (m *mockChunkRepo) CountByWorkspace(context.Context, string, string) (int64, error) {
	return 0, nil
}

func (m *mockChunkRepo) ListByDoc(context.Context, string, string, string) ([]domain.Chunk, error) {
	return nil, nil
}

func (m *mockChunkRepo) DeleteByWorkspace(_ context.Context, tenantID, workspaceID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, struct{ tenantID, workspaceID string }{tenantID, workspaceID})
	return nil
}

func TestIngestBatchSuccess(t *testing.T) {
	parser := &mockParser{out: paragraphInput(2)}
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, newMockDocRepo())

	results, err := ki.IngestBatch(context.Background(), []IngestDocumentRequest{
		req("d1"), req("d2"),
	})
	if err != nil || len(results) != 2 {
		t.Fatalf("batch = %+v, %v", results, err)
	}
	for i, r := range results {
		if r.DocumentID != req("d1").DocumentID && r.DocumentID != req("d2").DocumentID {
			t.Fatalf("unexpected id %q", r.DocumentID)
		}
		if r.Status != constants.IngestStatusProcessing || r.TotalChunks == 0 {
			t.Fatalf("result[%d] = %+v", i, r)
		}
	}
	// 极端情况：空请求列表 → 空结果非 nil。
	results, err = ki.IngestBatch(context.Background(), nil)
	if err != nil || len(results) != 0 || results == nil {
		t.Fatalf("empty batch = %+v, %v", results, err)
	}
	if err := ki.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown = %v", err)
	}
}

func TestIngestBatchSynthesizesFailedResults(t *testing.T) {
	// 极端情况：某条失败 → 该条合成 failed 结果，其余成功。
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())

	results, err := ki.IngestBatch(context.Background(), []IngestDocumentRequest{
		req("good"),
		{TenantID: "t1", WorkspaceID: "wsid-1", DocumentID: "bad", ChunkingStrategy: "bogus"},
	})
	if err != nil || len(results) != 2 {
		t.Fatalf("batch = %+v, %v", results, err)
	}
	if results[0].Status != constants.IngestStatusProcessing {
		t.Fatalf("good item = %+v", results[0])
	}
	if results[1].Status != constants.IngestStatusFailed || len(results[1].Errors) == 0 {
		t.Fatalf("bad item = %+v", results[1])
	}
	if err := ki.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown = %v", err)
	}
}

func TestIngestBatchAllFail(t *testing.T) {
	// 极端情况：全部失败 → 全部 failed 且不 spawn goroutine。
	ki := buildIngest(t, &mockParser{err: errors.New("parse boom")}, &mockEmbedder{dim: 4}, newMockDocRepo())
	results, err := ki.IngestBatch(context.Background(), []IngestDocumentRequest{req("d1"), req("d2")})
	if err != nil || len(results) != 2 {
		t.Fatalf("batch = %+v, %v", results, err)
	}
	for _, r := range results {
		if r.Status != constants.IngestStatusFailed || len(r.Errors) == 0 {
			t.Fatalf("failed item = %+v", r)
		}
	}
	// 无 goroutine 泄漏：Shutdown 立即返回。
	if err := ki.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown = %v", err)
	}
}

func TestDeleteWorkspaceData(t *testing.T) {
	store := NewMockVectorStore()
	chunks := &mockChunkRepo{}
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())
	ki.vectorStore = store
	ki.SetChunkRepo(chunks)

	if err := ki.DeleteWorkspaceData(context.Background(), "t1", "wsid-1"); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if len(chunks.deleted) != 1 || chunks.deleted[0].workspaceID != "wsid-1" {
		t.Fatalf("chunk deletes = %+v", chunks.deleted)
	}
}

func TestDeleteWorkspaceDataVariants(t *testing.T) {
	// 极端情况：无 chunkRepo → 跳过 PG 清理仍成功。
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())
	ki.vectorStore = NewMockVectorStore()
	if err := ki.DeleteWorkspaceData(context.Background(), "t1", "wsid-1"); err != nil {
		t.Fatalf("no chunk repo delete = %v", err)
	}

	// 极端情况：chunkRepo 失败仅告警，不阻断。
	ki.SetChunkRepo(&mockChunkRepo{deleteErr: errors.New("pg down")})
	if err := ki.DeleteWorkspaceData(context.Background(), "t1", "wsid-1"); err != nil {
		t.Fatalf("chunk repo failure must warn only, got %v", err)
	}

	// 极端情况：collection 删除失败 → 错误传播。
	ki.vectorStore = &vectorStoreFailing{}
	if err := ki.DeleteWorkspaceData(context.Background(), "t1", "wsid-1"); err == nil {
		t.Fatal("collection failure must error")
	}
}

func TestGetWorkspaceStats(t *testing.T) {
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())
	ki.vectorStore = NewMockVectorStore()

	stats, err := ki.GetWorkspaceStats(context.Background(), "t1", "wsid-1")
	if err != nil {
		t.Fatalf("stats = %v", err)
	}
	if stats["workspace"] != "wsid-1" || stats["vector_count"] != int64(0) || stats["collection"] != constants.CollectionName("t1", "wsid-1") {
		t.Fatalf("stats = %+v", stats)
	}

	// 极端情况：CountVectors 失败 → 错误传播。
	ki.vectorStore = &vectorStoreFailing{}
	if _, err := ki.GetWorkspaceStats(context.Background(), "t1", "wsid-1"); err == nil {
		t.Fatal("count failure must error")
	}
}

func TestSetMetricsNilIgnored(t *testing.T) {
	// 极端情况：SetMetrics(nil) 保持 Noop，不 panic。
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())
	before := ki.metrics
	ki.SetMetrics(nil)
	if ki.metrics != before {
		t.Fatal("nil metrics must be ignored")
	}
	// 正常注入。
	ki.SetMetrics(observability.NoopMetrics{})
	if _, ok := ki.metrics.(observability.NoopMetrics); !ok {
		t.Fatalf("metrics = %T", ki.metrics)
	}
}

func TestSetChunkRepoAndEmbedResolver(t *testing.T) {
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())
	chunks := &mockChunkRepo{}
	ki.SetChunkRepo(chunks)
	if ki.chunkRepo != chunks {
		t.Fatal("chunk repo must be injected")
	}
	resolver := func(context.Context, string, string) knowledgeport.Embedder { return nil }
	ki.SetEmbedResolver(resolver)
	if ki.embedResolver == nil {
		t.Fatal("embed resolver must be injected")
	}
}

func TestShutdownRespectsContext(t *testing.T) {
	// 极端情况：已取消的 ctx → Shutdown 返回 ctx.Err()。
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ki.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown err = %v", err)
	}
	// 空 wg 正常返回。
	if err := ki.Shutdown(context.Background()); err != nil {
		t.Fatalf("idle shutdown = %v", err)
	}
}

func TestIngestDocumentQueueFull(t *testing.T) {
	// 极端情况：queueSem 打满 → ErrIngestQueueFull。
	parser := &mockParser{out: paragraphInput(2)}
	ki := buildIngest(t, parser, &mockEmbedder{dim: 4}, newMockDocRepo())
	// 占满 queueSem（容量 constants.IngestQueueCapacity），释放后恢复。
	for i := 0; i < constants.IngestQueueCapacity; i++ {
		ki.queueSem <- struct{}{}
	}
	_, err := ki.IngestDocument(context.Background(), req("d1"))
	if !errors.Is(err, domain.ErrIngestQueueFull) {
		t.Fatalf("queue full err = %v", err)
	}
	// 释放后恢复正常。
	<-ki.queueSem
	if _, err := ki.IngestDocument(context.Background(), req("d2")); err != nil {
		t.Fatalf("after release = %v", err)
	}
	if err := ki.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown = %v", err)
	}
}

func TestIngestDocumentUnknownStrategy(t *testing.T) {
	// 极端情况：非法 chunking 策略 → 错误传播。
	ki := buildIngest(t, &mockParser{out: paragraphInput(2)}, &mockEmbedder{dim: 4}, newMockDocRepo())
	r := req("bad")
	r.ChunkingStrategy = "bogus"
	if _, err := ki.IngestDocument(context.Background(), r); err == nil {
		t.Fatal("unknown strategy must fail")
	}
}
