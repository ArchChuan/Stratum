// Package knowledge provides knowledge base and RAG services.
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/document"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/byteBuilderX/stratum/pkg/textchunk"
	"github.com/byteBuilderX/stratum/pkg/vector"
	"go.uber.org/zap"
)

// EmbedClient is the minimal interface KnowledgeIngest needs for vectorization.
type EmbedClient interface {
	EmbedVector(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbedResolver resolves an EmbedClient for a given tenant and model at request time.
type EmbedResolver func(ctx context.Context, tenantID, model string) EmbedClient

type KnowledgeIngest struct {
	parser        *document.Parser
	chunker       *textchunk.Chunker
	embeddingSvc  *embedding.EmbeddingService
	embedResolver EmbedResolver
	vectorStore   *vector.VectorStore
	graphRAG      *GraphRAG
	logger        *zap.Logger
}

func NewKnowledgeIngest(
	parser *document.Parser,
	chunker *textchunk.Chunker,
	embeddingSvc *embedding.EmbeddingService,
	vectorStore *vector.VectorStore,
	graphRAG *GraphRAG,
	logger *zap.Logger,
) *KnowledgeIngest {
	return &KnowledgeIngest{
		parser:       parser,
		chunker:      chunker,
		embeddingSvc: embeddingSvc,
		vectorStore:  vectorStore,
		graphRAG:     graphRAG,
		logger:       logger,
	}
}

// SetEmbedResolver injects a per-tenant/per-model embed resolver.
func (ki *KnowledgeIngest) SetEmbedResolver(r EmbedResolver) {
	ki.embedResolver = r
}

type IngestDocumentRequest struct {
	TenantID       string
	Workspace      string
	EmbeddingModel string
	DocumentData   []byte
	FileName       string
	DocumentID     string
}

type IngestResult struct {
	DocumentID   string
	Workspace    string
	TotalChunks  int
	TotalVectors int
	TotalNodes   int
	Duration     time.Duration
	Errors       []string
}

func (ki *KnowledgeIngest) IngestDocument(ctx context.Context, req IngestDocumentRequest) (*IngestResult, error) {
	startTime := time.Now()
	sc, _ := observability.SpanFromContext(ctx)
	ki.logger.Info("starting document ingestion",
		zap.String("trace_id", sc.TraceID),
		zap.String("workspace", req.Workspace),
		zap.String("filename", req.FileName))

	result := &IngestResult{
		DocumentID: req.DocumentID,
		Workspace:  req.Workspace,
		Errors:     []string{},
	}

	content, err := ki.parser.ParseBytes(req.DocumentData, req.FileName)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("parse failed: %v", err))
		return result, fmt.Errorf("failed to parse document: %w", err)
	}

	ki.logger.Info("document parsed", zap.String("trace_id", sc.TraceID), zap.Int("content_length", len(content)))

	chunks := ki.chunker.SmartChunk(content)
	result.TotalChunks = len(chunks)
	ki.logger.Info("text chunked", zap.String("trace_id", sc.TraceID), zap.Int("num_chunks", len(chunks)))

	// Resolve embed client: prefer per-workspace resolver, fall back to global svc.
	var embedClient EmbedClient
	if ki.embedResolver != nil && req.TenantID != "" {
		embedClient = ki.embedResolver(ctx, req.TenantID, req.EmbeddingModel)
	}
	if embedClient == nil {
		embedClient = ki.embeddingSvc
	}
	if embedClient == nil {
		return result, fmt.Errorf("embedding service not configured: set an embedding model in workspace settings")
	}

	// Batch embed all chunks in one API call
	chunkTexts := make([]string, len(chunks))
	for i, c := range chunks {
		chunkTexts[i] = c.Content
	}

	embedVecs, err := embedClient.EmbedBatch(ctx, chunkTexts)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("batch embed failed: %v", err))
		return result, fmt.Errorf("failed to embed chunks: %w", err)
	}

	if len(embedVecs) != len(chunks) {
		msg := fmt.Sprintf("embedding count mismatch: got %d vectors for %d chunks", len(embedVecs), len(chunks))
		result.Errors = append(result.Errors, msg)
		return result, errors.New(msg)
	}

	docChunks := make([]vector.DocumentChunk, len(embedVecs))
	for i, vec := range embedVecs {
		docChunks[i] = vector.DocumentChunk{
			ID:             fmt.Sprintf("%s_chunk_%d", req.DocumentID, i),
			Content:        chunks[i].Content,
			SourceDocument: req.DocumentID,
			ChunkIndex:     int64(i),
			Vector:         vec,
		}
	}

	result.TotalVectors = len(docChunks)
	ki.logger.Info("vectors generated", zap.String("trace_id", sc.TraceID), zap.Int("count", len(docChunks)))

	collectionName := fmt.Sprintf("%s_kb", req.Workspace)
	if col, err := tenantdb.WorkspaceCollection(ctx, req.Workspace); err == nil {
		collectionName = col
	}

	if err := ki.vectorStore.CreateCollection(ctx, collectionName); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			ki.logger.Error("failed to create collection", zap.String("trace_id", sc.TraceID), zap.Error(err))
			result.Errors = append(result.Errors, fmt.Sprintf("collection create failed: %v", err))
			return result, err
		}
	}

	if err := ki.vectorStore.Insert(ctx, collectionName, docChunks); err != nil {
		ki.logger.Error("failed to insert vectors", zap.String("trace_id", sc.TraceID), zap.Error(err))
		result.Errors = append(result.Errors, fmt.Sprintf("vector insert failed: %v", err))
		return result, fmt.Errorf("failed to insert vectors: %w", err)
	}

	if err := ki.vectorStore.Flush(ctx, collectionName); err != nil {
		ki.logger.Error("failed to flush vectors", zap.String("trace_id", sc.TraceID), zap.Error(err))
		result.Errors = append(result.Errors, fmt.Sprintf("flush failed: %v", err))
		return result, fmt.Errorf("failed to flush collection: %w", err)
	}

	ki.logger.Info("vectors inserted and flushed", zap.String("trace_id", sc.TraceID), zap.String("collection", collectionName))

	if ki.graphRAG != nil {
		ki.logger.Info("creating knowledge graph nodes", zap.String("trace_id", sc.TraceID))
		now := time.Now().Unix()

		docNodeProps := map[string]interface{}{
			"id":         req.DocumentID,
			"title":      req.FileName,
			"workspace":  req.Workspace,
			"created_at": now,
		}

		docLabel := "Document"
		if l, err := tenantdb.TenantLabel(ctx, "Document"); err == nil {
			docLabel = l
		}
		if err := ki.graphRAG.CreateNode(ctx, docLabel, docNodeProps); err != nil {
			ki.logger.Warn("failed to create document node", zap.String("trace_id", sc.TraceID), zap.Error(err))
			result.Errors = append(result.Errors, fmt.Sprintf("graph document node failed: %v", err))
		} else {
			result.TotalNodes++

			for i, chunk := range chunks {
				chunkID := fmt.Sprintf("%s_chunk_%d", req.DocumentID, i)
				chunkProps := map[string]interface{}{
					"id":          chunkID,
					"content":     chunk.Content,
					"chunk_index": i,
					"created_at":  now,
				}

				chunkLabel := "DocumentChunk"
				if l, err := tenantdb.TenantLabel(ctx, "DocumentChunk"); err == nil {
					chunkLabel = l
				}
				if err := ki.graphRAG.CreateNode(ctx, chunkLabel, chunkProps); err != nil {
					ki.logger.Warn("failed to create chunk node", zap.String("trace_id", sc.TraceID), zap.Int("chunk", i), zap.Error(err))
					result.Errors = append(result.Errors, fmt.Sprintf("graph chunk %d node failed: %v", i, err))
					continue
				}
				result.TotalNodes++

				// Link chunk to its parent document
				if err := ki.graphRAG.CreateRelationship(ctx, req.DocumentID, chunkID, "HAS_CHUNK"); err != nil {
					ki.logger.Warn("failed to create HAS_CHUNK relationship", zap.String("trace_id", sc.TraceID), zap.Int("chunk", i), zap.Error(err))
					result.Errors = append(result.Errors, fmt.Sprintf("graph chunk %d relationship failed: %v", i, err))
				}
			}
		}
	}

	result.Duration = time.Since(startTime)

	if len(result.Errors) == 0 {
		ki.logger.Info("document ingestion completed",
			zap.String("trace_id", sc.TraceID),
			zap.String("document_id", result.DocumentID),
			zap.Int("total_chunks", result.TotalChunks),
			zap.Int("total_vectors", result.TotalVectors),
			zap.Int("total_nodes", result.TotalNodes),
			zap.Duration("duration", result.Duration))
	} else {
		ki.logger.Warn("document ingestion completed with errors",
			zap.String("trace_id", sc.TraceID),
			zap.Int("error_count", len(result.Errors)))
	}

	return result, nil
}

