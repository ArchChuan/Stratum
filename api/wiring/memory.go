package wiring

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	memworkers "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	parametersapp "github.com/byteBuilderX/stratum/internal/parameters/application"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// Memory groups memory-system services: the user-facing manager, the
// per-tenant memory injector consumed by agents, and the async write
// pipeline (JetStream-backed) that embeds and persists memories.
//
// Pipeline is nil when MEMORY_PIPELINE_ENABLED is false or NATS is not
// reachable; downstream consumers must nil-check before use. DLQReplay
// follows the same availability (depends on the shared NATS connection),
// and needs no shutdown code (no goroutines).
type Memory struct {
	Manager          *memory.MemoryManager
	Service          *memory.MemoryService
	MigrationService *memory.MemoryMigrationService
	Injector         port.MemoryInjector
	Pipeline         *pipeline.Pipeline
	DLQReplay        *pipeline.ReplayService
	RecallFn         port.RecallMemoryFn
}

type memoryGatewayCompleter interface {
	Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error)
}

// memoryLLMAdapter 把 memory pipeline 的 LLM 请求适配到 llmgateway。
// tenantID 由构造方闭包捕获（pipeline worker 的 ctx 不含请求租户），
// Complete 时注入 reqctx——Gateway 内部从 ctx 取租户做模型解析（gateway.go
// 的 TenantIDFromContext），不注入则 resolve 报 tenant_id is empty。
type memoryLLMAdapter struct {
	client   memoryGatewayCompleter
	tenantID string
}

func (a memoryLLMAdapter) Complete(ctx context.Context, req *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	if a.client == nil {
		return nil, fmt.Errorf("memory llm adapter: client is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("memory llm adapter: request is nil")
	}
	if a.tenantID != "" {
		ctx = reqctx.WithTenantID(ctx, a.tenantID)
	}
	resp, err := a.client.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("memory llm adapter: provider returned nil response")
	}
	return resp, nil
}

// agentResourceParamResolver adapts the parameters resolver + agent repo to
// the memory pipeline's per-agent resource-param port (thin ACL; wiring is
// the only allowed adapter seam). agentRepo is captured as a closure because
// buildMemory runs before buildAgent assigns c.Agent.AgentRepo; the closure
// reads it lazily at resolve time (runtime), so the build-time nil is never
// dereferenced. A nil service or repo reports absent so consumers keep their
// definition defaults (degrade convention).
type agentResourceParamResolver struct {
	agentRepo func() port.AgentRepo
	svc       *parametersapp.Service
}

func (r agentResourceParamResolver) Resolve(ctx context.Context, tenantID, agentID, key string) (any, bool, error) {
	if r.svc == nil || r.agentRepo == nil {
		return nil, false, nil
	}
	repo := r.agentRepo()
	if repo == nil {
		return nil, false, nil
	}
	cfg, ok, err := repo.Get(reqctx.WithTenantID(ctx, tenantID), agentID)
	if err != nil || !ok {
		return nil, false, err
	}
	return r.svc.Resolver().Resolve(ctx, key, cfg.MemoryParameters)
}

// memoryResourceParamResolver builds the per-agent resource resolver, or nil
// when the parameters registry is unavailable (degrade).
func (c *Container) memoryResourceParamResolver() memport.ResourceParamResolver {
	if c.Parameters == nil {
		return nil
	}
	return agentResourceParamResolver{
		agentRepo: func() port.AgentRepo {
			if c.Agent == nil {
				return nil
			}
			return c.Agent.AgentRepo
		},
		svc: c.Parameters.Service,
	}
}

// platformParamResolver adapts the parameters resolver to the memory domain's
// platform-scope port for cross-agent background workers (thin ACL; wiring is
// the only allowed adapter seam). declared=nil yields the pure platform value
// or the definition default — exactly the ScopePlatform consumption contract.
type platformParamResolver struct {
	svc *parametersapp.Service
}

