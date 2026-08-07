package application

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewKnowledgeIngest(t *testing.T) {
	logger := zap.NewNop()
	ingest := NewKnowledgeIngest(nil, nil, nil, nil, logger)

	if ingest == nil {
		t.Error("expected KnowledgeIngest to be non-nil")
	}
}

func TestNewRAGService(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)

	if service == nil {
		t.Error("expected RAGService to be non-nil")
	}
}

func TestRAGQueryKeywordUsesWorkspaceID(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	chunks := &recordingChunkRepo{
		chunks: []domain.Chunk{{ID: "chunk-1", DocID: "doc-1", Text: "content", Index: 0}},
	}
	service.SetChunkRepo(chunks)

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID:    "tenant-1",
		Workspace:   "项目资料",
		WorkspaceID: "019047ac-0000-7000-9000-000000000001",
		Question:    "如何申请",
		Mode:        "keyword",
		TopK:        3,
	})
	if err != nil {
		t.Fatalf("expected keyword query to succeed, got %v", err)
	}

	if chunks.workspaceID != "019047ac-0000-7000-9000-000000000001" {
		t.Fatalf("expected keyword search to use workspace ID, got %q", chunks.workspaceID)
	}
}

func TestRAGQueryPreservesDocumentIdentityAcrossRetrievalModes(t *testing.T) {
	for _, mode := range []string{"vector", "keyword", "hybrid"} {
		t.Run(mode, func(t *testing.T) {
			vectors := NewMockVectorStore()
			vectors.SetSearchResults([]port.VectorSearchResult{{
				ID: "chunk-vector", SourceDocument: "doc-vector", Content: "vector content", Score: 0.95,
			}})
			service := NewRAGService(&mockEmbedder{dim: 3}, vectors, zap.NewNop())
			service.SetChunkRepo(&recordingChunkRepo{chunks: []domain.Chunk{{
				ID: "chunk-keyword", DocID: "doc-keyword", Text: "keyword content",
			}}})
			expectedIDs := []string{"doc-vector"}
			if mode == "keyword" {
				expectedIDs = []string{"doc-keyword"}
			} else if mode == "hybrid" {
				expectedIDs = []string{"doc-vector", "doc-keyword"}
			}
			result, err := NewRetrievalEvaluator(service).EvaluateRetrieval(
				reqctx.WithTenantID(context.Background(), "tenant-1"), RetrievalSnapshot{
					WorkspaceID: "workspace-1", WorkspaceName: "support", EmbeddingModel: "embedding-3",
					QueryMode: mode, TopK: 5, Reranking: RerankingNone, QueryRewrite: QueryRewriteNone,
				}, RetrievalCase{Query: "query", RelevantDocumentIDs: expectedIDs,
					CitationDocumentIDs: expectedIDs})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Relevant || !result.CitationCorrect {
				t.Fatalf("document-level evaluation failed: %+v", result)
			}
			got := make(map[string]bool, len(result.RetrievedDocumentIDs))
			for _, id := range result.RetrievedDocumentIDs {
				got[id] = true
			}
			for _, expectedID := range expectedIDs {
				if !got[expectedID] {
					t.Fatalf("document identity %q lost: %+v", expectedID, result)
				}
			}
		})
	}
}

func TestRAGQuerySanitizesDependencyErrorsAndLogs(t *testing.T) {
	sensitive := errors.New("POST https://user:password@example.test/search?api_key=secret-token " +
		"response body private document")
	for _, mode := range []string{"vector", "keyword", "hybrid"} {
		t.Run(mode, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			vectors := NewMockVectorStore()
			vectors.SetSearchError(sensitive)
			service := NewRAGService(&mockEmbedder{dim: 3}, vectors, zap.New(core))
			chunks := &recordingChunkRepo{searchErr: sensitive}
			service.SetChunkRepo(chunks)
			if mode == "vector" {
				chunks.searchErr = nil
			}
			_, err := service.Query(context.Background(), RAGQueryRequest{
				TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: mode, TopK: 5,
			})
			if !errors.Is(err, ErrRAGDependency) || errors.Is(err, sensitive) {
				t.Fatalf("dependency classification/cause exposure mismatch: %v", err)
			}
			assertSensitiveTextAbsent(t, err.Error(), logs.All())
		})
	}
}

