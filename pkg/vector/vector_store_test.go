package vector

import (
	"testing"

	"go.uber.org/zap"
)

// TestVectorStore_NewVectorStore tests constructor
func TestVectorStore_NewVectorStore(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	vs := NewVectorStore("localhost", "19530", logger)

	if vs.host != "localhost" {
		t.Errorf("expected host localhost, got %s", vs.host)
	}

	if vs.port != "19530" {
		t.Errorf("expected port 19530, got %s", vs.port)
	}

	if vs.dim != 1536 {
		t.Errorf("expected dim 1536, got %d", vs.dim)
	}
}

// TestSearchResult tests SearchResult struct
func TestSearchResult(t *testing.T) {
	result := SearchResult{
		ID:             "test-id",
		Content:        "test content",
		SourceDocument: "source.txt",
		ChunkIndex:     1,
		Score:          0.95,
	}

	if result.ID != "test-id" {
		t.Errorf("got ID %s, want test-id", result.ID)
	}

	if result.Content != "test content" {
		t.Errorf("got Content %s, want test content", result.Content)
	}

	if result.SourceDocument != "source.txt" {
		t.Errorf("got SourceDocument %s, want source.txt", result.SourceDocument)
	}

	if result.ChunkIndex != 1 {
		t.Errorf("got ChunkIndex %d, want 1", result.ChunkIndex)
	}

	if result.Score != 0.95 {
		t.Errorf("got Score %f, want 0.95", result.Score)
	}
}

// TestDocumentChunk tests DocumentChunk struct
func TestDocumentChunk(t *testing.T) {
	chunk := DocumentChunk{
		ID:             "chunk-1",
		Content:        "chunk content",
		SourceDocument: "doc.txt",
		ChunkIndex:     0,
		Vector:         make([]float32, 1536),
	}

	if chunk.ID != "chunk-1" {
		t.Errorf("got ID %s, want chunk-1", chunk.ID)
	}

	if chunk.Content != "chunk content" {
		t.Errorf("got Content %s, want chunk content", chunk.Content)
	}

	if chunk.SourceDocument != "doc.txt" {
		t.Errorf("got SourceDocument %s, want doc.txt", chunk.SourceDocument)
	}

	if chunk.ChunkIndex != 0 {
		t.Errorf("got ChunkIndex %d, want 0", chunk.ChunkIndex)
	}

	if len(chunk.Vector) != 1536 {
		t.Errorf("got Vector length %d, want 1536", len(chunk.Vector))
	}
}
