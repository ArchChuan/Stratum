// Package application implements knowledge bounded context use-cases.
package application

import (
	"context"
	"fmt"
	"sync"
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
	cleaner       *textchunk.TextCleaner
	strategies    map[string]textchunk.Strategy
	embeddingSvc  knowledgeport.Embedder
	embedResolver EmbedResolver
	vectorStore   *vector.VectorStore
	chunkRepo     knowledgeport.ChunkRepo
	docRepo       knowledgeport.DocRepo
	metrics       observability.MetricsProvider
	logger        *zap.Logger

	// queueSem caps total accepted (running + queued) ingest jobs.
	// Non-blocking acquire in the sync path; full → ErrIngestQueueFull → 429.
	queueSem chan struct{}
	// sem caps concurrent workers doing embed/persist inside goroutines.
	sem chan struct{}
	// wg tracks in-flight background goroutines for graceful shutdown.
	wg sync.WaitGroup
}

func NewKnowledgeIngest(
	parser knowledgeport.DocumentParser,
	chunker *textchunk.Chunker,
	embeddingSvc knowledgeport.Embedder,
	vectorStore *vector.VectorStore,
	logger *zap.Logger,
) *KnowledgeIngest {
	return &KnowledgeIngest{
		parser:  parser,
		chunker: chunker,
		cleaner: textchunk.NewTextCleaner(),
		strategies: map[string]textchunk.Strategy{
			"recursive":           textchunk.NewRecursiveStrategy(),
			"structure_recursive": textchunk.NewStructureRecursiveStrategy(),
			"semantic":            textchunk.NewSemanticStrategy(),
		},
		embeddingSvc: embeddingSvc,
		vectorStore:  vectorStore,
		logger:       logger,
		metrics:      observability.NoopMetrics{},
		queueSem:     make(chan struct{}, constants.IngestQueueCapacity),
		sem:          make(chan struct{}, constants.MaxConcurrentIngest),
	}
}

// SetEmbedResolver injects a per-tenant/per-model embed resolver.
func (ki *KnowledgeIngest) SetEmbedResolver(r EmbedResolver) { ki.embedResolver = r }

// SetChunkRepo injects the PG chunk repository (for keyword search dual-write).
func (ki *KnowledgeIngest) SetChunkRepo(r knowledgeport.ChunkRepo) { ki.chunkRepo = r }

// SetDocRepo injects the document repository (owns ingest status lifecycle).
func (ki *KnowledgeIngest) SetDocRepo(r knowledgeport.DocRepo) { ki.docRepo = r }

// SetMetrics injects the observability sink used by background jobs.
// Defaults to Noop; wiring provides Prometheus.
func (ki *KnowledgeIngest) SetMetrics(m observability.MetricsProvider) {
	if m == nil {
		return
	}
	ki.metrics = m
}

// Shutdown waits for all in-flight background ingest jobs to finish.
// Called from Harness Shutdown to preserve ordering guarantees.
func (ki *KnowledgeIngest) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		ki.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// RecoverStuckIngests marks docs stuck in 'processing' for longer than
// KnowledgeIngestStuckThreshold as 'failed'. Called on startup.
func (ki *KnowledgeIngest) RecoverStuckIngests(ctx context.Context, tenantID string) (int, error) {
	if ki.docRepo == nil {
		return 0, nil
	}
	return ki.docRepo.RecoverStuckIngests(ctx, tenantID, constants.KnowledgeIngestStuckThreshold)
}

type IngestDocumentRequest struct {
	TenantID         string
	Workspace        string // display name (for logging)
	WorkspaceID      string // stable ID used for Milvus collection naming
	EmbeddingModel   string
	ChunkingStrategy string // from workspace config; defaults to structure_recursive
	ChunkSize        int    // 0 → domain.DefaultChunkSize
	ChunkOverlap     int    // 0 → domain.DefaultChunkOverlap
	DocumentData     []byte
	FileName         string
	DocumentID       string
	ContentHash      string
}

type IngestResult struct {
	DocumentID   string
	Workspace    string
	Status       string
	TotalChunks  int
	TotalVectors int
	Duration     time.Duration
	Errors       []string
}