func assertSensitiveTextAbsent(t *testing.T, errorMessage string, entries []observer.LoggedEntry) {
	t.Helper()
	for _, leaked := range []string{"example.test", "password", "api_key", "secret-token", "private document", "response body"} {
		if strings.Contains(errorMessage, leaked) {
			t.Fatalf("error leaked %q: %s", leaked, errorMessage)
		}
		for _, entry := range entries {
			if strings.Contains(entry.Message, leaked) {
				t.Fatalf("log message leaked %q: %s", leaked, entry.Message)
			}
			for _, value := range entry.ContextMap() {
				if text, ok := value.(string); ok && strings.Contains(text, leaked) {
					t.Fatalf("structured log leaked %q: %#v", leaked, entry.ContextMap())
				}
			}
		}
	}
}

func TestRAGQueryDoesNotLogQuestionContent(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	service := NewRAGService(nil, nil, zap.New(core))
	service.SetChunkRepo(&recordingChunkRepo{})

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1", WorkspaceID: "workspace-1",
		Question: "rag-sensitive-sentinel", Mode: "keyword", TopK: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "rag-sensitive-sentinel") {
			t.Fatalf("question reached log message: %q", entry.Message)
		}
		for _, value := range entry.ContextMap() {
			if text, ok := value.(string); ok && strings.Contains(text, "rag-sensitive-sentinel") {
				t.Fatalf("question reached structured log: %#v", entry.ContextMap())
			}
		}
	}
}

func TestRAGQueryKeywordResolvesWorkspaceIDByName(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	chunks := &recordingChunkRepo{}
	service.SetChunkRepo(chunks)
	service.SetWorkspaceRepo(&recordingWorkspaceRepo{
		workspace: &domain.Workspace{
			ID:   "019047ac-0000-7000-9000-000000000002",
			Name: "产品文档",
		},
	})

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID:  "tenant-1",
		Workspace: "产品文档",
		Question:  "如何申请",
		Mode:      "keyword",
		TopK:      3,
	})
	if err != nil {
		t.Fatalf("expected keyword query to succeed, got %v", err)
	}

	if chunks.workspaceID != "019047ac-0000-7000-9000-000000000002" {
		t.Fatalf("expected keyword search to use resolved workspace ID, got %q", chunks.workspaceID)
	}
}

func TestRAGQueryKeywordDefaultsTopK(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	chunks := &recordingChunkRepo{}
	service.SetChunkRepo(chunks)

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID:    "tenant-1",
		WorkspaceID: "workspace-1",
		Question:    "如何申请",
		Mode:        "keyword",
		TopK:        0,
	})
	if err != nil {
		t.Fatalf("expected keyword query to succeed, got %v", err)
	}

	if chunks.topK != constants.DefaultRAGTopK {
		t.Fatalf("expected non-positive TopK to default to %d, got %d", constants.DefaultRAGTopK, chunks.topK)
	}
}

func TestRAGQueryKeywordRequiresWorkspaceIDWhenNameCannotBeResolved(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	service.SetChunkRepo(&recordingChunkRepo{})

	_, err := service.Query(context.Background(), RAGQueryRequest{
		TenantID: "tenant-1",
		Question: "如何申请",
		Mode:     "keyword",
		TopK:     3,
	})
	if err == nil {
		t.Fatal("expected keyword query without workspace ID to fail, got nil")
	}
	if !strings.Contains(err.Error(), "requires workspace ID") {
		t.Fatalf("expected workspace ID error, got %v", err)
	}
}