// DeleteWorkspaceData removes all Milvus vectors and Neo4j nodes for the given
// workspace. Deletion order: drop Milvus collection → delete Neo4j →
// caller deletes PG. Collection-per-workspace means a single DropCollection
// replaces the prior docID-query + per-vector delete.
func (ki *KnowledgeIngest) DeleteWorkspaceData(ctx context.Context, workspace string) error {
	collectionName := fmt.Sprintf("%s_kb", workspace)
	if col, err := tenantdb.WorkspaceCollection(ctx, workspace); err == nil {
		collectionName = col
	}

	// Step 1: drop the per-workspace Milvus collection
	if err := ki.vectorStore.DeleteCollection(ctx, collectionName); err != nil {
		return fmt.Errorf("failed to delete workspace collection: %w", err)
	}

	// Step 2: delete Neo4j nodes (idempotent — DETACH DELETE on absent nodes is a no-op)
	if ki.graphRAG != nil {
		if err := ki.graphRAG.DeleteWorkspaceNodes(ctx, workspace); err != nil {
			return fmt.Errorf("failed to delete workspace graph nodes: %w", err)
		}
	}

	ki.logger.Info("workspace storage resources deleted",
		zap.String("trace_id", func() string { sc, _ := observability.SpanFromContext(ctx); return sc.TraceID }()),
		zap.String("workspace", workspace))
	return nil
}

