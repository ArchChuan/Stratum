package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

const milvusSourceDocumentMaxLength = 255

type memoryFactSourceDocument struct {
	ConversationID string  `json:"conversation_id"`
	Importance     float64 `json:"importance"`
	Category       string  `json:"category"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
}

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
		chunk, err := memoryFactDocumentChunk(d)
		if err != nil {
			return fmt.Errorf("milvus upsert: document %q: %w", d.ID, err)
		}
		chunks[i] = chunk
	}
	return a.vs.Insert(ctx, collectionName, chunks, "")
}

func memoryFactDocumentChunk(doc *memport.VectorDoc) (storagemilvus.DocumentChunk, error) {
	if doc == nil {
		return storagemilvus.DocumentChunk{}, fmt.Errorf("invalid metadata: nil document")
	}
	userID, err := requiredMilvusMetadataString(doc.Metadata, "user_id")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	agentID, err := requiredMilvusMetadataString(doc.Metadata, "agent_id")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	scope, err := requiredMilvusMetadataString(doc.Metadata, "scope")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	content, err := requiredMilvusMetadataString(doc.Metadata, "content")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	conversationID, err := requiredMilvusMetadataString(doc.Metadata, "conversation_id")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	importance, err := requiredMilvusMetadataFloat64(doc.Metadata, "importance")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	category, err := requiredMilvusMetadataString(doc.Metadata, "category")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	confidence, err := requiredMilvusMetadataFloat64(doc.Metadata, "confidence")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}
	source, err := requiredMilvusMetadataString(doc.Metadata, "source")
	if err != nil {
		return storagemilvus.DocumentChunk{}, err
	}

	encoded, err := json.Marshal(memoryFactSourceDocument{
		ConversationID: conversationID,
		Importance:     importance,
		Category:       category,
		Confidence:     confidence,
		Source:         source,
	})
	if err != nil {
		return storagemilvus.DocumentChunk{}, fmt.Errorf("encode source_document metadata: %w", err)
	}
	if len(encoded) > milvusSourceDocumentMaxLength {
		return storagemilvus.DocumentChunk{}, fmt.Errorf("source_document metadata length %d exceeds 255-character limit", len(encoded))
	}

	return storagemilvus.DocumentChunk{
		ID:             doc.ID,
		Vector:         doc.Embedding,
		UserID:         userID,
		AgentID:        agentID,
		Scope:          scope,
		Content:        content,
		SourceDocument: string(encoded),
	}, nil
}

func requiredMilvusMetadataString(metadata map[string]interface{}, key string) (string, error) {
	value, ok := metadata[key]
	if !ok {
		return "", fmt.Errorf("invalid metadata: %q is required", key)
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("invalid metadata: %q must be a string", key)
	}
	return result, nil
}

func requiredMilvusMetadataFloat64(metadata map[string]interface{}, key string) (float64, error) {
	value, ok := metadata[key]
	if !ok {
		return 0, fmt.Errorf("invalid metadata: %q is required", key)
	}
	result, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("invalid metadata: %q must be a float64", key)
	}
	return result, nil
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