func (r platformParamResolver) ResolvePlatform(ctx context.Context, key string) (any, bool, error) {
	if r.svc == nil {
		return nil, false, nil
	}
	return r.svc.Resolver().Resolve(ctx, key, nil)
}

// memoryPlatformParamResolver builds the cross-agent platform resolver, or nil
// when the parameters registry is unavailable (degrade). A nil resolver keeps
// workers on their const defaults, matching pre-config behaviour.
func (c *Container) memoryPlatformParamResolver() memport.PlatformParamResolver {
	if c.Parameters == nil {
		return nil
	}
	return platformParamResolver{svc: c.Parameters.Service}
}

func (c *Container) buildMemory(ctx context.Context) error {
	memRepo := persistence.NewMemoryRepo(c.dbOrNil())
	mem := &Memory{
		Manager: memory.NewMemoryManager(c.Logger, memRepo),
	}

	db := c.dbOrNil()
	c.buildMemoryService(mem, db, memRepo)
	c.buildMemoryInjector(mem, db)
	c.buildMemoryRecall(mem, db)
	c.buildMemoryMigration(mem, db)

	c.Memory = mem
	return c.buildMemoryPipeline(mem, db)
}

// memoryModelValidator 校验目标记忆嵌入模型在目录中精确可解析（fail-closed）。
// 复用 LLMGateway Registry.ResolveEmbeddingExact（无默认兜底）；Registry 未装配
// 时拒绝启动迁移——宁可 400，也不把生效模型切到未校验模型。
func (c *Container) memoryModelValidator(ctx context.Context, tenantID, model string) error {
	if c.LLMGateway == nil || c.LLMGateway.Registry == nil {
		return fmt.Errorf("memory migration: model registry not wired")
	}
	_, _, err := c.LLMGateway.Registry.ResolveEmbeddingExact(ctx, model)
	return err
}

// buildMemoryMigration 装配 P5 记忆嵌入模型平滑迁移服务（确认制切换 + 后台回填）。
// 依赖 DB + Milvus；任一缺失则迁移服务保持 nil（fail-closed：无迁移能力，
// 管理界面不展示迁移操作）。embed resolver 复用 knowledge 的 per-tenant+per-model
// 解析（buildKnowledgeEmbedResolver，fail-closed——目标模型不可解析时回填标 failed）。
func (c *Container) buildMemoryMigration(mem *Memory, db *pgxpool.Pool) {
	if db == nil || c.Storage == nil || c.Storage.Milvus == nil {
		return
	}
	svc := memory.NewMemoryMigrationService(
		persistence.NewMigrationRepo(db),
		persistence.NewFactRepo(db),
		persistence.NewMilvusPortAdapter(c.Storage.Milvus),
		c.Logger,
	)
	if c.Knowledge != nil && c.Knowledge.KnowledgeResolver != nil {
		svc.SetEmbedResolver(makeMigrationEmbedResolver(c.Knowledge.KnowledgeResolver))
	}
	// P6 监控：迁移进度 gauge / 停滞计数器上报到导出 /metrics 的同一 registry
	// （Platform.Metrics 与 LLMGateway.Metrics 同实例）。未装配时 NoopMetrics 安全跳过。
	if c.LLMGateway != nil && c.LLMGateway.Metrics != nil {
		svc.SetMetrics(c.LLMGateway.Metrics)
	}
	// IAM 在 memory 之后构建：闭包延迟读取 c.IAM，运行时才解引用
	// （与 agentResourceParamResolver 的 lazy-closure 模式一致）。
	svc.SetTenantLister(func(ctx context.Context) ([]string, error) {
		if c.IAM == nil || c.IAM.TenantRepo == nil {
			return nil, fmt.Errorf("memory migration: tenant repo not wired")
		}
		return c.IAM.TenantRepo.ListActiveTenantIDs(ctx)
	})
	svc.SetEffectiveModelSetter(func(ctx context.Context, tenantID, model string) error {
		if c.IAM == nil || c.IAM.TenantService == nil {
			return fmt.Errorf("memory migration: tenant service not wired")
		}
		return c.IAM.TenantService.SetSetting(ctx, tenantID, "memory_embedding_model", model)
	})
	// 目标模型可解析性校验（fail-closed）：StartMigration 登记前确认 toModel 是
	// 目录中 enabled 的嵌入模型（ResolveEmbeddingExact 精确解析，无默认兜底），
	// 拒绝把生效模型切到不存在的模型。与 SetEffectiveModelSetter 成对注入。
	svc.SetModelValidator(c.memoryModelValidator)
	mem.MigrationService = svc
}