func TestNewRAGSearchFnResolvesWorkspaceNameToID(t *testing.T) {
	logger := zap.NewNop()
	service := NewRAGService(nil, nil, logger)
	chunks := &recordingChunkRepo{
		chunks: []domain.Chunk{{ID: "c1", DocID: "d1", Text: "关于学习的段落", Index: 0}},
	}
	service.SetChunkRepo(chunks)
	service.SetWorkspaceRepo(&recordingWorkspaceRepo{
		workspace: &domain.Workspace{
			ID:   "019047ac-0000-7000-9000-000000000099",
			Name: "个人知识库",
			Config: domain.WorkspaceConfig{
				EmbeddingModel: "text-embedding-v3",
				QueryMode:      "keyword",
				TopK:           7,
			},
		},
	})

	fn := NewRAGSearchFn(service, "tenant-1")
	content, err := fn(context.Background(), []string{"个人知识库"}, "学习", 3)
	if err != nil {
		t.Fatalf("expected search to succeed, got %v", err)
	}
	if !strings.Contains(content, "关于学习的段落") {
		t.Fatalf("expected content to include chunk text, got %q", content)
	}
	if chunks.workspaceID != "019047ac-0000-7000-9000-000000000099" {
		t.Fatalf("expected keyword search to receive resolved UUID, got %q", chunks.workspaceID)
	}
	if chunks.topK != 7 {
		t.Fatalf("expected topK from workspace config (7), got %d", chunks.topK)
	}
}

type recordingChunkRepo struct {
	workspaceID string
	topK        int
	chunks      []domain.Chunk
	insertErr   error
	parentErr   error
	searchErr   error
}

func (r *recordingChunkRepo) InsertBatch(ctx context.Context, tenantID, workspaceID string, chunks []domain.Chunk) error {
	r.workspaceID = workspaceID
	return r.insertErr
}

func (r *recordingChunkRepo) KeywordSearch(ctx context.Context, tenantID, workspaceID, query string, topK int) ([]domain.Chunk, error) {
	r.workspaceID = workspaceID
	r.topK = topK
	return r.chunks, r.searchErr
}

func (r *recordingChunkRepo) DeleteByWorkspace(ctx context.Context, tenantID, workspaceID string) error {
	r.workspaceID = workspaceID
	return nil
}

func (r *recordingChunkRepo) InsertParentBatch(_ context.Context, _, _ string, _ []port.ParentChunk) error {
	return r.parentErr
}

func (r *recordingChunkRepo) GetParentByID(_ context.Context, _, _, _ string) (*port.ParentChunk, error) {
	return nil, nil
}

func (r *recordingChunkRepo) GetChunksByIDs(_ context.Context, _, _ string, _ []string) ([]domain.Chunk, error) {
	return nil, nil
}

func (r *recordingChunkRepo) CountByWorkspace(context.Context, string, string) (int64, error) {
	return 0, nil
}

type recordingWorkspaceRepo struct {
	workspace *domain.Workspace
}

func (r *recordingWorkspaceRepo) Create(ctx context.Context, tenantID string, ws *domain.Workspace) error {
	return nil
}

func (r *recordingWorkspaceRepo) GetByName(ctx context.Context, tenantID, name string) (*domain.Workspace, error) {
	return r.workspace, nil
}

func (r *recordingWorkspaceRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Workspace, error) {
	return r.workspace, nil
}

func (r *recordingWorkspaceRepo) List(ctx context.Context, tenantID string) ([]*domain.Workspace, error) {
	return nil, nil
}

func (r *recordingWorkspaceRepo) UpdateDescriptionAndConfig(ctx context.Context, tenantID, name string, description *string, cfg domain.WorkspaceConfig) error {
	return nil
}

func (r *recordingWorkspaceRepo) UpdateName(ctx context.Context, tenantID, oldName, newName string) error {
	return nil
}

func (r *recordingWorkspaceRepo) Delete(ctx context.Context, tenantID, name string) error {
	return nil
}

func (r *recordingWorkspaceRepo) GetConfigForUpload(ctx context.Context, tenantID, name string) (domain.WorkspaceConfig, error) {
	return domain.WorkspaceConfig{}, nil
}

func (r *recordingWorkspaceRepo) GetConfigByID(ctx context.Context, tenantID, id string) (domain.WorkspaceConfig, error) {
	if r.workspace != nil && r.workspace.ID == id {
		return r.workspace.Config, nil
	}
	return domain.WorkspaceConfig{}, nil
}

