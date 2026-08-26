package vectorstore

import (
	"context"
	"errors"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

// storeIface narrows the Milvus-backed store to what the adapter needs so
// tests can inject a stub without a live Milvus.
type storeIface interface {
	CreateCollectionWithDim(ctx context.Context, collectionName string, dimension int) error
	Insert(ctx context.Context, collectionName string, docs []storagemilvus.DocumentChunk, partition string) error
	// SearchWithFilterStrict is the fail-closed search variant: RAG collections
	// are created with the current schema, so a missing collection or schema
	// drift is reported instead of silently returning empty results.
	SearchWithFilterStrict(ctx context.Context, collectionName string, queryVector []float32, topK int, expression string, partitions ...string) ([]storagemilvus.SearchResult, error)
	DescribeCollection(ctx context.Context, collectionName string) (storagemilvus.CollectionInfo, error)
	Flush(ctx context.Context, collectionName string) error
	DeleteCollection(ctx context.Context, collectionName string) error
	CountVectors(ctx context.Context, collectionName, partition string) (int64, error)
	DeleteByDocumentIDs(ctx context.Context, collectionName string, docIDs []string) error
}

var _ storeIface = (*storagemilvus.VectorStore)(nil)

type Adapter struct {
	store storeIface
}

func New(store *storagemilvus.VectorStore) *Adapter {
	return &Adapter{store: store}
}

func (a *Adapter) CreateCollectionWithDim(ctx context.Context, collectionName string, dimension int) error {
	return a.store.CreateCollectionWithDim(ctx, collectionName, dimension)
}

func (a *Adapter) Insert(ctx context.Context, collectionName string, docs []knowledgeport.VectorDocument) error {
	converted := make([]storagemilvus.DocumentChunk, len(docs))
	for i, doc := range docs {
		converted[i] = storagemilvus.DocumentChunk{
			ID: doc.ID, Content: doc.Content, SourceDocument: doc.SourceDocument,
			ChunkIndex: doc.ChunkIndex, Vector: doc.Vector,
		}
	}
	return a.store.Insert(ctx, collectionName, converted, "")
}

func (a *Adapter) Search(ctx context.Context, collectionName string, queryVector []float32, topK int) ([]knowledgeport.VectorSearchResult, error) {
	results, err := a.store.SearchWithFilterStrict(ctx, collectionName, queryVector, topK, "")
	if err != nil {
		return nil, err
	}
	converted := make([]knowledgeport.VectorSearchResult, len(results))
	for i, result := range results {
		converted[i] = knowledgeport.VectorSearchResult{
			ID: result.ID, Content: result.Content, SourceDocument: result.SourceDocument,
			ChunkIndex: result.ChunkIndex, Score: result.Score,
		}
	}
	return converted, nil
}

func (a *Adapter) SearchWithFilter(ctx context.Context, collectionName string, queryVector []float32, topK int, expression string) ([]knowledgeport.VectorSearchResult, error) {
	results, err := a.store.SearchWithFilterStrict(ctx, collectionName, queryVector, topK, expression)
	if err != nil {
		return nil, err
	}
	converted := make([]knowledgeport.VectorSearchResult, len(results))
	for i, result := range results {
		converted[i] = knowledgeport.VectorSearchResult{
			ID: result.ID, Content: result.Content, SourceDocument: result.SourceDocument,
			ChunkIndex: result.ChunkIndex, Score: result.Score,
		}
	}
	return converted, nil
}

func (a *Adapter) DescribeCollection(ctx context.Context, collectionName string) (knowledgeport.CollectionInfo, error) {
	info, err := a.store.DescribeCollection(ctx, collectionName)
	if err != nil {
		return knowledgeport.CollectionInfo{}, err
	}
	return knowledgeport.CollectionInfo{
		Dim: info.Dim, HasAgentID: info.HasAgentID, HasUserID: info.HasUserID,
	}, nil
}

func (a *Adapter) Flush(ctx context.Context, collectionName string) error {
	if err := a.store.Flush(ctx, collectionName); err != nil {
		return translateStoreError(err)
	}
	return nil
}

// translateStoreError maps transient store failures (Milvus unavailable or
// server-side rate-limited) onto the port-level VectorStoreUnavailableError so
// application code can retry without depending on pkg/storage/milvus. Non-
// transient errors pass through unchanged.
func translateStoreError(err error) error {
	var unavailable *storagemilvus.UnavailableError
	if errors.As(err, &unavailable) {
		return &knowledgeport.VectorStoreUnavailableError{Err: err}
	}
	return err
}

func (a *Adapter) DeleteCollection(ctx context.Context, collectionName string) error {
	return a.store.DeleteCollection(ctx, collectionName)
}

func (a *Adapter) CountVectors(ctx context.Context, collectionName string) (int64, error) {
	n, err := a.store.CountVectors(ctx, collectionName, "")
	if errors.Is(err, storagemilvus.ErrCollectionNotFound) {
		// A workspace that never ingested has no collection: zero vectors is
		// the correct stats answer, not a failure.
		return 0, nil
	}
	return n, err
}

func (a *Adapter) DeleteByDocumentIDs(ctx context.Context, collectionName string, docIDs []string) error {
	if len(docIDs) == 0 {
		return nil
	}
	if err := a.store.DeleteByDocumentIDs(ctx, collectionName, docIDs); err != nil {
		// A collection that never existed has nothing to purge — idempotent.
		if errors.Is(err, storagemilvus.ErrCollectionNotFound) {
			return nil
		}
		return translateStoreError(err)
	}
	return nil
}

var _ knowledgeport.VectorStore = (*Adapter)(nil)
