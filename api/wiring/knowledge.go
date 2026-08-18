package wiring

import (
	"context"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/officialdocs"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/document"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/rerank"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/seeds"
	knowledgevector "github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/vectorstore"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/httpclient"
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
		// Doc-level whitelist filtering needs the document repo; the tenant
		// role resolver is injected with the other services by
		// injectTenantRoleResolvers (same DB-backed adapter).
		rag.SetDocRepo(docRepo)
	}
	// 证据充分性 judge（生成前门，仅 evidence 路径）：fail-closed——gateway
	// 不可用或未启用时保持 nil，检索行为与不配置完全一致。单独成方法以
	// 控制 buildKnowledge 的圈复杂度。
	c.wireKnowledgeJudge(rag)
	if c.Platform != nil && c.Platform.Metrics != nil {
		ingest.SetMetrics(c.Platform.Metrics)
		rag.SetMetrics(c.Platform.Metrics)
		if c.Config.RerankConfigured() {
			// RerankHTTPRetryMax is the retry budget; WithRetry counts total
			// attempts, hence +1.
			doer := httpclient.New(
				httpclient.WithTimeout(constants.RerankHTTPTimeout),
				httpclient.WithRetry(constants.RerankHTTPRetryMax+1),
			)
			rag.SetReranker(rerank.NewCohereReranker(
				c.Config.RerankBaseURL, c.Config.RerankAPIKey, c.Config.RerankModel,
				doer, c.Platform.Metrics, c.Logger,
			))
		}
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
		c.Knowledge.WorkspaceService.SetEditorRepo(persistence.NewPgResourceEditorRepo(db))
	}
	c.wireKnowledgeModelExists()
	return nil
}

// wireKnowledgeJudge 在 LLM gateway 可用且 KNOWLEDGE_JUDGE_ENABLED 时注入
// 证据充分性 judge；任一条件不满足保持 nil（fail-closed 放行）。单独成方法
// 以控制 buildKnowledge 的圈复杂度。
func (c *Container) wireKnowledgeJudge(rag *knowledge.RAGService) {
	if c.LLMGateway == nil || c.LLMGateway.Gateway == nil || !c.Config.KnowledgeJudge.Enabled {
		return
	}
	judge := knowledgeJudge{
		completer: c.LLMGateway.Gateway,
		model:     c.Config.KnowledgeJudge.Model,
		timeout:   c.Config.KnowledgeJudge.Timeout,
	}
	if c.Platform != nil {
		judge.metrics = c.Platform.Metrics
	}
	rag.SetSufficiencyJudge(judge)
}

// wireKnowledgeModelExists 在 Platform 提供全局模型目录时注入 knowledge 的
// ModelExists 适配器；WorkspaceService 未装配（无 DB）或目录缺失时跳过。
// 单独成方法以控制 buildKnowledge 的圈复杂度。
func (c *Container) wireKnowledgeModelExists() {
	if c.Knowledge == nil || c.Knowledge.WorkspaceService == nil {
		return
	}
	if c.Platform != nil && c.Platform.ModelRegistry != nil {
		c.Knowledge.WorkspaceService.SetModelExists(knowledgeModelExistsAdapter{registry: c.Platform.ModelRegistry})
	}
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
	if c.IAM == nil || c.IAM.TenantRepo == nil {
		return
	}
	tenantIDs, err := c.IAM.TenantRepo.ListActiveTenantIDs(ctx)
	if err != nil {
		c.Logger.Warn("knowledge.recover_stuck.list_tenants_failed", zap.Error(err))
		return
	}
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

// buildEmbedResolver resolves the tenant's default embedding model via the
// registry (marked default → first available → empty).
func buildEmbedResolver(
	registry *llmgateway.ModelRegistry,
	logger *zap.Logger,
) pipeline.EmbedServiceResolver {
	return func(ctx context.Context, tenantID string) pipeline.EmbedClient {
		model, err := registry.ResolveDefaultEmbeddingModel(ctx)
		if err != nil || model == "" {
			return nil
		}
		cfg, _, err := registry.ResolveEmbedding(ctx, model)
		if err != nil {
			return nil
		}
		client := llmgateway.NewOpenAICompatClient(cfg, logger)
		return embedding.NewEmbeddingServiceWithModel(client, model, logger)
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
			var err error
			m, err = registry.ResolveDefaultEmbeddingModel(ctx)
			if err != nil || m == "" {
				return nil
			}
		} else if !registryHasEmbeddingModel(ctx, registry, m) {
			// 显式指定的 workspace 模型不在 managed 目录：不静默替换成默认
			// embedding（那会改变 workspace 语义），按未配置处理（fail-closed）。
			return nil
		}

		cfg, _, err := registry.ResolveEmbedding(ctx, m)
		if err != nil {
			return nil
		}
		client := llmgateway.NewOpenAICompatClient(cfg, logger)
		return embedding.NewEmbeddingServiceWithModel(client, m, logger)
	}
}

