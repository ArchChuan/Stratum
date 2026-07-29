package wiring

import (
	"context"

	"go.uber.org/zap"

	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/document"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/seeds"
	knowledgevector "github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/vectorstore"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	"github.com/byteBuilderX/stratum/pkg/textchunk"
	vectorstore "github.com/byteBuilderX/stratum/pkg/vector"
)

// Knowledge groups RAG/ingest services along with the per-tenant embed
// resolvers consumed by ingest, RAG, and the memory pipeline. The
// VectorStore here is the same Milvus client held by Storage, re-exposed
// as the typed alias used by knowledge/ingest.
type Knowledge struct {
	VectorStore       *vectorstore.VectorStore
	Parser            *document.Parser
	Chunker           *textchunk.Chunker
	EmbedSvc          *embedding.EmbeddingService
	Ingest            *knowledge.KnowledgeIngest
	RAGService        *knowledge.RAGService
	WorkspaceService  *knowledge.WorkspaceService
	DocRepo           *persistence.DocRepo
	EmbedResolver     pipeline.EmbedServiceResolver
	KnowledgeResolver knowledge.EmbedResolver
}

func (c *Container) buildKnowledge(ctx context.Context) error {
	vs := c.Storage.Milvus
	vectorAdapter := knowledgevector.New(vs)
	parser := document.NewParser(c.Logger)
	chunker := textchunk.NewChunker(c.Logger)
	chunking := document.NewChunkingService()

	var embedSvc *embedding.EmbeddingService
	var embedIface knowledge.EmbedClient
	if embedSvc != nil {
		embedIface = embedSvc
	}

	ingest := knowledge.NewKnowledgeIngest(parser, chunking, embedIface, vectorAdapter, c.Logger)
	rag := knowledge.NewRAGService(embedIface, vectorAdapter, c.Logger)

	var pipelineResolver pipeline.EmbedServiceResolver
	var knowledgeResolver knowledge.EmbedResolver
	var docRepo *persistence.DocRepo
	db := c.dbOrNil()
	if db != nil {
		chunkRepo := persistence.NewChunkRepo(db)
		docRepo = persistence.NewDocRepo(db)
		if c.LLMGateway != nil && c.LLMGateway.Registry != nil {
			pipelineResolver = buildEmbedResolver(c.LLMGateway.Registry, c.Logger)
			knowledgeResolver = buildKnowledgeEmbedResolver(c.LLMGateway.Registry, c.Logger)
		}
		ingest.SetEmbedResolver(knowledgeResolver)
		ingest.SetChunkRepo(chunkRepo)
		ingest.SetDocRepo(docRepo)
		rag.SetEmbedResolver(knowledgeResolver)
		rag.SetWorkspaceRepo(persistence.NewWorkspaceRepo(db))
		rag.SetChunkRepo(chunkRepo)
	}
	if c.Platform != nil && c.Platform.Metrics != nil {
		ingest.SetMetrics(c.Platform.Metrics)
	}
	c.shutdown = append(c.shutdown, ingest.Shutdown)

	c.Knowledge = &Knowledge{
		VectorStore:       vs,
		Parser:            parser,
		Chunker:           chunker,
		EmbedSvc:          embedSvc,
		Ingest:            ingest,
		RAGService:        rag,
		DocRepo:           docRepo,
		EmbedResolver:     pipelineResolver,
		KnowledgeResolver: knowledgeResolver,
	}
	if db != nil {
		repo := persistence.NewWorkspaceRepo(db)
		c.Knowledge.WorkspaceService = knowledge.NewWorkspaceService(repo, ingest, c.Logger)
		c.Knowledge.WorkspaceService.SetDocRepo(docRepo)
		c.Knowledge.WorkspaceService.SetVectorStore(vs)
	}
	return nil
}