// IngestDocument accepts an ingest job and returns immediately once the
// document row has been persisted with status='processing'. The heavy work
// (embed + vector insert + PG chunk persist) runs in a detached goroutine.
//
// Synchronous path (fast): parse → chunk → chunk-count guard → queueSem
// acquire → docRepo.Save(processing). Any failure here returns the error
// verbatim; nothing is spawned.
//
// Backpressure: queueSem has capacity IngestQueueCapacity. If full,
// returns ErrIngestQueueFull → mapped to HTTP 429.
//
// The returned IngestResult has Status='processing' and TotalVectors=0.
// Terminal state is written to the docs table by the background goroutine
// and surfaced to the client via the docs list endpoint (front-end polls).
func (ki *KnowledgeIngest) IngestDocument(ctx context.Context, req IngestDocumentRequest) (*IngestResult, error) {
	sc, _ := observability.SpanFromContext(ctx)
	ki.logger.Info("knowledge.ingest.accepted",
		zap.String("trace_id", sc.TraceID),
		zap.String("workspace", req.Workspace),
		zap.String("document_id", req.DocumentID),
		zap.String("filename", req.FileName))

	content, err := ki.parser.ParseBytes(req.DocumentData, req.FileName)
	if err != nil {
		return nil, fmt.Errorf("failed to parse document: %w", err)
	}
	content = ki.cleaner.Clean(content)

	// Select chunking strategy from workspace config (defaults to structure_recursive).
	strategyName := req.ChunkingStrategy
	if strategyName == "" {
		strategyName = domain.DefaultChunkingStrategy
	}
	strategy, ok := ki.strategies[strategyName]
	if !ok {
		return nil, fmt.Errorf("unknown chunking strategy: %s", strategyName)
	}

	var chunkEmbedder textchunk.Embedder
	if strategyName == domain.ChunkingStrategySemantic {
		chunkEmbedder = ki.resolveEmbedClient(ctx, req)
	}

	// Chunk using the selected strategy. Semantic chunking receives the
	// workspace-configured embedding model through req.EmbeddingModel.
	chunkSize := req.ChunkSize
	if chunkSize <= 0 {
		chunkSize = domain.DefaultChunkSize
	}
	chunkOverlap := req.ChunkOverlap
	if chunkOverlap <= 0 {
		chunkOverlap = domain.DefaultChunkOverlap
	}
	chunkResult := strategy.Chunk(ctx, content, chunkSize, chunkOverlap, chunkEmbedder)
	if len(chunkResult.Leaves) == 0 {
		return nil, fmt.Errorf("document produced zero chunks after parsing")
	}
	if len(chunkResult.Leaves) > constants.MaxChunksPerDocument {
		return nil, fmt.Errorf("%w: got %d, max %d", domain.ErrChunkLimitExceeded, len(chunkResult.Leaves), constants.MaxChunksPerDocument)
	}
	chunkResult.Leaves = ki.cleaner.FilterChunks(chunkResult.Leaves)
	if len(chunkResult.Leaves) == 0 {
		return nil, fmt.Errorf("document produced zero chunks after cleaning")
	}

	// Non-blocking queue admission. Full → 429.
	select {
	case ki.queueSem <- struct{}{}:
	default:
		return nil, domain.ErrIngestQueueFull
	}

	// Persist doc row with processing status BEFORE spawning. If DB write
	// fails we release the queue slot and abort — client sees the error.
	if ki.docRepo != nil {
		doc := &domain.Document{
			ID:           req.DocumentID,
			KBID:         req.WorkspaceID,
			Source:       req.FileName,
			ContentHash:  req.ContentHash,
			IngestStatus: constants.IngestStatusProcessing,
			TotalChunks:  len(chunkResult.Leaves),
		}
		if err := ki.docRepo.Save(ctx, req.TenantID, req.WorkspaceID, doc); err != nil {
			<-ki.queueSem
			return nil, fmt.Errorf("failed to persist document metadata: %w", err)
		}
	}

	ki.wg.Add(1)
	go ki.runIngestJob(ctx, req, chunkResult)

	return &IngestResult{
		DocumentID:  req.DocumentID,
		Workspace:   req.Workspace,
		Status:      constants.IngestStatusProcessing,
		TotalChunks: len(chunkResult.Leaves),
		Errors:      []string{},
	}, nil
}