func (ki *KnowledgeIngest) IngestBatch(ctx context.Context, requests []IngestDocumentRequest) ([]IngestResult, error) {
	bsc, _ := observability.SpanFromContext(ctx)
	ki.logger.Info("starting batch ingestion", zap.String("trace_id", bsc.TraceID), zap.Int("count", len(requests)))

	results := make([]IngestResult, len(requests))
	for i, req := range requests {
		result, err := ki.IngestDocument(ctx, req)
		if err != nil {
			ki.logger.Error("document ingestion failed",
				zap.String("trace_id", bsc.TraceID),
				zap.Int("index", i),
				zap.String("document_id", req.DocumentID),
				zap.Error(err))
		}
		if result != nil {
			results[i] = *result
		} else {
			results[i] = IngestResult{
				DocumentID: req.DocumentID,
				Errors:     []string{"internal error: nil result"},
			}
		}
	}

	return results, nil
}

func (ki *KnowledgeIngest) GetWorkspaceStats(ctx context.Context, workspace string) (map[string]interface{}, error) {
	collectionName := fmt.Sprintf("%s_kb", workspace)
	if col, err := tenantdb.WorkspaceCollection(ctx, workspace); err == nil {
		collectionName = col
	}
	cypher := `
		MATCH (d:Document)
		WHERE d.workspace = $workspace
		RETURN count(d) as doc_count
	`
	docCountResult, err := ki.graphRAG.Query(ctx, cypher, map[string]interface{}{"workspace": workspace})
	if err != nil {
		return nil, err
	}

	docCount := 0
	if resultList, ok := docCountResult.([]interface{}); ok && len(resultList) > 0 {
		if m, ok := resultList[0].(map[string]interface{}); ok {
			if c, ok := m["doc_count"].(int64); ok {
				docCount = int(c)
			}
		}
	}

	stats := map[string]interface{}{
		"workspace":      workspace,
		"document_count": docCount,
		"collection":     collectionName,
	}

	return stats, nil
}
