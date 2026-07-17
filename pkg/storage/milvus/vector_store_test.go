package milvus

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestPrimaryIDDeleteExpressionUsesIDFieldAndEscapesValues(t *testing.T) {
	expr := primaryIDDeleteExpression([]string{`fact-1`, `fact-"2\\`})
	want := `id in ["fact-1","fact-\"2\\\\"]`
	if expr != want {
		t.Fatalf("expression = %q, want %q", expr, want)
	}
}

func TestPrimaryIDDeleteExpressionEmptyIsNoOp(t *testing.T) {
	if got := primaryIDDeleteExpression(nil); !reflect.DeepEqual(got, "") {
		t.Fatalf("expression = %q, want empty", got)
	}
}

func TestVectorStore_NewVectorStore(t *testing.T) {
	logger := zap.NewNop()
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

	if vs.logger == nil {
		t.Error("expected non-nil logger")
	}
}

func TestVectorStore_NewVectorStoreCustomPort(t *testing.T) {
	logger := zap.NewNop()
	vs := NewVectorStore("milvus.example.com", "9999", logger)

	if vs.host != "milvus.example.com" {
		t.Errorf("expected host milvus.example.com, got %s", vs.host)
	}

	if vs.port != "9999" {
		t.Errorf("expected port 9999, got %s", vs.port)
	}
}

func TestVectorStoreWithCollectionLockSerializesSameCollection(t *testing.T) {
	vs := NewVectorStore("localhost", "19530", zap.NewNop())
	var active int32
	var maxActive int32
	var wg sync.WaitGroup

	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vs.withCollectionLock("tenant_kb", func() {
				now := atomic.AddInt32(&active, 1)
				for {
					max := atomic.LoadInt32(&maxActive)
					if now <= max || atomic.CompareAndSwapInt32(&maxActive, max, now) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&active, -1)
			})
		}()
	}

	wg.Wait()
	if maxActive != 1 {
		t.Fatalf("expected same collection operations to be serialized, max active=%d", maxActive)
	}
}

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

func TestSearchResultZeroScore(t *testing.T) {
	result := SearchResult{
		ID:    "id",
		Score: 0.0,
	}

	if result.Score != 0.0 {
		t.Errorf("expected score 0.0, got %f", result.Score)
	}
}

func TestSearchResultHighScore(t *testing.T) {
	result := SearchResult{
		ID:    "id",
		Score: 1.0,
	}

	if result.Score != 1.0 {
		t.Errorf("expected score 1.0, got %f", result.Score)
	}
}

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

func TestDocumentChunkMultipleChunks(t *testing.T) {
	chunks := []DocumentChunk{
		{
			ID:         "chunk-1",
			ChunkIndex: 0,
			Vector:     make([]float32, 1536),
		},
		{
			ID:         "chunk-2",
			ChunkIndex: 1,
			Vector:     make([]float32, 1536),
		},
		{
			ID:         "chunk-3",
			ChunkIndex: 2,
			Vector:     make([]float32, 1536),
		},
	}

	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		if chunk.ChunkIndex != int64(i) {
			t.Errorf("chunk %d: expected index %d, got %d", i, i, chunk.ChunkIndex)
		}
	}
}

func TestDocumentChunkEmptyVector(t *testing.T) {
	chunk := DocumentChunk{
		ID:     "chunk-1",
		Vector: []float32{},
	}

	if len(chunk.Vector) != 0 {
		t.Errorf("expected empty vector, got length %d", len(chunk.Vector))
	}
}

func TestDocumentChunkLargeVector(t *testing.T) {
	largeVector := make([]float32, 4096)
	chunk := DocumentChunk{
		ID:     "chunk-1",
		Vector: largeVector,
	}

	if len(chunk.Vector) != 4096 {
		t.Errorf("expected vector length 4096, got %d", len(chunk.Vector))
	}
}

func TestSearchResultMultiple(t *testing.T) {
	results := []SearchResult{
		{ID: "id-1", Score: 0.95},
		{ID: "id-2", Score: 0.87},
		{ID: "id-3", Score: 0.72},
	}

	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}

	if results[0].Score < results[1].Score {
		t.Error("expected results sorted by score descending")
	}
}
