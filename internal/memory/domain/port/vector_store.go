package port

import (
	"context"
)

// VectorDoc represents a document in the vector store.
type VectorDoc struct {
	ID         string
	Embedding  []float32
	Metadata   map[string]interface{}
	Similarity float64 // populated during search
}

// VectorStore manages embeddings and similarity search.
type VectorStore interface {
	// Upsert inserts or updates a document embedding.
	Upsert(ctx context.Context, collectionName string, docs []*VectorDoc) error

	// Search performs similarity search and returns top-k results.
	Search(ctx context.Context, collectionName string, queryVector []float32, topK int, filter map[string]interface{}) ([]*VectorDoc, error)

	// Delete removes documents by IDs.
	Delete(ctx context.Context, collectionName string, ids []string) error

	// CreateCollection initializes a vector collection with specified dimension.
	CreateCollection(ctx context.Context, collectionName string, dimension int) error
}
