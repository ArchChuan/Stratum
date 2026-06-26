package persistence

import (
	"context"
	"fmt"
	"strings"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

// MilvusPortAdapter adapts *storagemilvus.VectorStore to memport.VectorStore.
type MilvusPortAdapter struct{ vs *storagemilvus.VectorStore }

func NewMilvusPortAdapter(vs *storagemilvus.VectorStore) *MilvusPortAdapter {
	return &MilvusPortAdapter{vs: vs}
}

func (a *MilvusPortAdapter) Delete(ctx context.Context, collectionName string, ids []string) error {
	return a.vs.DeleteByDocumentIDs(ctx, collectionName, ids)
}

func (a *MilvusPortAdapter) CreateCollection(ctx context.Context, collectionName string, dim int) error {
	return a.vs.CreateCollectionWithDim(ctx, collectionName, dim)
}

func (a *MilvusPortAdapter) Upsert(ctx context.Context, collectionName string, docs []*memport.VectorDoc) error {
	if len(docs) == 0 {
		return nil
	}
	dim := len(docs[0].Embedding)
	if dim == 0 {
		return fmt.Errorf("milvus upsert: zero-dimension vector")
	}
	if err := a.vs.CreateCollectionWithDim(ctx, collectionName, dim); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			return err
		}
	}
	chunks := make([]storagemilvus.DocumentChunk, len(docs))
	for i, d := range docs {
		chunks[i] = storagemilvus.DocumentChunk{
			ID:      d.ID,
			Vector:  d.Embedding,
			UserID:  milvusMetaStr(d.Metadata, "user_id"),
			AgentID: milvusMetaStr(d.Metadata, "agent_id"),
			Content: milvusMetaStr(d.Metadata, "content"),
		}
	}
	return a.vs.Insert(ctx, collectionName, chunks, "")
}

func (a *MilvusPortAdapter) Search(_ context.Context, _ string, _ []float32, _ int, _ map[string]interface{}) ([]*memport.VectorDoc, error) {
	return nil, nil
}

func (a *MilvusPortAdapter) DeleteAllByUser(ctx context.Context, tenantID, userID string) error {
	collectionName := "memory_" + strings.ReplaceAll(tenantID, "-", "_")
	return a.vs.DeleteByFilter(ctx, collectionName, fmt.Sprintf(`user_id == "%s"`, userID))
}

func (a *MilvusPortAdapter) DeleteAllByAgent(ctx context.Context, tenantID, agentID string) error {
	collectionName := "memory_" + strings.ReplaceAll(tenantID, "-", "_")
	return a.vs.DeleteByFilter(ctx, collectionName, fmt.Sprintf(`agent_id == "%s"`, agentID))
}

func milvusMetaStr(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
