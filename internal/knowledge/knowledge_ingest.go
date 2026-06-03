// Package knowledge provides knowledge base and RAG services.
package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/document"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/textchunk"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/vector"
	"go.uber.org/zap"
)

type KnowledgeIngest struct {
	parser       *document.Parser
	chunker      *textchunk.Chunker
	embeddingSvc *embedding.EmbeddingService
	vectorStore  *vector.VectorStore
	graphRAG     *GraphRAG
	logger       *zap.Logger
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

type IngestDocumentRequest struct {
	TenantID     string
	Workspace    string
	DocumentData []byte
	FileName     string
	DocumentID   string
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
	ki.logger.Info("starting document ingestion",
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

	ki.logger.Info("document parsed", zap.Int("content_length", len(content)))

	chunks := ki.chunker.SmartChunk(content)
	result.TotalChunks = len(chunks)
	ki.logger.Info("text chunked", zap.Int("num_chunks", len(chunks)))

	// Batch embed all chunks in one API call
	chunkTexts := make([]string, len(chunks))
	for i, c := range chunks {
		chunkTexts[i] = c.Content
	}

	embedVecs, err := ki.embeddingSvc.EmbedBatch(ctx, chunkTexts)
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
	ki.logger.Info("vectors generated", zap.Int("count", len(docChunks)))

	collectionName := fmt.Sprintf("%s_kb", req.Workspace)
	if col, err := tenantdb.TenantCollection(ctx, "kb"); err == nil {
		collectionName = col
	}

	if err := ki.vectorStore.CreateCollection(ctx, collectionName); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			ki.logger.Error("failed to create collection", zap.Error(err))
			result.Errors = append(result.Errors, fmt.Sprintf("collection create failed: %v", err))
			return result, err
		}
	}

	if err := ki.vectorStore.Insert(ctx, collectionName, docChunks); err != nil {
		ki.logger.Error("failed to insert vectors", zap.Error(err))
		result.Errors = append(result.Errors, fmt.Sprintf("vector insert failed: %v", err))
		return result, fmt.Errorf("failed to insert vectors: %w", err)
	}

	if err := ki.vectorStore.Flush(ctx, collectionName); err != nil {
		ki.logger.Error("failed to flush vectors", zap.Error(err))
		result.Errors = append(result.Errors, fmt.Sprintf("flush failed: %v", err))
		return result, fmt.Errorf("failed to flush collection: %w", err)
	}

	ki.logger.Info("vectors inserted and flushed", zap.String("collection", collectionName))

	if ki.graphRAG != nil {
		ki.logger.Info("creating knowledge graph nodes")
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
			ki.logger.Warn("failed to create document node", zap.Error(err))
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
					ki.logger.Warn("failed to create chunk node", zap.Int("chunk", i), zap.Error(err))
					result.Errors = append(result.Errors, fmt.Sprintf("graph chunk %d node failed: %v", i, err))
					continue
				}
				result.TotalNodes++

				// Link chunk to its parent document
				if err := ki.graphRAG.CreateRelationship(ctx, req.DocumentID, chunkID, "HAS_CHUNK"); err != nil {
					ki.logger.Warn("failed to create HAS_CHUNK relationship", zap.Int("chunk", i), zap.Error(err))
					result.Errors = append(result.Errors, fmt.Sprintf("graph chunk %d relationship failed: %v", i, err))
				}
			}
		}
	}

	result.Duration = time.Since(startTime)

	if len(result.Errors) == 0 {
		ki.logger.Info("document ingestion completed",
			zap.String("document_id", result.DocumentID),
			zap.Int("total_chunks", result.TotalChunks),
			zap.Int("total_vectors", result.TotalVectors),
			zap.Int("total_nodes", result.TotalNodes),
			zap.Duration("duration", result.Duration))
	} else {
		ki.logger.Warn("document ingestion completed with errors",
			zap.Int("error_count", len(result.Errors)))
	}

	return result, nil
}

func (ki *KnowledgeIngest) IngestBatch(ctx context.Context, requests []IngestDocumentRequest) ([]IngestResult, error) {
	ki.logger.Info("starting batch ingestion", zap.Int("count", len(requests)))

	results := make([]IngestResult, len(requests))
	for i, req := range requests {
		result, err := ki.IngestDocument(ctx, req)
		if err != nil {
			ki.logger.Error("document ingestion failed",
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
	cypher := `
		MATCH (d:Document)
		WHERE d.workspace = $workspace
		RETURN count(d) as doc_count
	`
	docCountResult, err := ki.graphRAG.Query(ctx, cypher)
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
