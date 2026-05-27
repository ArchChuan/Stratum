package vector

import (
	"context"
	"fmt"
	"net"
	"time"

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

	// First check if the port is reachable using net.Dialer with timeout
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", milvusAddr)
	if err != nil {
		vs.logger.Warn("Milvus port not reachable", zap.Error(err))
		return fmt.Errorf("Milvus port not reachable: %w", err)
	}
	conn.Close()

	// Now try to create gRPC client
	type result struct {
		client client.Client
		err    error
	}
	resultCh := make(chan result, 1)

	go func() {
		c, err := client.NewGrpcClient(ctx, milvusAddr)
		resultCh <- result{client: c, err: err}
	}()

	select {
	case res := <-resultCh:
		if res.err != nil {
			vs.logger.Error("failed to connect to Milvus", zap.Error(res.err))
			return fmt.Errorf("failed to connect to Milvus: %w", res.err)
		}
		vs.client = res.client
		vs.logger.Info("connected to Milvus successfully")
		return nil
	case <-ctx.Done():
		vs.logger.Warn("Milvus connection timeout")
		return fmt.Errorf("Milvus connection timeout")
	}
}

func (vs *VectorStore) CreateCollection(ctx context.Context, collectionName string) error {
	vs.logger.Info("creating collection", zap.String("collection", collectionName))

	hasCollection, err := vs.client.HasCollection(ctx, collectionName)
	if err != nil {
		vs.logger.Error("failed to check collection", zap.Error(err))
		return fmt.Errorf("failed to check collection %s: %w", collectionName, err)
	}

	if hasCollection {
		vs.logger.Info("collection already exists", zap.String("collection", collectionName))
		return nil
	}

	schema := &entity.Schema{
		CollectionName: collectionName,
		Description:    "RAG knowledge collection",
			AutoID:         false,
			Fields: []*entity.Field{
			{
				Name:       "id",
				DataType:   entity.FieldTypeVarChar,
				PrimaryKey: true,
				TypeParams: map[string]string{"max_length": "65535"},
			},
			{
				Name:       "content",
				DataType:   entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "65535"},
			},
			{
				Name:       "source_document",
				DataType:  	entity.FieldTypeVarChar,
				TypeParams: map[string]string{"max_length": "255"},
			},
			{
				Name:     "chunk_index",
				DataType: entity.FieldTypeInt64,
			},
			{
				Name:     "vector",
				DataType: entity.FieldTypeFloatVector,
				TypeParams: map[string]string{
					"dim": fmt.Sprintf("%d", vs.dim),
				},
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

	idCol := entity.NewColumnVarChar("id", ids)
	contentCol := entity.NewColumnVarChar("content", contents)
	sourceCol := entity.NewColumnVarChar("source_document", sources)
	chunkIdxCol := entity.NewColumnInt64("chunk_index", chunkIndices)
	vectorCol := entity.NewColumnFloatVector("vector", vs.dim, vectors)

	_, err := vs.client.Insert(ctx, collectionName, "", idCol, contentCol, sourceCol, chunkIdxCol, vectorCol)
	if err != nil {
		vs.logger.Error("failed to insert vectors", zap.Error(err))
		return fmt.Errorf("failed to insert vectors: %w", err)
	}
	vs.logger.Info("vectors inserted successfully", zap.Int("count", len(docs)))
	return nil
}

func (vs *VectorStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int) ([]SearchResult, error) {
	vs.logger.Debug("searching vectors", zap.String("collection", collectionName), zap.Int("topK", topK))

	if err := vs.client.LoadCollection(ctx, collectionName, false); err != nil {
		vs.logger.Error("failed to load collection", zap.Error(err))
		return nil, fmt.Errorf("failed to load collection %s: %w", collectionName, err)
	}

	// Create search vector
	vectors := make([]entity.Vector, 1)
	vectors[0] = entity.FloatVector(queryVector)

	// Search parameters - L2 distance metric
	sp, err := entity.NewIndexFlatSearchParam()
	if err != nil {
		vs.logger.Error("failed to create search params", zap.Error(err))
		return nil, fmt.Errorf("failed to create search params: %w", err)
	}

	// Execute search
	results, searchErr := vs.client.Search(
		ctx,
		collectionName,
		[]string{}, // partition names
		"",         // expression (empty means no filtering)
		[]string{"id", "content", "source_document", "chunk_index"}, // output fields
		vectors,
		"vector",   // vector field name
		entity.L2,  // metric type
		topK,
		sp,
	)
	if searchErr != nil {
		vs.logger.Error("failed to search vectors", zap.Error(searchErr))
		return nil, fmt.Errorf("failed to search vectors: %w", searchErr)
	}

	// Process results - returns []client.SearchResult
	searchResults := make([]SearchResult, 0)
	if results != nil && len(results) > 0 {
		result := results[0]

		// Get columns from fields
		idCol := result.Fields.GetColumn("id")
		contentCol := result.Fields.GetColumn("content")
		sourceCol := result.Fields.GetColumn("source_document")
		chunkIdxCol := result.Fields.GetColumn("chunk_index")

		// Get scores from the result
		scores := result.Scores

		// Process each result (each result is one search with topK matches)
		for i := 0; i < result.ResultCount; i++ {
			var id, content, sourceDocument string
			var chunkIndex int64
			var score float32 = 0

			// Get ID
			if idCol != nil && i < idCol.Len() {
				if val, err := idCol.Get(i); err == nil {
					if idStr, ok := val.(string); ok {
						id = idStr
					}
				}
			}

			// Get content
			if contentCol != nil && i < contentCol.Len() {
				if val, err := contentCol.Get(i); err == nil {
					if contentStr, ok := val.(string); ok {
						content = contentStr
					}
				}
			}

			// Get source document
			if sourceCol != nil && i < sourceCol.Len() {
				if val, err := sourceCol.Get(i); err == nil {
					if sourceStr, ok := val.(string); ok {
						sourceDocument = sourceStr
					}
				}
			}

			// Get chunk index
			if chunkIdxCol != nil && i < chunkIdxCol.Len() {
				if val, err := chunkIdxCol.Get(i); err == nil {
					if idx, ok := val.(int64); ok {
						chunkIndex = idx
					}
				}
			}

			// Get score from result.Scores
			if i < len(scores) {
				score = float32(scores[i])
			}

			if id != "" && content != "" {
				searchResults = append(searchResults, SearchResult{
					ID:             id,
					Content:        content,
					SourceDocument: sourceDocument,
					ChunkIndex:     chunkIndex,
					Score:          score,
				})
			}
		}
	}

	vs.logger.Debug("search completed", zap.Int("results", len(searchResults)))
	return searchResults, nil
}

func (vs *VectorStore) Flush(ctx context.Context, collectionName string) error {
	vs.logger.Debug("flushing collection", zap.String("collection", collectionName))
	if err := vs.client.Flush(ctx, collectionName, false); err != nil {
		vs.logger.Error("failed to flush collection", zap.Error(err))
		return fmt.Errorf("failed to flush collection %s: %w", collectionName, err)
	}
	return nil
}

func (vs *VectorStore) DeleteCollection(ctx context.Context, collectionName string) error {
	vs.logger.Info("deleting collection", zap.String("collection", collectionName))
	if err := vs.client.DropCollection(ctx, collectionName); err != nil {
		vs.logger.Error("failed to delete collection", zap.String("collection", collectionName), zap.Error(err))
		return fmt.Errorf("failed to delete collection %s: %w", collectionName, err)
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
