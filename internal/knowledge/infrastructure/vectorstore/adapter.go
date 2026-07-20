package vectorstore

import (
	"context"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

type Adapter struct {
	store *storagemilvus.VectorStore
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
	results, err := a.store.Search(ctx, collectionName, queryVector, topK)
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

func (a *Adapter) Flush(ctx context.Context, collectionName string) error {
	return a.store.Flush(ctx, collectionName)
}

func (a *Adapter) DeleteCollection(ctx context.Context, collectionName string) error {
	return a.store.DeleteCollection(ctx, collectionName)
}

func (a *Adapter) CountVectors(ctx context.Context, collectionName string) (int64, error) {
	return a.store.CountVectors(ctx, collectionName, "")
}

var _ knowledgeport.VectorStore = (*Adapter)(nil)