// runIngestJob executes the heavy path (embed + vector + PG chunks +
// terminal state write). Runs in a detached goroutine spawned by
// IngestDocument. Ctx is decoupled from the caller's request context so a
// client disconnect never aborts the job.
func (ki *KnowledgeIngest) runIngestJob(parentCtx context.Context, req IngestDocumentRequest, result textchunk.ChunkResult) {
	defer ki.wg.Done()
	defer func() { <-ki.queueSem }()

	// Detach from request lifecycle; keep trace + logger values via WithoutCancel.
	bgCtx, cancel := context.WithTimeout(context.WithoutCancel(parentCtx), constants.KnowledgeIngestTimeout)
	defer cancel()

	// Worker slot acquire (blocking, respects timeout).
	select {
	case ki.sem <- struct{}{}:
	case <-bgCtx.Done():
		ki.markFailed(bgCtx, req, fmt.Errorf("worker slot wait timed out: %w", bgCtx.Err()))
		return
	}
	defer func() { <-ki.sem }()

	sc, _ := observability.SpanFromContext(bgCtx)
	startTime := time.Now()
	ki.metrics.IncKnowledgeIngestInFlight()
	defer ki.metrics.DecKnowledgeIngestInFlight()

	// Panic recovery — a crashing job must not tear down the process.
	defer func() {
		if r := recover(); r != nil {
			ki.logger.Error("knowledge.ingest.panic",
				zap.String("trace_id", sc.TraceID),
				zap.String("document_id", req.DocumentID),
				zap.Any("panic", r))
			ki.markFailed(bgCtx, req, fmt.Errorf("ingest job panicked: %v", r))
		}
	}()

	err := ki.doEmbedAndPersist(bgCtx, req, result)
	duration := time.Since(startTime)
	ki.metrics.RecordKnowledgeIngestDuration(duration.Seconds())

	if err != nil {
		ki.logger.Error("knowledge.ingest.failed",
			zap.String("trace_id", sc.TraceID),
			zap.String("document_id", req.DocumentID),
			zap.Duration("duration", duration),
			zap.Error(err))
		ki.metrics.IncKnowledgeIngest(constants.IngestStatusFailed)
		ki.markFailed(bgCtx, req, err)
		return
	}

	if ki.docRepo != nil {
		if err := ki.docRepo.MarkIngestCompleted(bgCtx, req.TenantID, req.DocumentID, len(result.Leaves)); err != nil {
			ki.logger.Warn("knowledge.ingest.mark_completed_failed",
				zap.String("document_id", req.DocumentID),
				zap.Error(err))
		}
	}
	ki.metrics.IncKnowledgeIngest(constants.IngestStatusCompleted)
	ki.logger.Info("knowledge.ingest.completed",
		zap.String("trace_id", sc.TraceID),
		zap.String("document_id", req.DocumentID),
		zap.Int("total_chunks", len(result.Leaves)),
		zap.Duration("duration", duration))
}

// doEmbedAndPersist executes the embed → vector insert → PG chunk write
// pipeline. All I/O uses the detached bg context so a client abort cannot
// tear it down. Errors bubble unchanged; caller writes terminal status.
func (ki *KnowledgeIngest) doEmbedAndPersist(ctx context.Context, req IngestDocumentRequest, result textchunk.ChunkResult) error {
	sc, _ := observability.SpanFromContext(ctx)

	embedClient := ki.resolveEmbedClient(ctx, req)
	if embedClient == nil {
		return fmt.Errorf("embedding service not configured: set an embedding model in workspace settings")
	}

	chunkTexts := make([]string, len(result.Leaves))
	for i, c := range result.Leaves {
		chunkTexts[i] = c.Content
	}

	embedVecs, err := embedClient.EmbedBatch(ctx, chunkTexts)
	if err != nil {
		return fmt.Errorf("failed to embed chunks: %w", err)
	}
	if len(embedVecs) != len(result.Leaves) {
		return fmt.Errorf("embedding count mismatch: got %d vectors for %d chunks", len(embedVecs), len(result.Leaves))
	}

	docChunks := make([]vector.DocumentChunk, len(embedVecs))
	for i, vec := range embedVecs {
		docChunks[i] = vector.DocumentChunk{
			ID:             fmt.Sprintf("%s_chunk_%d", req.DocumentID, i),
			Content:        result.Leaves[i].Content,
			SourceDocument: req.DocumentID,
			ChunkIndex:     int64(i),
			Vector:         vec,
		}
	}

	collectionName := constants.CollectionName(req.TenantID, req.WorkspaceID)
	if err := ki.vectorStore.CreateCollectionWithDim(ctx, collectionName, vectorDim(req.EmbeddingModel)); err != nil {
		return fmt.Errorf("failed to ensure vector collection: %w", err)
	}
	if err := ki.vectorStore.Insert(ctx, collectionName, docChunks, ""); err != nil {
		return fmt.Errorf("failed to insert vectors: %w", err)
	}
	if err := ki.vectorStore.Flush(ctx, collectionName); err != nil {
		return fmt.Errorf("failed to flush collection: %w", err)
	}
	ki.logger.Info("knowledge.ingest.vectors_persisted",
		zap.String("trace_id", sc.TraceID),
		zap.String("collection", collectionName),
		zap.Int("count", len(docChunks)))

	if ki.chunkRepo != nil && req.TenantID != "" {
		// Persist parent chunks first (leaves reference them by ID).
		if len(result.Parents) > 0 {
			parents := make([]knowledgeport.ParentChunk, len(result.Parents))
			for i, p := range result.Parents {
				parents[i] = knowledgeport.ParentChunk{
					ID:      fmt.Sprintf("%s_parent_%d", req.DocumentID, i),
					DocID:   req.DocumentID,
					Index:   int64(i),
					Content: p.Content,
				}
			}
			if err := ki.chunkRepo.InsertParentBatch(ctx, req.TenantID, req.WorkspaceID, parents); err != nil {
				ki.logger.Warn("knowledge.ingest.parent_pg_failed",
					zap.String("trace_id", sc.TraceID),
					zap.Error(err))
			}
		}

		pgChunks := make([]domain.Chunk, len(docChunks))
		for i, dc := range docChunks {
			parentID := ""
			if i < len(result.Leaves) && result.Leaves[i].ParentID != "" {
				parentID = fmt.Sprintf("%s_parent_%s", req.DocumentID, result.Leaves[i].ParentID)
			}
			pgChunks[i] = domain.Chunk{
				ID:       dc.ID,
				DocID:    dc.SourceDocument,
				Text:     dc.Content,
				Index:    dc.ChunkIndex,
				ParentID: parentID,
			}
		}
		if err := ki.chunkRepo.InsertBatch(ctx, req.TenantID, req.WorkspaceID, pgChunks); err != nil {
			ki.logger.Warn("knowledge.ingest.chunk_pg_failed",
				zap.String("trace_id", sc.TraceID),
				zap.Error(err))
		}
	}
	return nil
}

