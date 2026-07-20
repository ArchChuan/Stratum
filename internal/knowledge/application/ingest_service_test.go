package application

import (
	"context"
	"sync"
	"testing"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/document"
	"go.uber.org/zap"
)

func TestIngestDocumentRequest(t *testing.T) {
	req := IngestDocumentRequest{
		Workspace:    "test-workspace",
		DocumentData: []byte("test content"),
		FileName:     "test.txt",
		DocumentID:   "doc-123",
	}

	if req.Workspace != "test-workspace" {
		t.Errorf("expected workspace test-workspace, got %s", req.Workspace)
	}

	if req.DocumentID != "doc-123" {
		t.Errorf("expected document ID doc-123, got %s", req.DocumentID)
	}

	if len(req.DocumentData) == 0 {
		t.Error("expected non-empty document data")
	}
}

func TestIngestResultInitialization(t *testing.T) {
	result := &IngestResult{
		DocumentID:   "doc-1",
		Workspace:    "ws-1",
		TotalChunks:  10,
		TotalVectors: 10,
		Errors:       []string{},
	}

	if result.DocumentID != "doc-1" {
		t.Errorf("expected document ID doc-1, got %s", result.DocumentID)
	}

	if result.TotalChunks != 10 {
		t.Errorf("expected 10 chunks, got %d", result.TotalChunks)
	}

	if result.TotalVectors != 10 {
		t.Errorf("expected 10 vectors, got %d", result.TotalVectors)
	}

	if len(result.Errors) != 0 {
		t.Errorf("expected no errors, got %d", len(result.Errors))
	}
}

func TestIngestResultWithErrors(t *testing.T) {
	result := &IngestResult{
		DocumentID: "doc-1",
		Workspace:  "ws-1",
		Errors:     []string{"error1", "error2"},
	}

	if len(result.Errors) != 2 {
		t.Errorf("expected 2 errors, got %d", len(result.Errors))
	}

	if result.Errors[0] != "error1" {
		t.Errorf("expected error1, got %s", result.Errors[0])
	}
}

func TestRAGQueryRequest(t *testing.T) {
	req := RAGQueryRequest{
		Question:  "What is AI?",
		Workspace: "test-ws",
		Mode:      "vector",
		TopK:      5,
	}

	if req.Question != "What is AI?" {
		t.Errorf("expected question 'What is AI?', got %s", req.Question)
	}

	if req.Mode != "vector" {
		t.Errorf("expected mode vector, got %s", req.Mode)
	}

	if req.TopK != 5 {
		t.Errorf("expected TopK 5, got %d", req.TopK)
	}
}

func TestRAGQueryRequestHybridMode(t *testing.T) {
	req := RAGQueryRequest{
		Question:  "test",
		Workspace: "ws",
		Mode:      "hybrid",
		TopK:      10,
	}

	if req.Mode != "hybrid" {
		t.Errorf("expected mode hybrid, got %s", req.Mode)
	}
}

func TestRAGQueryResult(t *testing.T) {
	result := &RAGQueryResult{
		Answer:  "AI is artificial intelligence",
		Mode:    "vector",
		Sources: []Source{},
	}

	if result.Answer != "AI is artificial intelligence" {
		t.Errorf("expected answer, got %s", result.Answer)
	}

	if result.Mode != "vector" {
		t.Errorf("expected mode vector, got %s", result.Mode)
	}

	if len(result.Sources) != 0 {
		t.Errorf("expected 0 sources, got %d", len(result.Sources))
	}
}

func TestIngestDocumentSemanticStrategyUsesWorkspaceEmbeddingModelForChunking(t *testing.T) {
	logger := zap.NewNop()
	parser := &mockParser{
		out: "猫喜欢晒太阳。猫会追逐毛线。数据库索引提升查询速度。查询计划会影响性能。",
	}
	embedder := &recordingEmbedder{dim: 1536}
	docRepo := newMockDocRepo()
	ingest := NewKnowledgeIngest(parser, document.NewChunkingService(), nil, nil, logger)
	ingest.SetDocRepo(docRepo)
	ingest.SetEmbedResolver(func(_ context.Context, tenantID, model string) EmbedClient {
		embedder.recordResolverCall(tenantID, model)
		return embedder
	})

	_, err := ingest.IngestDocument(context.Background(), IngestDocumentRequest{
		TenantID:         "tenant-a",
		Workspace:        "workspace-a",
		WorkspaceID:      "workspace-id-a",
		EmbeddingModel:   "embedding-3",
		ChunkingStrategy: domain.ChunkingStrategySemantic,
		DocumentData:     []byte("ignored by mock parser"),
		FileName:         "semantic.txt",
		DocumentID:       "doc-semantic",
		ContentHash:      "hash-semantic",
	})
	if err != nil {
		t.Fatalf("expected semantic ingest to be accepted, got %v", err)
	}

	if got := embedder.vectorCalls(); got < 4 {
		t.Fatalf("expected semantic chunking to embed sentences before queueing job, got %d vector calls", got)
	}
	tenantID, model := embedder.lastResolverCall()
	if tenantID != "tenant-a" {
		t.Fatalf("expected resolver to receive tenant tenant-a, got %q", tenantID)
	}
	if model != "embedding-3" {
		t.Fatalf("expected semantic chunking to use workspace embedding model embedding-3, got %q", model)
	}
}

