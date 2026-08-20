package pipeline

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/vector"
)

// memoryCollectionName builds the Milvus collection name for a tenant's
// raw-turn vectors, encoding the embedding model suffix so switching models
// isolates data into a fresh collection.
func memoryCollectionName(tenantID, model string) string {
	return "memory_" + strings.ReplaceAll(tenantID, "-", "_") + "_" + constants.SanitizeMilvusName(model)
}

// memoryFactsCollectionName builds the collection name for LLM-extracted facts.
func memoryFactsCollectionName(tenantID, model string) string {
	return "memory_facts_" + strings.ReplaceAll(tenantID, "-", "_") + "_" + constants.SanitizeMilvusName(model)
}

// memoryCollectionLegacyName / memoryFactsCollectionLegacyName 是无模型后缀的
// 存量 collection 名（升级前数据），仅查询回退使用；写入永远走新名。
func memoryCollectionLegacyName(tenantID string) string {
	return "memory_" + strings.ReplaceAll(tenantID, "-", "_")
}

func memoryFactsCollectionLegacyName(tenantID string) string {
	return "memory_facts_" + strings.ReplaceAll(tenantID, "-", "_")
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
// Uses sync.Map as a per-(tenant, model) once gate; CreateCollectionWithDim itself is idempotent.
func (a *MilvusVectorAdapter) ensureCollection(ctx context.Context, tenantID, model string) error {
	key := tenantID + "/" + model
	if _, ok := a.ensured.Load(key); ok {
		return nil
	}
	collectionName := memoryCollectionName(tenantID, model)
	dim := a.resolveDim(ctx, tenantID)
	if dim <= 0 {
		// fail-closed：租户未显式配置记忆嵌入模型 → 不建 collection，Upsert
		// 上层失败 → 消息进 DLQ。绝不回退默认维度建错集合。
		return fmt.Errorf("memory collection %s: no embedding model configured for tenant", collectionName)
	}
	if err := a.vs.CreateCollectionWithDim(ctx, collectionName, dim); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	a.ensured.Store(key, struct{}{})
	return nil
}

// Upsert implements VectorStore by delegating to the underlying Milvus Insert.
func (a *MilvusVectorAdapter) Upsert(ctx context.Context, tenantID string, userID string, id string, model string, vec []float32, metadata map[string]any) error {
	if err := a.ensureCollection(ctx, tenantID, model); err != nil {
		return err
	}
	collectionName := memoryCollectionName(tenantID, model)
	doc := vector.DocumentChunk{
		ID:             id,
		UserID:         userID,
		AgentID:        metadataString(metadata, "agent_id"),
		Scope:          metadataString(metadata, "scope"),
		Content:        metadataString(metadata, "content"),
		SourceDocument: metadataString(metadata, "conversation_id"),
		ChunkIndex:     0,
		Vector:         vec,
	}
	return a.vs.Insert(ctx, collectionName, []vector.DocumentChunk{doc}, "")
}

func metadataString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