func (ki *KnowledgeIngest) resolveEmbedClient(ctx context.Context, req IngestDocumentRequest) knowledgeport.Embedder {
	if ki.embedResolver != nil && req.TenantID != "" {
		return ki.embedResolver(ctx, req.TenantID, req.EmbeddingModel)
	}
	return ki.embeddingSvc
}

// markFailed writes the terminal 'failed' state. Best-effort — logs on
// repository error since the caller (goroutine) cannot propagate it.
func (ki *KnowledgeIngest) markFailed(ctx context.Context, req IngestDocumentRequest, cause error) {
	if ki.docRepo == nil {
		return
	}
	msg := cause.Error()
	if len(msg) > 512 {
		msg = msg[:512]
	}
	if err := ki.docRepo.MarkIngestFailed(ctx, req.TenantID, req.DocumentID, msg); err != nil {
		ki.logger.Warn("knowledge.ingest.mark_failed_write_failed",
			zap.String("document_id", req.DocumentID),
			zap.Error(err))
	}
}

// DeleteWorkspaceData purges the vector collection and PG chunks for a workspace.
func (ki *KnowledgeIngest) DeleteWorkspaceData(ctx context.Context, tenantID, workspaceID string) error {
	col := constants.CollectionName(tenantID, workspaceID)
	if err := ki.vectorStore.DeleteCollection(ctx, col); err != nil {
		return fmt.Errorf("failed to delete workspace collection: %w", err)
	}
	if ki.chunkRepo != nil {
		if err := ki.chunkRepo.DeleteByWorkspace(ctx, tenantID, workspaceID); err != nil {
			ki.logger.Warn("knowledge.workspace.chunk_pg_delete_failed", zap.Error(err))
		}
	}
	ki.logger.Info("knowledge.workspace.collection_deleted",
		zap.String("tenant_id", tenantID),
		zap.String("workspace_id", workspaceID),
		zap.String("collection", col))
	return nil
}

// IngestBatch dispatches N ingest jobs sequentially through the accept path
// and returns the immediate (Status='processing') results. Front-end polls
// docs list for terminal state.
func (ki *KnowledgeIngest) IngestBatch(ctx context.Context, requests []IngestDocumentRequest) ([]IngestResult, error) {
	bsc, _ := observability.SpanFromContext(ctx)
	ki.logger.Info("knowledge.ingest.batch_accepted",
		zap.String("trace_id", bsc.TraceID),
		zap.Int("count", len(requests)))

	results := make([]IngestResult, len(requests))
	for i, req := range requests {
		result, err := ki.IngestDocument(ctx, req)
		if err != nil {
			ki.logger.Error("knowledge.ingest.batch_item_failed",
				zap.String("trace_id", bsc.TraceID),
				zap.Int("index", i),
				zap.String("document_id", req.DocumentID),
				zap.Error(err))
			if result != nil {
				results[i] = *result
			} else {
				results[i] = IngestResult{
					DocumentID: req.DocumentID,
					Status:     constants.IngestStatusFailed,
					Errors:     []string{err.Error()},
				}
			}
			continue
		}
		results[i] = *result
	}
	return results, nil
}

// GetWorkspaceStats returns vector counts for a workspace collection.
func (ki *KnowledgeIngest) GetWorkspaceStats(ctx context.Context, tenantID, workspaceID string) (map[string]interface{}, error) {
	col := constants.CollectionName(tenantID, workspaceID)
	vectorCount, err := ki.vectorStore.CountVectors(ctx, col, "")
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"workspace":    workspaceID,
		"vector_count": vectorCount,
		"collection":   col,
	}, nil
}
