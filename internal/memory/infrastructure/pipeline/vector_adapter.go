package pipeline

import (
	"context"
	"strings"
	"sync"

	"github.com/byteBuilderX/stratum/pkg/vector"
)

// memoryCollectionName builds a Milvus-safe collection name for a tenant.
// Milvus only allows letters, digits, and underscores.
func memoryCollectionName(tenantID string) string {
	return "memory_" + strings.ReplaceAll(tenantID, "-", "_")
}

// DimResolver resolves the vector dimension for a tenant's memory collection.
type DimResolver func(ctx context.Context, tenantID string) int

// MilvusVectorAdapter adapts *vector.VectorStore to the pipeline VectorStore interface.
type MilvusVectorAdapter struct {
	vs          *vector.VectorStore
	ensured     sync.Map // tenantID -> struct{}; presence means collection already provisioned
	dimResolver DimResolver
}

// NewMilvusVectorAdapter creates a new adapter wrapping a VectorStore.
func NewMilvusVectorAdapter(vs *vector.VectorStore) *MilvusVectorAdapter {
	return &MilvusVectorAdapter{vs: vs}
}

// WithDimResolver sets a custom dimension resolver; returns the adapter for chaining.
func (a *MilvusVectorAdapter) WithDimResolver(r DimResolver) *MilvusVectorAdapter {
	a.dimResolver = r
	return a
}

func (a *MilvusVectorAdapter) resolveDim(ctx context.Context, tenantID string) int {
	if a.dimResolver != nil {
		return a.dimResolver(ctx, tenantID)
	}
	return 1536
}

// ensureCollection creates the memory collection for a tenant if not already done.
// Uses sync.Map as a per-tenant once gate; CreateCollectionWithDim itself is idempotent.
func (a *MilvusVectorAdapter) ensureCollection(ctx context.Context, tenantID string) error {
	if _, ok := a.ensured.Load(tenantID); ok {
		return nil
	}
	collectionName := memoryCollectionName(tenantID)
	dim := a.resolveDim(ctx, tenantID)
	if err := a.vs.CreateCollectionWithDim(ctx, collectionName, dim); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	a.ensured.Store(tenantID, struct{}{})
	return nil
}

// Upsert implements VectorStore by delegating to the underlying Milvus Insert.
func (a *MilvusVectorAdapter) Upsert(ctx context.Context, tenantID string, userID string, id string, vec []float32, metadata map[string]any) error {
	if err := a.ensureCollection(ctx, tenantID); err != nil {
		return err
	}
	collectionName := memoryCollectionName(tenantID)
	doc := vector.DocumentChunk{
		ID:             id,
		UserID:         userID,
		Content:        metadataString(metadata, "content"),
		SourceDocument: metadataString(metadata, "conversation_id"),
		ChunkIndex:     0,
		Vector:         vec,
	}
	return a.vs.Insert(ctx, collectionName, []vector.DocumentChunk{doc})
}

func metadataString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