// RecoverStuckKnowledgeIngests transitions any doc rows left in
// 'processing' longer than KnowledgeIngestStuckThreshold to 'failed'.
// Called on startup (after wiring completes) so the UI stops polling
// jobs abandoned by a crash. Iterates all tenants because a stuck row
// belongs to a specific tenant schema. Errors on individual tenants are
// logged and skipped: startup must not fail on partial recovery.
func (c *Container) RecoverStuckKnowledgeIngests(ctx context.Context) {
	if c.Knowledge == nil || c.Knowledge.Ingest == nil {
		return
	}
	db := c.dbOrNil()
	if db == nil {
		return
	}
	rows, err := db.Query(ctx, "SELECT id FROM public.tenants WHERE deleted_at IS NULL")
	if err != nil {
		c.Logger.Warn("knowledge.recover_stuck.list_tenants_failed", zap.Error(err))
		return
	}
	var tenantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			c.Logger.Warn("knowledge.recover_stuck.scan_failed", zap.Error(err))
			return
		}
		tenantIDs = append(tenantIDs, id)
	}
	rows.Close()
	total := 0
	for _, tid := range tenantIDs {
		n, err := c.Knowledge.Ingest.RecoverStuckIngests(ctx, tid)
		if err != nil {
			c.Logger.Warn("knowledge.recover_stuck.tenant_failed",
				zap.String("tenant_id", tid), zap.Error(err))
			continue
		}
		total += n
	}
	if total > 0 {
		c.Logger.Info("knowledge.recover_stuck.done", zap.Int("marked_failed", total))
	}
}

// buildEmbedResolver resolves the tenant's first available managed embedding model.
func buildEmbedResolver(
	registry *llmgateway.ModelRegistry,
	logger *zap.Logger,
) pipeline.EmbedServiceResolver {
	return func(ctx context.Context, tenantID string) pipeline.EmbedClient {
		models, err := registry.ListEmbeddingModelsByTenant(ctx, tenantID)
		if err != nil || len(models) == 0 {
			return nil
		}
		embedModel := models[0]
		cfg, _, err := registry.ResolveEmbedding(ctx, tenantID, embedModel)
		if err != nil {
			return nil
		}
		client := llmgateway.NewOpenAICompatClient(cfg, logger)
		return embedding.NewEmbeddingServiceWithModel(client, embedModel, logger)
	}
}

// buildKnowledgeEmbedResolver honours the workspace model and falls back to the managed embedding catalogue.
func buildKnowledgeEmbedResolver(
	registry *llmgateway.ModelRegistry,
	logger *zap.Logger,
) knowledge.EmbedResolver {
	return func(ctx context.Context, tenantID, model string) knowledge.EmbedClient {
		m := model
		if m == "" {
			models, err := registry.ListEmbeddingModelsByTenant(ctx, tenantID)
			if err != nil || len(models) == 0 {
				return nil
			}
			m = models[0]
		}

		cfg, _, err := registry.ResolveEmbedding(ctx, tenantID, m)
		if err != nil {
			return nil
		}
		client := llmgateway.NewOpenAICompatClient(cfg, logger)
		return embedding.NewEmbeddingServiceWithModel(client, m, logger)
	}
}

// SeedBuiltinKnowledgeDocs ingests official documentation catalog entries into
// the built-in stratum_docs workspace for every active tenant. Idempotent —
// existing docs (matched by content hash) are skipped. Errors are logged as
// warnings and do not prevent startup. Call after BootstrapTenants and
// RecoverStuckKnowledgeIngests.
func (c *Container) SeedBuiltinKnowledgeDocs(ctx context.Context) {
	if c.Knowledge == nil || c.Knowledge.Ingest == nil || c.Knowledge.DocRepo == nil {
		c.Logger.Debug("knowledge.seed_builtin_docs.skipped",
			zap.String("reason", "knowledge ingest or docRepo not wired"))
		return
	}
	db := c.dbOrNil()
	if db == nil {
		return
	}

	rows, err := db.Query(ctx, "SELECT id FROM public.tenants WHERE deleted_at IS NULL")
	if err != nil {
		c.Logger.Warn("knowledge.seed_builtin_docs.list_tenants_failed", zap.Error(err))
		return
	}
	defer rows.Close()

	var tenantIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			c.Logger.Warn("knowledge.seed_builtin_docs.scan_failed", zap.Error(err))
			return
		}
		tenantIDs = append(tenantIDs, id)
	}

	for _, tid := range tenantIDs {
		seeds.SeedBuiltinDocs(ctx, tid, "text-embedding-v3",
			c.Knowledge.Ingest, c.Knowledge.DocRepo, c.Logger)
	}
}
