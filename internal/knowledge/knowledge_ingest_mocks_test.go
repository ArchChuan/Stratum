package knowledge

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/vector"
)

func TestMockGraphRAGInterface(t *testing.T) {
	ctx := context.Background()
	mockGraphRAG := NewMockGraphRAG()

	if err := mockGraphRAG.Connect(ctx); err != nil {
		t.Errorf("expected no error from Connect, got %v", err)
	}

	if err := mockGraphRAG.CreateNode(ctx, "Test", map[string]interface{}{}); err != nil {
		t.Errorf("expected no error from CreateNode, got %v", err)
	}

	if err := mockGraphRAG.CreateRelationship(ctx, "from", "to", "rel"); err != nil {
		t.Errorf("expected no error from CreateRelationship, got %v", err)
	}

	mockGraphRAG.SetQueryResult([]interface{}{"test result"})
	result, err := mockGraphRAG.Query(ctx, "test query")
	if err != nil {
		t.Errorf("expected no error from Query, got %v", err)
	}

	if result == nil {
		t.Error("expected non-nil query result")
	}

	neighbors, err := mockGraphRAG.GetNeighborNodes(ctx, "node-1", 2)
	if err != nil {
		t.Errorf("expected no error from GetNeighborNodes, got %v", err)
	}

	if len(neighbors) != 0 {
		t.Errorf("expected 0 neighbors, got %d", len(neighbors))
	}

	results, err := mockGraphRAG.FullTextSearch(ctx, "search", 10)
	if err != nil {
		t.Errorf("expected no error from FullTextSearch, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	if err := mockGraphRAG.Close(); err != nil {
		t.Errorf("expected no error from Close, got %v", err)
	}
}

func TestMockVectorStoreInterface(t *testing.T) {
	ctx := context.Background()
	mockVectorStore := NewMockVectorStore()

	if err := mockVectorStore.Connect(ctx); err != nil {
		t.Errorf("expected no error from Connect, got %v", err)
	}

	if err := mockVectorStore.CreateCollection(ctx, "test-collection"); err != nil {
		t.Errorf("expected no error from CreateCollection, got %v", err)
	}

	chunks := []vector.DocumentChunk{
		{
			ID:             "chunk-1",
			Content:        "content",
			SourceDocument: "doc-1",
			ChunkIndex:     0,
			Vector:         make([]float32, 1536),
		},
	}

	if err := mockVectorStore.Insert(ctx, "test-collection", chunks); err != nil {
		t.Errorf("expected no error from Insert, got %v", err)
	}

	results, err := mockVectorStore.Search(ctx, "test-collection", make([]float32, 1536), 5)
	if err != nil {
		t.Errorf("expected no error from Search, got %v", err)
	}

	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}

	if err := mockVectorStore.Delete(ctx, "test-collection", []string{"chunk-1"}); err != nil {
		t.Errorf("expected no error from Delete, got %v", err)
	}

	if err := mockVectorStore.Flush(ctx, "test-collection"); err != nil {
		t.Errorf("expected no error from Flush, got %v", err)
	}

	if err := mockVectorStore.Close(); err != nil {
		t.Errorf("expected no error from Close, got %v", err)
	}
}

func TestMockGraphRAGSetters(t *testing.T) {
	mockGraphRAG := NewMockGraphRAG()

	testResult := []interface{}{"result1", "result2"}
	mockGraphRAG.SetQueryResult(testResult)

	ctx := context.Background()
	result, err := mockGraphRAG.Query(ctx, "test")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if len(result.([]interface{})) != 2 {
		t.Errorf("expected 2 results, got %d", len(result.([]interface{})))
	}
}

func TestMockVectorStoreSetters(t *testing.T) {
	mockVectorStore := NewMockVectorStore()

	testResults := []interface{}{"result1", "result2"}
	mockVectorStore.SetSearchResults(testResults)

	ctx := context.Background()
	results, err := mockVectorStore.Search(ctx, "test", make([]float32, 1536), 5)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if len(results) != 2 {
		t.Errorf("expected 2 results, got %d", len(results))
	}
}