// makeMigrationEmbedResolver 把 knowledge 的 per-tenant+per-model 嵌入解析器适配为
// 迁移服务的 memport.EmbedClient 解析器。knowledge.EmbedClient 是 pipeline.EmbedClient
// 的接口超集（多 EmbedBatch），显式接口转换后经 NewEmbedClientAdapter 适配；nil 传播
// （fail-closed，backfill 因 embed 不可用标 failed，与 buildEmbedResolver 一致）。
func makeMigrationEmbedResolver(embedRes knowledge.EmbedResolver) memory.EmbedClientResolverByModel {
	return func(ctx context.Context, tenantID, model string) memport.EmbedClient {
		ec := embedRes(ctx, tenantID, model)
		if ec == nil {
			return nil
		}
		return pipeline.NewEmbedClientAdapter(pipeline.EmbedClient(ec))
	}
}

func (c *Container) buildMemoryService(mem *Memory, db *pgxpool.Pool, memRepo memport.MemoryRepo) {
	if db == nil {
		return
	}
	factRepo := persistence.NewFactRepo(db)
	entityRepo := persistence.NewEntityRepo(db)
	queue := persistence.NewExtractionQueue(db)

	var messageBufferStore memport.MessageBufferStore
	if c.Storage != nil && c.Storage.Redis != nil {
		messageBufferStore = persistence.NewRedisMessageBufferStore(c.Storage.Redis.Client())
	}

	mem.Service = memory.NewMemoryService(factRepo, entityRepo, queue, nil, nil, nil, messageBufferStore, c.Logger)
	mem.Service.SetMemoryRepo(memRepo)

	if c.LLMGateway != nil && c.LLMGateway.Registry != nil {
		llmRes := newTenantCapabilityResolver(
			c.LLMGateway.Registry, c.LLMGateway.Gateway, c.Logger,
		).(*tenantCapabilityResolver)
		mem.Service.SetLLMExtractResolver(makeLLMExtractResolver(llmRes, c.memoryResourceParamResolver(), c.Logger))
		mem.Service.SetLLMSupersederResolver(makeLLMSupersederResolver(llmRes, c.memoryPlatformParamResolver(), c.Logger))
	}
	if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
		mem.Service.SetEmbedClientResolver(makeEmbedClientResolver(c.Knowledge.EmbedResolver))
	}
}

func (c *Container) buildMemoryInjector(mem *Memory, db *pgxpool.Pool) {
	if db == nil {
		return
	}
	vectorStore := c.Storage.Milvus
	inj := pipeline.NewMemoryInjector(db, c.Logger, nil, vectorStore)
	if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
		inj.SetEmbedResolver(c.Knowledge.EmbedResolver)
	}
	inj.SetResourceResolver(c.memoryResourceParamResolver())
	mem.Injector = injectorAdapter{inj: inj}
}

func (c *Container) buildMemoryRecall(mem *Memory, db *pgxpool.Pool) {
	if db == nil || c.Storage == nil || c.Storage.Milvus == nil {
		return
	}
	var embedResolver pipeline.EmbedServiceResolver
	if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
		embedResolver = c.Knowledge.EmbedResolver
	}
	recallHandler := pipeline.NewRecallHandler(db, c.Logger, nil, embedResolver, c.Storage.Milvus)
	if c.LLMGateway != nil && c.LLMGateway.Metrics != nil {
		recallHandler.WithMetrics(c.LLMGateway.Metrics)
	}
	recallHandler.SetResourceResolver(c.memoryResourceParamResolver())
	mem.RecallFn = func(ctx context.Context, tenantID, userID, agentID, scope string, input map[string]any) (string, error) {
		return recallHandler.Handle(ctx, tenantID, userID, agentID, scope, input)
	}

	if mem.Service != nil {
		mem.Service.SetVectorStore(persistence.NewMilvusPortAdapter(c.Storage.Milvus))
	}
}

