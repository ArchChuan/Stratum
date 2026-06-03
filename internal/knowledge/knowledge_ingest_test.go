package knowledge

import (
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/llmgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
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
		TotalNodes:   1,
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

	if result.TotalNodes != 1 {
		t.Errorf("expected 1 node, got %d", result.TotalNodes)
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
		Answer:       "AI is artificial intelligence",
		Mode:         "vector",
		Sources:      []Source{},
		GraphContext: []GraphEntity{},
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

func TestGraphEntityStructure(t *testing.T) {
	entity := GraphEntity{
		ID:    "entity-1",
		Label: "Person",
		Properties: map[string]interface{}{
			"name": "John",
			"age":  30,
		},
	}

	if entity.ID != "entity-1" {
		t.Errorf("expected ID entity-1, got %s", entity.ID)
	}

	if entity.Label != "Person" {
		t.Errorf("expected label Person, got %s", entity.Label)
	}

	if len(entity.Properties) != 2 {
		t.Errorf("expected 2 properties, got %d", len(entity.Properties))
	}
}

func TestBuildPrompt(t *testing.T) {
	logger := zap.NewNop()
	embedSvc := embedding.NewEmbeddingService(llmgateway.NewOpenAIClient("", "", logger), logger)
	vectorStore := vector.NewVectorStore("localhost", "19530", logger)
	graphRAG := NewGraphRAG("bolt://localhost:7687", "neo4j", "password", logger)

	ragService := NewRAGService(embedSvc, vectorStore, graphRAG, logger)

	chunks := []string{"chunk1", "chunk2"}
	graphContext := []GraphEntity{
		{
			ID:         "e1",
			Label:      "Entity",
			Properties: map[string]interface{}{"name": "test"},
		},
	}

	prompt := ragService.BuildPrompt("What is this?", chunks, graphContext)

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