func TestIngestDocumentUsesWorkspaceChunkSizeAndOverlap(t *testing.T) {
	logger := zap.NewNop()
	parser := &mockParser{out: "这是一段超过最小长度的知识库文档内容，用于验证分块参数会从知识库配置传入实际分块策略。" +
		"如果仍然使用默认值，记录型策略会捕获到错误的分块大小和重叠长度。"}
	strategy := &recordingChunkingService{}
	ingest := NewKnowledgeIngest(parser, strategy, nil, nil, logger)

	_, err := ingest.IngestDocument(context.Background(), IngestDocumentRequest{
		TenantID:         "tenant-a",
		Workspace:        "workspace-a",
		WorkspaceID:      "workspace-id-a",
		EmbeddingModel:   domain.DefaultEmbeddingModel,
		ChunkingStrategy: domain.ChunkingStrategyRecursive,
		ChunkSize:        777,
		ChunkOverlap:     123,
		DocumentData:     []byte("ignored by mock parser"),
		FileName:         "chunk-params.txt",
		DocumentID:       "doc-chunk-params",
		ContentHash:      "hash-chunk-params",
	})
	if err != nil {
		t.Fatalf("expected ingest to be accepted, got %v", err)
	}

	maxRunes, overlapRunes := strategy.params()
	if maxRunes != 777 {
		t.Fatalf("expected strategy to receive chunk size 777, got %d", maxRunes)
	}
	if overlapRunes != 123 {
		t.Fatalf("expected strategy to receive chunk overlap 123, got %d", overlapRunes)
	}
}

type recordingChunkingService struct {
	mu           sync.Mutex
	maxRunes     int
	overlapRunes int
}

func (s *recordingChunkingService) Clean(text string) string { return text }

func (s *recordingChunkingService) Chunk(
	_ context.Context,
	_, _ string,
	maxRunes, overlapRunes int,
	_ knowledgeport.Embedder,
) (knowledgeport.ChunkResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxRunes = maxRunes
	s.overlapRunes = overlapRunes
	return knowledgeport.ChunkResult{
		Leaves: []knowledgeport.TextChunk{{
			Content: "这是一段超过最小长度的分块结果，用于通过清洗过滤并触发后续异步导入流程。" +
				"测试只关心分块参数是否传入策略，不依赖后台向量写入完成。",
		}},
	}, nil
}

func (s *recordingChunkingService) Filter(chunks []knowledgeport.TextChunk) []knowledgeport.TextChunk {
	return chunks
}

func (s *recordingChunkingService) params() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.maxRunes, s.overlapRunes
}

type recordingEmbedder struct {
	mu sync.Mutex

	dim          int
	vectorCount  int
	batchCount   int
	lastTenantID string
	lastModel    string
}

func (e *recordingEmbedder) recordResolverCall(tenantID, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastTenantID = tenantID
	e.lastModel = model
}

func (e *recordingEmbedder) EmbedVector(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	e.vectorCount++
	e.mu.Unlock()

	switch {
	case contains(text, "猫"):
		return []float32{1, 0, 0}, nil
	case contains(text, "数据库") || contains(text, "查询"):
		return []float32{0, 1, 0}, nil
	default:
		return []float32{0, 0, 1}, nil
	}
}

func (e *recordingEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	e.batchCount++
	e.mu.Unlock()

	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, e.dim)
	}
	return out, nil
}

func (e *recordingEmbedder) GetVectorDimension() int { return e.dim }

func (e *recordingEmbedder) vectorCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.vectorCount
}

func (e *recordingEmbedder) lastResolverCall() (string, string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastTenantID, e.lastModel
}

func TestSourceStructure(t *testing.T) {
	source := Source{
		DocumentID: "doc-1",
		Content:    "test content",
		ChunkIndex: 0,
		Score:      0.95,
	}

	if source.DocumentID != "doc-1" {
		t.Errorf("expected document ID doc-1, got %s", source.DocumentID)
	}

	if source.Score != 0.95 {
		t.Errorf("expected score 0.95, got %f", source.Score)
	}
}

func TestBuildPrompt(t *testing.T) {
	logger := zap.NewNop()

	ragService := NewRAGService(nil, nil, logger)

	chunks := []string{"chunk1", "chunk2"}

	prompt := ragService.BuildPrompt("What is this?", chunks)

	if len(prompt) == 0 {
		t.Error("expected non-empty prompt")
	}

	if !contains(prompt, "What is this?") {
		t.Error("expected prompt to contain question")
	}

	if !contains(prompt, "chunk1") {
		t.Error("expected prompt to contain chunk1")
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
