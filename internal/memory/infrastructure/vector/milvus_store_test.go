package vector_test

import (
	"context"
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/vector"
	"github.com/stretchr/testify/require"
)

func setupMilvusTest(t *testing.T) *vector.MilvusStore {
	t.Helper()

	// Check if Milvus is available
	milvusAddr := os.Getenv("MILVUS_ADDRESS")
	if milvusAddr == "" {
		milvusAddr = "localhost:19530"
	}

	store, err := vector.NewMilvusStore(milvusAddr)
	if err != nil {
		t.Skipf("skipping test: Milvus not available at %s: %v", milvusAddr, err)
	}

	t.Cleanup(func() {
		store.Close()
	})

	return store
}

func TestMilvusStore_UpsertAndSearch(t *testing.T) {
	store := setupMilvusTest(t)
	ctx := context.Background()

	tenantID := "tenant_test_milvus"
	collectionName := "memory_facts_" + tenantID

	// Create collection
	err := store.CreateCollection(ctx, collectionName, 1536)
	require.NoError(t, err)

	// Upsert a document
	vector1 := make([]float32, 1536)
	for i := range vector1 {
		vector1[i] = float32(i) * 0.001
	}

	docs := []*port.VectorDoc{
		{
			ID:        "fact_001",
			Embedding: vector1,
			Metadata: map[string]interface{}{
				"user_id": "user123",
				"content": "User prefers dark mode",
			},
		},
	}

	err = store.Upsert(ctx, collectionName, docs)
	require.NoError(t, err)

	// Search for similar documents
	results, err := store.Search(ctx, collectionName, vector1, 5, map[string]interface{}{
		"user_id": "user123",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "fact_001", results[0].ID)
}

func TestMilvusStore_Delete(t *testing.T) {
	store := setupMilvusTest(t)
	ctx := context.Background()

	tenantID := "tenant_test_milvus_delete"
	collectionName := "memory_facts_" + tenantID

	err := store.CreateCollection(ctx, collectionName, 1536)
	require.NoError(t, err)

	vector1 := make([]float32, 1536)
	docs := []*port.VectorDoc{
		{
			ID:        "fact_to_delete",
			Embedding: vector1,
			Metadata:  map[string]interface{}{"user_id": "user123"},
		},
	}

	err = store.Upsert(ctx, collectionName, docs)
	require.NoError(t, err)

	err = store.Delete(ctx, collectionName, []string{"fact_to_delete"})
	require.NoError(t, err)

	// Search should return no results
	results, err := store.Search(ctx, collectionName, vector1, 5, nil)
	require.NoError(t, err)
	require.Empty(t, results)
}
