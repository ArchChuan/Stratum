package vector

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/milvus-io/milvus-sdk-go/v2/client"
	"github.com/milvus-io/milvus-sdk-go/v2/entity"
)

// MilvusStore implements domain/port.VectorStore using Milvus with HNSW index.
type MilvusStore struct {
	client client.Client
}

// NewMilvusStore creates a new Milvus vector store client.
func NewMilvusStore(address string) (*MilvusStore, error) {
	c, err := client.NewClient(context.Background(), client.Config{
		Address: address,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to milvus: %w", err)
	}

	return &MilvusStore{client: c}, nil
}

// Close closes the Milvus client connection.
func (m *MilvusStore) Close() error {
	return m.client.Close()
}

// CreateCollection initializes a vector collection with specified dimension and HNSW index.
func (m *MilvusStore) CreateCollection(ctx context.Context, collectionName string, dimension int) error {
	// Check if collection already exists
	has, err := m.client.HasCollection(ctx, collectionName)
	if err != nil {
		return fmt.Errorf("check collection exists: %w", err)
	}
	if has {
		return nil // Already exists
	}

	// Define schema
	schema := &entity.Schema{
		CollectionName: collectionName,
		AutoID:         false,
		Fields: []*entity.Field{
			{
				Name:       "id",
				DataType:   entity.FieldTypeVarChar,
				PrimaryKey: true,
				AutoID:     false,
				TypeParams: map[string]string{
					"max_length": "64",
				},
			},
			{
				Name:     "embedding",
				DataType: entity.FieldTypeFloatVector,
				TypeParams: map[string]string{
					"dim": fmt.Sprintf("%d", dimension),
				},
			},
			{
				Name:     "user_id",
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					"max_length": "64",
				},
			},
			{
				Name:     "content",
				DataType: entity.FieldTypeVarChar,
				TypeParams: map[string]string{
					"max_length": "2048",
				},
			},
		},
	}

	// Create collection
	err = m.client.CreateCollection(ctx, schema, entity.DefaultShardNumber)
	if err != nil {
		return fmt.Errorf("create collection: %w", err)
	}

	// Create HNSW index (M=16, efConstruction=200)
	idx, err := entity.NewIndexHNSW(entity.L2, 16, 200)
	if err != nil {
		return fmt.Errorf("create HNSW index: %w", err)
	}
	err = m.client.CreateIndex(ctx, collectionName, "embedding", idx, false)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	// Load collection into memory
	err = m.client.LoadCollection(ctx, collectionName, false)
	if err != nil {
		return fmt.Errorf("load collection: %w", err)
	}

	return nil
}

// Upsert inserts or updates document embeddings.
func (m *MilvusStore) Upsert(ctx context.Context, collectionName string, docs []*port.VectorDoc) error {
	if len(docs) == 0 {
		return nil
	}

	// Prepare columns
	ids := make([]string, len(docs))
	embeddings := make([][]float32, len(docs))
	userIDs := make([]string, len(docs))
	contents := make([]string, len(docs))

	for i, doc := range docs {
		ids[i] = doc.ID
		embeddings[i] = doc.Embedding

		if uid, ok := doc.Metadata["user_id"].(string); ok {
			userIDs[i] = uid
		}
		if content, ok := doc.Metadata["content"].(string); ok {
			contents[i] = content
		}
	}

	// Build columns
	idColumn := entity.NewColumnVarChar("id", ids)
	embeddingColumn := entity.NewColumnFloatVector("embedding", len(embeddings[0]), embeddings)
	userIDColumn := entity.NewColumnVarChar("user_id", userIDs)
	contentColumn := entity.NewColumnVarChar("content", contents)

	// Insert (Milvus handles upsert via primary key)
	_, err := m.client.Insert(ctx, collectionName, "", idColumn, embeddingColumn, userIDColumn, contentColumn)
	if err != nil {
		return fmt.Errorf("insert vectors: %w", err)
	}

	// Flush to ensure data is persisted
	err = m.client.Flush(ctx, collectionName, false)
	if err != nil {
		return fmt.Errorf("flush collection: %w", err)
	}

	return nil
}

// Search performs similarity search and returns top-k results.
func (m *MilvusStore) Search(ctx context.Context, collectionName string, queryVector []float32, topK int, filter map[string]interface{}) ([]*port.VectorDoc, error) {
	// Build filter expression
	expr := ""
	if filter != nil {
		if userID, ok := filter["user_id"].(string); ok {
			expr = fmt.Sprintf("user_id == '%s'", userID)
		}
	}

	// Search parameters
	sp, err := entity.NewIndexHNSWSearchParam(200) // ef for search
	if err != nil {
		return nil, fmt.Errorf("create search param: %w", err)
	}

	// Perform search
	results, err := m.client.Search(
		ctx,
		collectionName,
		[]string{},
		expr,
		[]string{"id", "user_id", "content"},
		[]entity.Vector{entity.FloatVector(queryVector)},
		"embedding",
		entity.L2,
		topK,
		sp,
	)
	if err != nil {
		return nil, fmt.Errorf("search vectors: %w", err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	// Parse results
	var docs []*port.VectorDoc
	if len(results) == 0 || results[0].ResultCount == 0 {
		return nil, nil
	}

	result := results[0]

	// Extract ID column
	idField := result.Fields.GetColumn("id")
	idColumn, ok := idField.(*entity.ColumnVarChar)
	if !ok {
		return nil, fmt.Errorf("invalid id column type")
	}

	// Extract other columns
	userIDColumn, _ := result.Fields.GetColumn("user_id").(*entity.ColumnVarChar)
	contentColumn, _ := result.Fields.GetColumn("content").(*entity.ColumnVarChar)

	// Build result documents
	for i := 0; i < result.ResultCount; i++ {
		doc := &port.VectorDoc{
			ID:         idColumn.Data()[i],
			Similarity: float64(result.Scores[i]),
			Metadata:   make(map[string]interface{}),
		}

		if userIDColumn != nil && i < len(userIDColumn.Data()) {
			doc.Metadata["user_id"] = userIDColumn.Data()[i]
		}
		if contentColumn != nil && i < len(contentColumn.Data()) {
			doc.Metadata["content"] = contentColumn.Data()[i]
		}

		docs = append(docs, doc)
	}

	return docs, nil
}

// Delete removes documents by IDs.
func (m *MilvusStore) Delete(ctx context.Context, collectionName string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// Build delete expression
	expr := fmt.Sprintf("id in %v", ids)

	err := m.client.DeleteByPks(ctx, collectionName, "", entity.NewColumnVarChar("id", ids))
	if err != nil {
		// Try expr-based delete as fallback
		err = m.client.Delete(ctx, collectionName, "", expr)
		if err != nil {
			return fmt.Errorf("delete vectors: %w", err)
		}
	}

	return nil
}