func TestRAGService_MergeResults_PartialFailureKeepsContentAndWarns(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	svc := NewRAGService(nil, nil, zap.New(core))

	content, err := svc.mergeResults([]wsResult{
		{content: "chunk-a"},
		{err: errors.New("workspace down")},
		{content: "chunk-b"},
	})
	if err != nil {
		t.Fatalf("partial failure must not fail the query, got %v", err)
	}
	if !strings.Contains(content, "chunk-a") || !strings.Contains(content, "chunk-b") {
		t.Fatalf("expected successful workspace content, got %q", content)
	}

	warns := logs.FilterMessage("knowledge.rag.partial_failure").All()
	if len(warns) != 1 {
		t.Fatalf("expected 1 partial-failure WARN, got %d", len(warns))
	}
	failed := warns[0].ContextMap()["failed_workspaces"]
	if failed != int64(1) {
		t.Errorf("failed_workspaces=%v, want 1", failed)
	}
}

func TestRAGService_MergeResults_AllFailedReturnsFirstError(t *testing.T) {
	svc := NewRAGService(nil, nil, zap.NewNop())
	errA := errors.New("workspace A down")

	content, err := svc.mergeResults([]wsResult{{err: errA}, {err: errors.New("workspace B down")}})
	if !errors.Is(err, errA) {
		t.Fatalf("expected first error to surface, got %v", err)
	}
	if content != "" {
		t.Fatalf("expected empty content when all workspaces failed, got %q", content)
	}
}

func TestRAGService_MergeResults_AllSucceedConcatenatesContent(t *testing.T) {
	svc := NewRAGService(nil, nil, zap.NewNop())

	content, err := svc.mergeResults([]wsResult{{content: "a"}, {content: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if content != "ab" {
		t.Fatalf("expected concatenated content, got %q", content)
	}
}

// blockingChunkRepo blocks each KeywordSearch until release is closed while
// tracking peak concurrency, making the fan-out bound deterministically
// observable.
type blockingChunkRepo struct {
	recordingChunkRepo
	release chan struct{}
	current int32
	max     int32
}

func (r *blockingChunkRepo) KeywordSearch(_ context.Context, _, _, _ string, _ int) ([]domain.Chunk, error) {
	inFlight := atomic.AddInt32(&r.current, 1)
	for {
		peak := atomic.LoadInt32(&r.max)
		if inFlight <= peak || atomic.CompareAndSwapInt32(&r.max, peak, inFlight) {
			break
		}
	}
	<-r.release
	atomic.AddInt32(&r.current, -1)
	return []domain.Chunk{{ID: "c1", DocID: "d1", Text: "关于学习的段落", Index: 0}}, nil
}

func TestNewRAGSearchFn_FanoutBoundedByMaxConcurrentWorkspaceSearch(t *testing.T) {
	repo := &blockingChunkRepo{release: make(chan struct{})}
	svc := NewRAGService(nil, nil, zap.NewNop())
	svc.SetChunkRepo(repo)
	svc.SetWorkspaceRepo(&recordingWorkspaceRepo{
		workspace: &domain.Workspace{
			ID:   "019047ac-0000-7000-9000-000000000099",
			Name: "个人知识库",
			Config: domain.WorkspaceConfig{
				EmbeddingModel: "text-embedding-v3",
				QueryMode:      "keyword",
				TopK:           3,
			},
		},
	})

	const wsCount = 6
	names := make([]string, wsCount)
	for i := range names {
		names[i] = "ws-" + itoa(i+1)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = NewRAGSearchFn(svc, "tenant-1")(context.Background(), names, "学习", 3)
	}()

	limit := int32(constants.MaxConcurrentWorkspaceSearch)
	// Wait until the bound is saturated: searches may enter KeywordSearch
	// only one at a time, so reaching `limit` proves bounded launch.
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&repo.current) < limit {
		select {
		case <-done:
			t.Fatal("fan-out finished before the concurrency bound was saturated")
		case <-deadline:
			t.Fatalf("timeout waiting for %d concurrent searches", limit)
		case <-time.After(5 * time.Millisecond):
		}
	}

	// If the fan-out were unbounded, extra searches would have entered by now.
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&repo.current); got > limit {
		t.Fatalf("fan-out exceeded bound: %d searches in flight, limit %d", got, limit)
	}

	close(repo.release)
	<-done
	if peak := atomic.LoadInt32(&repo.max); peak > limit {
		t.Fatalf("peak concurrency %d exceeds bound %d", peak, limit)
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}