func (c *Container) buildMemoryPipeline(mem *Memory, db *pgxpool.Pool) error {
	mp := c.Config.MemoryPipeline
	pipelineCfg := pipeline.Config{
		Enabled:       mp.Enabled,
		PollInterval:  mp.PollInterval,
		BatchSize:     mp.BatchSize,
		EmbedWorkers:  mp.EmbedWorkers,
		EnrichWorkers: mp.EnrichWorkers,
		EmbedAckWait:  mp.EmbedAckWait,
		EnrichAckWait: mp.EnrichAckWait,
		MaxDeliver:    mp.MaxDeliver,
	}

	if !pipelineCfg.Enabled || db == nil || c.Storage == nil || c.Storage.Milvus == nil {
		return nil
	}

	// 复用平台共享 NATS 连接（pkg/messaging/nats.Connect 创建，
	// MaxReconnects(-1)）；连接生命周期归 wiring，pipeline 只使用不关闭。
	if c.Storage.NATS == nil {
		c.Logger.Warn("memory-pipeline: NATS unavailable, pipeline disabled")
		return nil
	}

	replaySvc, err := pipeline.NewReplayService(c.Storage.NATS, c.Logger)
	if err != nil {
		return fmt.Errorf("memory dlq replay service: %w", err)
	}
	mem.DLQReplay = replaySvc

	dimResolver := pipeline.DimResolver(func(ctx context.Context, tenantID string) int {
		return c.resolveEmbeddingDim(ctx, tenantID)
	})

	vectorAdapter := pipeline.NewMilvusVectorAdapter(c.Storage.Milvus).WithDimResolver(dimResolver)
	p := pipeline.New(pipelineCfg, db, c.Storage.NATS, vectorAdapter, c.Logger)
	c.attachPipelineDynamic(p)
	if c.LLMGateway != nil && c.LLMGateway.Metrics != nil {
		pipeline.RegisterMetrics(c.LLMGateway.Metrics.Registerer())
	}
	if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
		p.SetEmbedResolver(c.Knowledge.EmbedResolver)
	}
	if c.LLMGateway != nil && c.LLMGateway.Registry != nil {
		llmRes := newTenantCapabilityResolver(
			c.LLMGateway.Registry, c.LLMGateway.Gateway, c.Logger,
		).(*tenantCapabilityResolver)
		p.SetLLMResolver(func(ctx context.Context, tenantID string) pipeline.LLMClient {
			gw := llmRes.ResolveLLM(ctx, tenantID)
			if gw == nil {
				return nil
			}
			return memoryLLMAdapter{client: gw, tenantID: tenantID}
		})
	}
	p.SetPlatformParamResolver(c.memoryPlatformParamResolver())
	mem.Pipeline = p
	return nil
}

// resolveEmbeddingDim 按租户显式配置的记忆嵌入模型查维度表；未配置或解析失败
// 返回 0 → MilvusVectorAdapter 跳过建 collection（fail-closed），不再回退
// 1536 全局兜底。独立成方法以保持 buildMemoryPipeline 复杂度在基线内。
func (c *Container) resolveEmbeddingDim(ctx context.Context, tenantID string) int {
	if c.LLMGateway == nil || c.LLMGateway.TenantEmbeddingResolver == nil {
		return 0
	}
	model, err := c.LLMGateway.TenantEmbeddingResolver.ResolveMemoryEmbeddingModel(ctx, tenantID)
	if err != nil || model == "" {
		return 0
	}
	return constants.DimensionForModel(model)
}

