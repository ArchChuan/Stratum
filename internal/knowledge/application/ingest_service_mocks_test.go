package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/vector"
)

func TestMockGraphStoreInterface(t *testing.T) {
	ctx := context.Background()
	mockGraphStore := NewMockGraphStore()

	if err := mockGraphStore.Connect(ctx); err != nil {
		t.Errorf("expected no error from Connect, got %v", err)
	}
	if err := mockGraphStore.CreateNode(ctx, "Test", map[string]interface{}{}); err != nil {
		t.Errorf("expected no error from CreateNode, got %v", err)
	}
	if err := mockGraphStore.CreateRelationship(ctx, "from", "to", "rel"); err != nil {
		t.Errorf("expected no error from CreateRelationship, got %v", err)
	}
	neighbors, err := mockGraphStore.GetNeighborNodes(ctx, "node-1", 2)
	if err != nil {
		t.Errorf("expected no error from GetNeighborNodes, got %v", err)
	}
	if len(neighbors) != 0 {
		t.Errorf("expected 0 neighbors, got %d", len(neighbors))
	}
	results, err := mockGraphStore.FullTextSearch(ctx, "search", 10)
	if err != nil {
		t.Errorf("expected no error from FullTextSearch, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if err := mockGraphStore.Close(); err != nil {
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
	chunks := []vector.DocumentChunk{{ID: "chunk-1", Content: "content", SourceDocument: "doc-1", ChunkIndex: 0, Vector: make([]float32, 1536)}}
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
