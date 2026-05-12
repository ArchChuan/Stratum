package mcp

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
	"go.uber.org/zap"
)

type VectorStore struct {
	client   client.Client
	host     string
	port     string
	logger   *zap.Logger
	dim      int
}

func NewVectorStore(host, port string, logger *zap.Logger) *VectorStore {
	return &VectorStore{
		host:   host,
		port:   port,
		logger: logger,
		dim:    1536,
	}
}

func (vs *VectorStore) Connect(ctx context.Context) error {
	vs.logger.Info("connecting to Milvus", zap.String("host", vs.host), zap.String("port", vs.port))
	milvusAddr := fmt.Sprintf("%s:%s", vs.host, vs.port)
	c, err := client.NewGrpcClient(ctx, milvusAddr)
	if err != nil {
		vs.logger.Error("failed to connect to Milvus", zap.Error(err))
		return fmt.Errorf("failed to connect to Milvus: %w", err)
	}
	vs.client = c
	vs.logger.Info("connected to Milvus successfully")
	return nil
}

func (vs *VectorStore) CreateCollection(ctx context.Context, collectionName string) error {
	vs.logger.Info("creating collection", zap.String("collection", collectionName))
	schema := &client.Schema{
		CollectionName: collectionName,
		Description:    "RAG knowledge collection",
		Fields: []*client.Field{
			{
				Name:       "id",
				DataType:   client.FieldTypeVarChar,
				PrimaryKey: true,
				AutoID:    false,
				TypeParams: map[string]string{"max_length": "65535"},
			},
			{
				Name:     "content",
				DataType: client.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "65535"},
			},
			{
				Name:     "source_document",
				DataType: client.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "255"},
			},
			{
				Name:       "chunk_index",
				DataType:   client.FieldTypeInt64,
				PrimaryKey: false,
			},
			{
				Name:     "vector",
				DataType: client.FieldTypeFloatVector,
				TypeParams: map[string]string{"dim": fmt.Sprintf("%d", vs.dim)},
			},
		},
	}
	if err := vs.client.CreateCollection(ctx, schema, 2); err != nil {
		vs.logger.Error("failed to create collection", zap.String("collection", collectionName), zap.Error(err))
		return fmt.Errorf("failed to create collection %s: %w", collectionName, err)
	}
	vs.logger.Info("collection created successfully")
	return nil
}

func (vs *VectorStore) Insert(ctx context.Context, collectionName string, docs []DocumentChunk) error {
	if len(docs) == 0 {
		return nil
	}
	vs.logger.Debug("inserting vectors", zap.String("collection", collectionName), zap.Int("count", len(docs)))
	ids := make([]string, len(docs))
	contents := make([]string, len(docs))
	sources := make([]string, len(docs))
	chunkIndices := make([]int64, len(docs))
	vectors := make([][]float32, len(docs))
	for i, doc := range docs {
		ids[i] = doc.ID
		contents[i] = doc.Content
		sources[i] = doc.SourceDocument
		chunkIndices[i] = doc.ChunkIndex
		vectors[i] = doc.Vector
	}
	data := []client.RowData{
		{"id": ids, "content": contents, "source_document": sources, "chunk_index": chunkIndices, "vector": vectors},
	}
	if err := vs.client.Insert(ctx, collectionName, "", data); err != nil {
		vs.logger.Error("failed to insert vectors", zap.Error(err))
		return fmt.Errorf("failed to insert vectors: %w", err)
	}
	vs.logger.Info("vectors inserted successfully", zap.Int("count", len(docs)))
	return nil
}

func (vs *VectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int) ([]SearchResult, error) {
	vs.logger.Debug("searching vectors", zap.String("collection", collectionName), zap.Int("topK", topK))
	if err := vs.client.LoadCollection(ctx, collectionName, false); err != nil {
		vs.logger.Warn("failed to load collection", zap.Error(err))
	}
	searchVec := []float32(queryVector)
	sp, _ := entity.NewIndexFlatSearchParam()
	results, err := vs.client.Search(
		ctx, collectionName, []string{"vector"}, []entity.Vector{entity.FloatVector(searchVec)},
		"vector", []string{"content", "source_document", "chunk_index"}, topK, sp,
	)
	if err != nil {
		vs.logger.Error("failed to search vectors", zap.Error(err))
		return nil, fmt.Errorf("failed to search vectors: %w", err)
	}
	searchResults := make([]SearchResult, 0)
	if len(results) > 0 {
		for _, result := range results[0] {
			if len(result.Fields.Column("content")) > 0 {
				searchResults = append(searchResults, SearchResult{
					ID:            result.Fields.GetByName("id").(string),
					Content:        result.Fields.GetByName("content").(string),
					SourceDocument: result.Fields.GetByName("source_document").(string),
					ChunkIndex:     result.Fields.GetByName("chunk_index").(int64),
					Score:          result.Score,
				})
			}
		}
	}
	vs.logger.Debug("search completed", zap.Int("results", len(searchResults)))
	return searchResults, nil
}

func (vs *VectorStore) Flush(ctx context.Context, collectionName string) error {
	vs.logger.Debug("flushing collection", zap.String("collection", collectionName))
	if err := vs.client.Flush(ctx, collectionName, true); err != nil {
		vs.logger.Error("failed to flush collection", zap.Error(err))
		return fmt.Errorf("failed to flush collection: %w", err)
	}
	return nil
}

func (vs *VectorStore) DeleteCollection(ctx context.Context, collectionName string) error {
	vs.logger.Info("deleting collection", zap.String("collection", collectionName))
	if err := vs.client.DropCollection(ctx, collectionName); err != nil {
		vs.logger.Error("failed to delete collection", zap.Error(err))
		return fmt.Errorf("failed to delete collection: %w", err)
	}
	vs.logger.Info("collection deleted successfully")
	return nil
}

func (vs *VectorStore) Close() error {
	vs.logger.Info("closing Milvus connection")
	if vs.client != nil {
		return vs.client.Close()
	}
	return nil
}

type DocumentChunk struct {
	ID             string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Vector         []float32
}

type SearchResult struct {
	ID             string
	Content        string
	SourceDocument string
	ChunkIndex     int64
	Score          float32
}