// attachPipelineDynamic 桥接热更新管道：config 层动态配置 → atomic 指针 →
// poller 每轮 re-read。dynamic 逃逸到堆，生命周期与 Container 一致；
// 若从未 Store 过，poller 回退静态值——与现状一致。
// 独立成函数以保持 buildMemoryPipeline 复杂度在基线内。
func (c *Container) attachPipelineDynamic(p *pipeline.Pipeline) {
	var dynamic atomic.Pointer[pipeline.DynamicConfig]
	if d := c.Config.LoadMemoryPipelineDynamic(); d.PollInterval > 0 || d.BatchSize > 0 {
		dynamic.Store(&pipeline.DynamicConfig{PollInterval: d.PollInterval, BatchSize: d.BatchSize})
	}
	c.Config.OnMemoryPipelineDynamic(func(d config.MemoryPipelineDynamic) {
		dynamic.Store(&pipeline.DynamicConfig{PollInterval: d.PollInterval, BatchSize: d.BatchSize})
	})
	p.WithDynamic(&dynamic)
}

func makeLLMExtractResolver(llmRes *tenantCapabilityResolver, resolver memport.ResourceParamResolver, logger *zap.Logger) func(context.Context, string) memport.LLMExtractor {
	return func(ctx context.Context, tenantID string) memport.LLMExtractor {
		llm := llmRes.ResolveLLM(ctx, tenantID)
		if llm == nil {
			return nil
		}
		extractor := pipeline.NewLLMExtractor(memoryLLMAdapter{client: llm, tenantID: tenantID}).WithLogger(logger)
		extractor.SetTenantID(tenantID)
		extractor.SetResourceResolver(resolver)
		return extractor
	}
}

func makeLLMSupersederResolver(llmRes *tenantCapabilityResolver, paramResolver memport.PlatformParamResolver, logger *zap.Logger) func(context.Context, string) memport.LLMSuperseder {
	return func(ctx context.Context, tenantID string) memport.LLMSuperseder {
		llm := llmRes.ResolveLLM(ctx, tenantID)
		if llm == nil {
			return nil
		}
		s := memworkers.NewLLMSuperseder(memoryLLMAdapter{client: llm, tenantID: tenantID}).WithLogger(logger)
		if paramResolver != nil {
			s = s.WithParamResolver(paramResolver)
		}
		return s
	}
}

func makeEmbedClientResolver(embedRes pipeline.EmbedServiceResolver) func(context.Context, string) memport.EmbedClient {
	return func(ctx context.Context, tenantID string) memport.EmbedClient {
		ec := embedRes(ctx, tenantID)
		if ec == nil {
			return nil
		}
		return pipeline.NewEmbedClientAdapter(ec)
	}
}

// injectorAdapter adapts *pipeline.MemoryInjector to port.MemoryInjector.
// Pipeline keeps its own InjectionContext VO; this thin shim copies fields
// so the application layer (port) stays free of pipeline imports.
type injectorAdapter struct{ inj *pipeline.MemoryInjector }

func (a injectorAdapter) BuildContext(ctx context.Context, ic port.InjectionContext) (string, error) {
	return a.inj.BuildContext(ctx, pipeline.InjectionContext{
		TenantID:       ic.TenantID,
		UserID:         ic.UserID,
		AgentID:        ic.AgentID,
		ConversationID: ic.ConversationID,
		Query:          ic.Query,
		Scope:          ic.Scope,
	})
}

// memoryWorker 是记忆后台 worker 的生命周期接口。
type memoryWorker interface {
	Start(context.Context)
	Stop()
}

