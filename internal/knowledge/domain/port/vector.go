package port

import (
	"context"
	"fmt"
)

// VectorStoreUnavailableError identifies a transient vector-store failure
// (Milvus unavailable or server-side rate-limited). Ingest retries on it
// instead of failing the document outright. Infrastructure adapters translate
// the concrete store error into this port type so application code never
// depends on a specific storage implementation.
type VectorStoreUnavailableError struct{ Err error }

func (e *VectorStoreUnavailableError) Error() string {
	return fmt.Sprintf("vector store unavailable: %v", e.Err)
}

func (e *VectorStoreUnavailableError) Unwrap() error { return e.Err }

type VectorDocument struct {
	ID             string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Vector         []float32
}

type VectorSearchResult struct {
	ID             string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Score          float32
}

// CollectionInfo is the structural snapshot of a Milvus collection returned
// by DescribeCollection. HasAgentID / HasUserID report optional-column presence
// so callers can decide their own tolerance for legacy schemas.
type CollectionInfo struct {
	Dim        int
	HasAgentID bool
	HasUserID  bool
}

type VectorStore interface {
	CreateCollectionWithDim(ctx context.Context, collectionName string, dimension int) error
	Insert(ctx context.Context, collectionName string, docs []VectorDocument) error
	Search(ctx context.Context, collectionName string, queryVector []float32, topK int) ([]VectorSearchResult, error)
	// SearchWithFilter runs Search restricted by a Milvus filter expression
	// (e.g. `source_document in ["a","b"]`). An empty expression means no
	// filter. Fail-closed: schema drift or a missing collection is an error.
	SearchWithFilter(ctx context.Context, collectionName string, queryVector []float32, topK int, expression string) ([]VectorSearchResult, error)
	DescribeCollection(ctx context.Context, collectionName string) (CollectionInfo, error)
	Flush(ctx context.Context, collectionName string) error
	DeleteCollection(ctx context.Context, collectionName string) error
	CountVectors(ctx context.Context, collectionName string) (int64, error)
	// DeleteByDocumentIDs removes all vectors whose source_document is in docIDs
	// from the given collection. Used to purge a document's old vectors before
	// re-embedding an updated version. A missing collection is treated as
	// success (idempotent). An empty docIDs list is a no-op.
	DeleteByDocumentIDs(ctx context.Context, collectionName string, docIDs []string) error
}
