package port

import (
	"context"
	"errors"
	"fmt"
)

var ErrInvalidVectorSearchFilter = errors.New("invalid vector search filter")

type VectorStoreUnavailableError struct{ Err error }

func (e *VectorStoreUnavailableError) Error() string {
	return fmt.Sprintf("vector store unavailable: %v", e.Err)
}

func (e *VectorStoreUnavailableError) Unwrap() error { return e.Err }

type VectorSearchFilter struct {
	UserID            string
	AgentID           string
	IncludeUserScope  bool
	IncludeAgentScope bool
}

func (f VectorSearchFilter) Validate() error {
	if f.UserID == "" {
		return fmt.Errorf("%w: user ID is required", ErrInvalidVectorSearchFilter)
	}
	if !f.IncludeUserScope && !f.IncludeAgentScope {
		return fmt.Errorf("%w: at least one scope is required", ErrInvalidVectorSearchFilter)
	}
	if f.IncludeAgentScope && f.AgentID == "" {
		return fmt.Errorf("%w: agent ID is required for agent scope", ErrInvalidVectorSearchFilter)
	}
	return nil
}

// VectorDoc represents a document in the vector store.
type VectorDoc struct {
	ID         string
	Embedding  []float32
	Metadata   map[string]interface{}
	Similarity float64 // legacy cosine-style score; Milvus L2 search leaves this zero
	Distance   float64 // L2 distance populated during Milvus search; lower is closer
}

// VectorStore manages embeddings and similarity search.
type VectorStore interface {
	// Upsert inserts or updates a document embedding.
	Upsert(ctx context.Context, collectionName string, docs []*VectorDoc) error

	// Search performs similarity search and returns top-k results.
	Search(ctx context.Context, collectionName string, queryVector []float32, topK int, filter VectorSearchFilter) ([]*VectorDoc, error)

	// Delete removes documents by IDs.
	Delete(ctx context.Context, collectionName string, ids []string) error

	// DeleteAllByUser removes all vectors for a user from the tenant's memory collection.
	DeleteAllByUser(ctx context.Context, tenantID, userID string) error

	// DeleteAllByAgent removes all vectors for an agent from the tenant's memory collection.
	DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error

	// CreateCollection initializes a vector collection with specified dimension.
	CreateCollection(ctx context.Context, collectionName string, dimension int) error
}