// BuildMemoryWorkers constructs memory background workers.
// TenantWatcher replaces the static per-tenant startup loop — new tenants are
// automatically picked up on the next 60s reconcile tick.
// BufferScanner is global (Redis key names encode tenantID).
func BuildMemoryWorkers(c *Container) []memoryWorker {
	if c.Memory == nil || c.Memory.Service == nil {
		return nil
	}
	db := c.dbOrNil()
	if db == nil {
		return nil
	}

	factRepo := persistence.NewFactRepo(db)
	historyRepo := persistence.NewHistoryRepo(db)
	queue := persistence.NewExtractionQueue(db)

	if c.LLMGateway != nil && c.LLMGateway.Metrics != nil {
		memworkers.RegisterMetrics(c.LLMGateway.Metrics.Registerer())
	}

	var llmRes *tenantCapabilityResolver
	if c.LLMGateway != nil && c.LLMGateway.Registry != nil {
		llmRes = newTenantCapabilityResolver(
			c.LLMGateway.Registry, c.LLMGateway.Gateway, c.Logger,
		).(*tenantCapabilityResolver)
	}

	watcher := memworkers.NewTenantWatcher(db, func(tid string) memworkers.WorkerSet {
		ws := memworkers.WorkerSet{
			memworkers.NewExtractionWorker(tid, queue, c.Memory.Service, c.Logger),
			memworkers.NewGCWorker(tid, factRepo, c.Logger).WithQueue(queue),
		}
		return appendTenantLLMWorkers(ws, tid, factRepo, historyRepo,
			buildWorkerLLMResolver(llmRes), c.memoryPlatformParamResolver(), c.Logger)
	}, c.Logger)

	result := []memoryWorker{watcher}

	if c.Storage != nil && c.Storage.Redis != nil {
		store := persistence.NewRedisMessageBufferStore(c.Storage.Redis.Client())
		scanner := memory.NewBufferScanner(store, queue, c.Logger)
		scanner.SetMetrics(c.Platform.Metrics)
		result = append(result, scanner)
	}

	return appendMigrationWorker(result, c)
}

// appendMigrationWorker 追加记忆嵌入模型平滑迁移回填 worker：确认制切换后由该
// worker 轮询所有租户的 active 迁移并渐进 re-embed 存量事实到目标模型 collection。
func appendMigrationWorker(workers []memoryWorker, c *Container) []memoryWorker {
	if c.Memory == nil || c.Memory.MigrationService == nil {
		return workers
	}
	return append(workers, c.Memory.MigrationService)
}

func buildWorkerLLMResolver(llmRes *tenantCapabilityResolver) memworkers.TenantLLMResolver {
	if llmRes == nil {
		return nil
	}
	return func(ctx context.Context, tenantID string) (memworkers.TenantLLMClient, error) {
		llm, err := llmRes.ResolveWorkerLLM(ctx, tenantID)
		if err != nil || llm == nil {
			return nil, err
		}
		return memoryLLMAdapter{client: llm, tenantID: tenantID}, nil
	}
}

func appendTenantLLMWorkers(
	workerSet memworkers.WorkerSet,
	tenantID string,
	factRepo memport.FactRepo,
	historyRepo memport.HistoryRepo,
	resolver memworkers.TenantLLMResolver,
	paramResolver memport.PlatformParamResolver,
	logger *zap.Logger,
) memworkers.WorkerSet {
	var summarizer memworkers.HistorySummarizer
	var compressor memworkers.HistoryCompressor
	if resolver != nil {
		superseder := memworkers.NewResolvingLLMSuperseder(tenantID, resolver).WithLogger(logger)
		if paramResolver != nil {
			superseder = superseder.WithParamResolver(paramResolver)
		}
		workerSet = append(workerSet, memworkers.NewSupersedeWorker(
			tenantID,
			factRepo,
			superseder,
			logger,
		))
		historyProcessor := memworkers.NewResolvingLLMHistorySummarizer(tenantID, resolver)
		if paramResolver != nil {
			historyProcessor = historyProcessor.WithParamResolver(paramResolver)
		}
		summarizer = historyProcessor
		compressor = historyProcessor
	}
	return append(workerSet, memworkers.NewHistoryWorker(tenantID, historyRepo, summarizer, compressor, logger))
}
