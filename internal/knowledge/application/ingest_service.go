// Package application implements knowledge bounded context use-cases.
package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/textchunk"
	"github.com/byteBuilderX/stratum/pkg/vector"
	"go.uber.org/zap"
)

// EmbedClient is an alias to the consumer-side embedder port. Kept exported so
// the per-tenant resolver type below stays stable for callers in api/wiring.
type EmbedClient = knowledgeport.Embedder

// EmbedResolver resolves an EmbedClient for a given tenant and model at request time.
type EmbedResolver func(ctx context.Context, tenantID, model string) EmbedClient

type KnowledgeIngest struct {
	parser        knowledgeport.DocumentParser
	chunker       *textchunk.Chunker
	embeddingSvc  knowledgeport.Embedder
	embedResolver EmbedResolver
	vectorStore   *vector.VectorStore
	chunkRepo     knowledgeport.ChunkRepo
	logger        *zap.Logger
}

func NewKnowledgeIngest(
	parser knowledgeport.DocumentParser,
	chunker *textchunk.Chunker,
	embeddingSvc knowledgeport.Embedder,
	vectorStore *vector.VectorStore,
	logger *zap.Logger,
) *KnowledgeIngest {
	return &KnowledgeIngest{
		parser:       parser,
		chunker:      chunker,
		embeddingSvc: embeddingSvc,
		vectorStore:  vectorStore,
		logger:       logger,
	}
}

// SetEmbedResolver injects a per-tenant/per-model embed resolver.
func (ki *KnowledgeIngest) SetEmbedResolver(r EmbedResolver) {
	ki.embedResolver = r
}

func (ki *KnowledgeIngest) SetChunkRepo(r knowledgeport.ChunkRepo) { ki.chunkRepo = r }

type IngestDocumentRequest struct {
	TenantID       string
	Workspace      string // display name (for logging)
	WorkspaceID    string // stable ID used for Milvus collection naming
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
	var embedClient knowledgeport.Embedder
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

	collectionName := constants.CollectionName(req.TenantID, req.WorkspaceID)

	if err := ki.vectorStore.CreateCollectionWithDim(ctx, collectionName, vectorDim(req.EmbeddingModel)); err != nil {
		return result, fmt.Errorf("failed to ensure vector collection: %w", err)
	}
	if err := ki.vectorStore.Insert(ctx, collectionName, docChunks, ""); err != nil {
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

	if ki.chunkRepo != nil && req.TenantID != "" {
		pgChunks := make([]domain.Chunk, len(docChunks))
		for i, dc := range docChunks {
			pgChunks[i] = domain.Chunk{ID: dc.ID, DocID: dc.SourceDocument, Text: dc.Content, Index: dc.ChunkIndex}
		}
		if err := ki.chunkRepo.InsertBatch(ctx, req.TenantID, req.Workspace, pgChunks); err != nil {
			ki.logger.Warn("knowledge.ingest.chunk_pg_failed", zap.Error(err))
		}
	}

	result.Duration = time.Since(startTime)

	if len(result.Errors) == 0 {
		ki.logger.Info("document ingestion completed",
			zap.String("trace_id", sc.TraceID),
			zap.String("document_id", result.DocumentID),
			zap.Int("total_chunks", result.TotalChunks),
			zap.Int("total_vectors", result.TotalVectors),
			zap.Duration("duration", result.Duration))
	} else {
		ki.logger.Warn("document ingestion completed with errors",
			zap.String("trace_id", sc.TraceID),
			zap.Int("error_count", len(result.Errors)))
	}

	return result, nil
}

func (ki *KnowledgeIngest) DeleteWorkspaceData(ctx context.Context, tenantID, workspaceName string) error {
	col := constants.CollectionName(tenantID, workspaceName)
	if err := ki.vectorStore.DeleteCollection(ctx, col); err != nil {
		return fmt.Errorf("failed to delete workspace collection: %w", err)
	}
	if ki.chunkRepo != nil {
		if err := ki.chunkRepo.DeleteByWorkspace(ctx, tenantID, workspaceName); err != nil {
			ki.logger.Warn("knowledge.workspace.chunk_pg_delete_failed", zap.Error(err))
		}
	}
	ki.logger.Info("knowledge.workspace.collection_deleted",
		zap.String("tenant_id", tenantID),
		zap.String("workspace", workspaceName),
		zap.String("collection", col))
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

func (ki *KnowledgeIngest) GetWorkspaceStats(ctx context.Context, tenantID, workspaceName string) (map[string]interface{}, error) {
	col := constants.CollectionName(tenantID, workspaceName)
	vectorCount, err := ki.vectorStore.CountVectors(ctx, col, "")
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"workspace":    workspaceName,
		"vector_count": vectorCount,
		"collection":   col,
	}, nil
}