// knowledgeModelExistsAdapter 把全局 ModelRegistry 目录查询适配为 knowledge
// 的 ModelExists port（wiring 唯一适配点：knowledge 不 import llmgateway）。
// 目录/DB 故障传播错误（fail-closed），不默认放行。
type knowledgeModelExistsAdapter struct{ registry *llmgateway.ModelRegistry }

func (a knowledgeModelExistsAdapter) Exists(ctx context.Context, model string, capability knowledgeport.ModelCapability) (bool, error) {
	var names []string
	var err error
	switch capability {
	case knowledgeport.CapRerank:
		names, err = a.registry.ListRerankModelsByTenant(ctx)
	default: // CapEmbedding
		names, err = a.registry.ListEmbeddingModelsByTenant(ctx)
	}
	if err != nil {
		return false, err
	}
	for _, n := range names {
		if n == model {
			return true, nil
		}
	}
	return false, nil
}

// registryHasEmbeddingModel 检查显式模型是否在 enabled 且 provider 可用的
// embedding 目录中。目录读取失败按不存在处理（fail-closed）。
func registryHasEmbeddingModel(ctx context.Context, registry *llmgateway.ModelRegistry, model string) bool {
	names, err := registry.ListEmbeddingModelsByTenant(ctx)
	if err != nil {
		return false
	}
	for _, n := range names {
		if n == model {
			return true
		}
	}
	return false
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
	if c.IAM == nil || c.IAM.TenantRepo == nil {
		c.Logger.Debug("knowledge.seed_builtin_docs.skipped",
			zap.String("reason", "tenant repo not wired"))
		return
	}
	if c.LLMGateway == nil || c.LLMGateway.Registry == nil {
		c.Logger.Debug("knowledge.seed_builtin_docs.skipped",
			zap.String("reason", "llm gateway registry not wired"))
		return
	}

	tenantIDs, err := c.IAM.TenantRepo.ListActiveTenantIDs(ctx)
	if err != nil {
		c.Logger.Warn("knowledge.seed_builtin_docs.list_tenants_failed", zap.Error(err))
		return
	}

	for _, tid := range tenantIDs {
		c.seedBuiltinDocsForTenant(ctx, tid)
	}
}

// seedBuiltinDocsForTenant 为单个 tenant 种子内置文档；无可用嵌入模型时
// WARN 并跳过，不阻断启动。
func (c *Container) seedBuiltinDocsForTenant(ctx context.Context, tid string) {
	model, err := c.LLMGateway.Registry.ResolveDefaultEmbeddingModel(ctx)
	if err != nil || model == "" {
		c.Logger.Warn("knowledge.seed_builtin_docs.skip: no embedding model",
			zap.String("tenant_id", tid))
		return
	}
	seeds.SeedBuiltinDocs(ctx, tid, model,
		c.Knowledge.Ingest, c.Knowledge.DocRepo, officialDocsCatalogAdapter{}, c.Logger)
}

// officialDocsCatalogAdapter adapts the agent context's embedded official
// documentation catalog to knowledgeport.OfficialDocsCatalog. Lives in
// wiring so knowledge infrastructure never imports a sibling context.
type officialDocsCatalogAdapter struct{}

func (officialDocsCatalogAdapter) AllCatalogEntries() ([]knowledgeport.OfficialDocEntry, error) {
	entries, err := officialdocs.AllCatalogEntries()
	if err != nil {
		return nil, err
	}
	out := make([]knowledgeport.OfficialDocEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, knowledgeport.OfficialDocEntry{
			DocumentID: e.DocumentID, Title: e.Title, ProductVersion: e.ProductVersion,
			Section: e.Section, URL: e.URL, Ordinal: e.Ordinal, Body: e.Body,
		})
	}
	return out, nil
}
