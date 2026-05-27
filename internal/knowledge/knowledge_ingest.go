package knowledge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/document"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/embedding"
	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/textchunk"
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
	Workspace     string
	DocumentData  []byte
	FileName      string
	DocumentID    string
}

type IngestResult struct {
	DocumentID      string
	Workspace      string
	TotalChunks    int
	TotalVectors   int
	TotalNodes     int
	Duration       time.Duration
	Errors         []string
}

func (ki *KnowledgeIngest) IngestDocument(ctx context.Context, req IngestDocumentRequest) (*IngestResult, error) {
	startTime := time.Now()
	ki.logger.Info("starting document ingestion",
		zap.String("workspace", req.Workspace),
		zap.String("filename", req.FileName))

	result := &IngestResult{
		DocumentID: req.DocumentID,
		Workspace:   req.Workspace,
		Errors:      []string{},
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

	var vectors []vector.DocumentChunk
	for i, chunk := range chunks {
		embedVec, err := ki.embeddingSvc.EmbedVector(ctx, chunk.Content)
		if err != nil {
			ki.logger.Warn("failed to embed chunk", zap.Int("chunk_index", i), zap.Error(err))
			result.Errors = append(result.Errors, fmt.Sprintf("chunk %d embed failed: %v", i, err))
			continue
		}

		docID := fmt.Sprintf("%s_chunk_%d", req.DocumentID, i)
		vectors = append(vectors, vector.DocumentChunk{
			ID:             docID,
			Content:        chunk.Content,
			SourceDocument: req.DocumentID,
			ChunkIndex:     int64(i),
			Vector:         embedVec,
		})
	}

	result.TotalVectors = len(vectors)
	ki.logger.Info("vectors generated", zap.Int("count", len(vectors)))

	collectionName := fmt.Sprintf("%s_kb", req.Workspace)

	// Create collection if not exists
	if err := ki.vectorStore.CreateCollection(ctx, collectionName); err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			ki.logger.Error("failed to create collection", zap.Error(err))
			result.Errors = append(result.Errors, fmt.Sprintf("collection create failed: %v", err))
			return result, err
		}
		ki.logger.Info("collection already exists or created successfully")
	}

	if err := ki.vectorStore.Insert(ctx, collectionName, vectors); err != nil {
		ki.logger.Error("failed to insert vectors", zap.Error(err))
		result.Errors = append(result.Errors, fmt.Sprintf("vector insert failed: %v", err))
		return result, fmt.Errorf("failed to insert vectors: %w", err)
	}

	ki.logger.Info("vectors inserted", zap.String("collection", collectionName))

	if err := ki.vectorStore.Flush(ctx, collectionName); err != nil {
		ki.logger.Error("failed to flush vectors", zap.Error(err))
		result.Errors = append(result.Errors, fmt.Sprintf("flush failed: %v", err))
		return result, fmt.Errorf("failed to flush collection: %w", err)
	}

	ki.logger.Info("vectors flushed")

	if ki.graphRAG != nil {
		ki.logger.Info("creating knowledge graph nodes")
		docNodeProps := map[string]interface{}{
			"id":        req.DocumentID,
			"title":     req.FileName,
			"format":    req.FileName,
			"workspace": req.Workspace,
			"created_at": time.Now().Unix(),
		}

		if err := ki.graphRAG.CreateNode(ctx, "Document", docNodeProps); err != nil {
			ki.logger.Warn("failed to create document node", zap.Error(err))
			result.Errors = append(result.Errors, fmt.Sprintf("graph node create failed: %v", err))
		} else {
			result.TotalNodes++

			for i := 0; i < len(chunks); i++ {
				chunkProps := map[string]interface{}{
					"id":             fmt.Sprintf("%s_chunk_%d", req.DocumentID, i),
					"content":        chunks[i].Content,
					"chunk_index":    i,
					"created_at":      time.Now().Unix(),
				}

				if err := ki.graphRAG.CreateNode(ctx, "DocumentChunk", chunkProps); err != nil {
					ki.logger.Warn("failed to create chunk node", zap.Int("chunk", i), zap.Error(err))
				} else {
					result.TotalNodes++
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
		results[i] = *result
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
