package port

import "context"

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
	DescribeCollection(ctx context.Context, collectionName string) (CollectionInfo, error)
	Flush(ctx context.Context, collectionName string) error
	DeleteCollection(ctx context.Context, collectionName string) error
	CountVectors(ctx context.Context, collectionName string) (int64, error)
}
