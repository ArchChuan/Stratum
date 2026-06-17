package milvus

import (
	"context"
	"fmt"
)

// Document is a generic vector record handed to a VectorIndex implementation.
type Document struct {
	ID       string
	Vector   []float32
	Metadata map[string]any
}

// SearchHit is a generic search result returned by VectorIndex.Search.
type SearchHit struct {
	ID       string
	Score    float32
	Metadata map[string]any
}

// VectorIndex is the consumer-side abstraction for vector search backends.
// Implementations adapt a concrete client (Milvus, etc.) to this interface
// so business code can depend on capability rather than SDK specifics.
type VectorIndex interface {
	EnsureCollection(ctx context.Context, name string, dim int) error
	Upsert(ctx context.Context, name string, docs []Document) error
	Search(ctx context.Context, name string, vec []float32, topK int, filter string) ([]SearchHit, error)
	Drop(ctx context.Context, name string) error
}

// Index adapts a *VectorStore to the VectorIndex interface. Document metadata
// is mapped to VectorStore's fixed schema (user_id / content / source_document
// / chunk_index) when present; unknown metadata keys are ignored.
type Index struct {
	store *VectorStore
}

// NewIndex returns a VectorIndex adapter backed by the given VectorStore.
func NewIndex(store *VectorStore) *Index {
	return &Index{store: store}
}

func (i *Index) EnsureCollection(ctx context.Context, name string, dim int) error {
	if i == nil || i.store == nil {
		return fmt.Errorf("milvus: index not initialised")
	}
	return i.store.CreateCollectionWithDim(ctx, name, dim)
}

func (i *Index) Upsert(ctx context.Context, name string, docs []Document) error {
	if i == nil || i.store == nil {
		return fmt.Errorf("milvus: index not initialised")
	}
	if len(docs) == 0 {
		return nil
	}
	chunks := make([]DocumentChunk, len(docs))
	for j, d := range docs {
		chunk := DocumentChunk{
			ID:     d.ID,
			Vector: d.Vector,
		}
		if d.Metadata != nil {
			if v, ok := d.Metadata["user_id"].(string); ok {
				chunk.UserID = v
			}
			if v, ok := d.Metadata["content"].(string); ok {
				chunk.Content = v
			}
			if v, ok := d.Metadata["source_document"].(string); ok {
				chunk.SourceDocument = v
			}
			if v, ok := d.Metadata["chunk_index"].(int64); ok {
				chunk.ChunkIndex = v
			} else if v, ok := d.Metadata["chunk_index"].(int); ok {
				chunk.ChunkIndex = int64(v)
			}
		}
		chunks[j] = chunk
	}
	return i.store.Insert(ctx, name, chunks)
}

func (i *Index) Search(ctx context.Context, name string, vec []float32, topK int, filter string) ([]SearchHit, error) {
	if i == nil || i.store == nil {
		return nil, fmt.Errorf("milvus: index not initialised")
	}
	results, err := i.store.SearchWithFilter(ctx, name, vec, topK, filter)
	if err != nil {
		return nil, err
	}
	hits := make([]SearchHit, len(results))
	for j, r := range results {
		hits[j] = SearchHit{
			ID:    r.ID,
			Score: r.Score,
			Metadata: map[string]any{
				"content":         r.Content,
				"source_document": r.SourceDocument,
				"chunk_index":     r.ChunkIndex,
			},
		}
	}
	return hits, nil
}

func (i *Index) Drop(ctx context.Context, name string) error {
	if i == nil || i.store == nil {
		return fmt.Errorf("milvus: index not initialised")
	}
	return i.store.DeleteCollection(ctx, name)
}

var _ VectorIndex = (*Index)(nil)
