package wiring

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/document"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence"
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
	db := c.dbOrNil()
	if db != nil {
		chunkRepo := persistence.NewChunkRepo(db)
		docRepo := persistence.NewDocRepo(db)
		if c.LLMGateway != nil && c.LLMGateway.Registry != nil {
			pipelineResolver = buildEmbedResolver(db, c.LLMGateway.Registry, c.Logger)
			knowledgeResolver = buildKnowledgeEmbedResolver(db, c.LLMGateway.Registry, c.Logger)
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
		EmbedResolver:     pipelineResolver,
		KnowledgeResolver: knowledgeResolver,
	}
	if db != nil {
		repo := persistence.NewWorkspaceRepo(db)
		c.Knowledge.WorkspaceService = knowledge.NewWorkspaceService(repo, ingest, c.Logger)
		c.Knowledge.WorkspaceService.SetDocRepo(persistence.NewDocRepo(db))
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

// buildEmbedResolver creates a per-tenant EmbedServiceResolver that resolves
// embedding capability from tenant DB settings via the ModelRegistry.
//
// Copied from api/router.go:344-417 — Task 10 will delete the
// router.go original once main.go is migrated to wiring.BuildContainer.
func buildEmbedResolver(
	db tenantSettingsQuerier,
	registry *llmgateway.ModelRegistry,
	logger *zap.Logger,
) pipeline.EmbedServiceResolver {
	return func(ctx context.Context, tenantID string) pipeline.EmbedClient {
		var settingsJSON []byte
		if err := db.QueryRow(ctx,
			"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
			tenantID,
		).Scan(&settingsJSON); err != nil {
			return nil
		}
		var settings map[string]interface{}
		if err := json.Unmarshal(settingsJSON, &settings); err != nil {
			return nil
		}
		embedModel, _ := settings["embed_model"].(string)
		if embedModel == "" {
			embedModel = "text-embedding-v3"
		}

		cfg, _, err := registry.ResolveEmbedding(ctx, tenantID, embedModel)
		if err != nil {
			return nil
		}
		client := llmgateway.NewOpenAICompatClient(cfg, logger)
		return embedding.NewEmbeddingServiceWithModel(client, embedModel, logger)
	}
}

// buildKnowledgeEmbedResolver returns a knowledge.EmbedResolver that resolves
// the embedding client for a given tenant, honouring the workspace-level model.
//
// Copied from api/router.go:421-491 — Task 10 will delete the
// router.go original once main.go is migrated to wiring.BuildContainer.
func buildKnowledgeEmbedResolver(
	db tenantSettingsQuerier,
	registry *llmgateway.ModelRegistry,
	logger *zap.Logger,
) knowledge.EmbedResolver {
	return func(ctx context.Context, tenantID, model string) knowledge.EmbedClient {
		var settingsJSON []byte
		if err := db.QueryRow(ctx,
			"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
			tenantID,
		).Scan(&settingsJSON); err != nil {
			return nil
		}
		var settings map[string]interface{}
		if err := json.Unmarshal(settingsJSON, &settings); err != nil {
			return nil
		}
		m := model
		if m == "" {
			if em, ok := settings["embed_model"].(string); ok && em != "" {
				m = em
			} else {
				m = "text-embedding-v3"
			}
		}

		cfg, _, err := registry.ResolveEmbedding(ctx, tenantID, m)
		if err != nil {
			return nil
		}
		client := llmgateway.NewOpenAICompatClient(cfg, logger)
		return embedding.NewEmbeddingServiceWithModel(client, m, logger)
	}
}
