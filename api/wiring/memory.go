package wiring

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
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
	Manager   *memory.MemoryManager
	Service   *memory.MemoryService
	Injector  port.MemoryInjector
	Pipeline  *pipeline.Pipeline
	DLQReplay *pipeline.ReplayService
	RecallFn  port.RecallMemoryFn
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

func (a memoryLLMAdapter) Complete(ctx context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	if a.client == nil {
		return nil, fmt.Errorf("memory llm adapter: client is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("memory llm adapter: request is nil")
	}
	if a.tenantID != "" {
		ctx = reqctx.WithTenantID(ctx, a.tenantID)
	}
	messages := make([]llmdomain.Message, len(req.Messages))
	for i, message := range req.Messages {
		messages[i] = llmdomain.Message{Role: message.Role, Content: message.Content}
	}
	response, err := a.client.Complete(ctx, &llmdomain.CompletionRequest{
		Model: req.Model, Messages: messages, Temperature: float32(req.Temperature), MaxTokens: req.MaxTokens,
		ResponseFormat: toLLMResponseFormat(req.ResponseFormat),
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("memory llm adapter: provider returned nil response")
	}
	return &memport.CompletionResponse{Content: response.Content, CompletionTokens: response.Usage.CompletionTokens}, nil
}

// toLLMResponseFormat 把 memport 的本地 ResponseFormat 适配到 llmgateway domain
// （DDD：memport 不能 import llmgateway domain，此转换只在组合根做一次）。
func toLLMResponseFormat(rf *memport.ResponseFormat) *llmdomain.ResponseFormat {
	if rf == nil {
		return nil
	}
	return &llmdomain.ResponseFormat{Type: rf.Type}
}

// platformParameterReader adapts the parameters application service to the
// pipeline's PlatformParams port (thin ACL; wiring is the only allowed
// adapter seam). nil service (db unavailable) reports absent so consumers
// keep their definition defaults.
type platformParameterReader struct {
	svc *parametersapp.Service
}

func (r platformParameterReader) Int(ctx context.Context, key string) (int, bool) {
	if r.svc == nil {
		return 0, false
	}
	values, err := r.svc.PlatformValues(ctx)
	if err != nil {
		return 0, false
	}
	switch v := values[key].(type) {
	case float64:
		return int(v), true
	case int64:
		return int(v), true
	}
	return 0, false
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

	c.Memory = mem
	return c.buildMemoryPipeline(mem, db)
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
		var extractParams pipeline.PlatformParams
		if c.Parameters != nil {
			extractParams = platformParameterReader{svc: c.Parameters.Service}
		}
		baseline := c.mechanismBaselineForTenant(c.Config.MemoryPipeline.EnrichModel)
		mem.Service.SetLLMExtractResolver(makeLLMExtractResolver(llmRes, extractParams, baseline, c.Logger))
		mem.Service.SetLLMSupersederResolver(makeLLMSupersederResolver(llmRes, baseline, c.Logger))
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
	if c.Parameters != nil {
		inj.SetPlatformParams(platformParameterReader{svc: c.Parameters.Service})
	}
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
		Enabled:               mp.Enabled,
		PollInterval:          mp.PollInterval,
		BatchSize:             mp.BatchSize,
		EmbedWorkers:          mp.EmbedWorkers,
		EnrichWorkers:         mp.EnrichWorkers,
		EmbedAckWait:          mp.EmbedAckWait,
		EnrichAckWait:         mp.EnrichAckWait,
		MaxDeliver:            mp.MaxDeliver,
		EnrichModel:           mp.EnrichModel,
		SummaryModel:          mp.SummaryModel,
		SummaryTokenThreshold: mp.SummaryTokenThreshold,
		EnrichmentPrompt:      mp.EnrichmentPrompt,
		SummaryPrompt:         mp.SummaryPrompt,
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
	// 机制基线（model_profiles 档案 → 种子兜底）注入 enricher：按现状
	// EnrichModel 解析模型族，命中档案则覆盖富化/总结模板与模型。
	p.SetMechanismBaseline(c.mechanismBaselineForTenant(pipelineCfg.EnrichModel))
	mem.Pipeline = p
	return nil
}

// resolveEmbeddingDim 按租户默认 embedding 模型查维度表；模型未配置或解析失败
// 时回退 1536（既有默认），保证 create-collection 维度始终可计算。
// 独立成方法以保持 buildMemoryPipeline 复杂度在基线内。
func (c *Container) resolveEmbeddingDim(ctx context.Context, tenantID string) int {
	if c.LLMGateway != nil && c.LLMGateway.Registry != nil {
		if model, err := c.LLMGateway.Registry.ResolveDefaultEmbeddingModel(ctx, tenantID); err == nil && model != "" {
			return constants.DimensionForModel(model)
		}
	}
	return 1536
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

func makeLLMExtractResolver(llmRes *tenantCapabilityResolver, params pipeline.PlatformParams, baseline memport.MechanismBaselineResolver, logger *zap.Logger) func(context.Context, string) memport.LLMExtractor {
	return func(ctx context.Context, tenantID string) memport.LLMExtractor {
		llm := llmRes.ResolveLLM(ctx, tenantID)
		if llm == nil {
			return nil
		}
		extractor := pipeline.NewLLMExtractor(memoryLLMAdapter{client: llm, tenantID: tenantID}).WithLogger(logger)
		extractor.SetPlatformParams(params)
		if baseline != nil {
			if b, err := baseline(ctx, tenantID); err == nil && b.MemoryExtraction != "" {
				extractor.SetSystemPrompt(b.MemoryExtraction)
			}
		}
		return extractor
	}
}

func makeLLMSupersederResolver(llmRes *tenantCapabilityResolver, baseline memport.MechanismBaselineResolver, logger *zap.Logger) func(context.Context, string) memport.LLMSuperseder {
	return func(ctx context.Context, tenantID string) memport.LLMSuperseder {
		llm := llmRes.ResolveLLM(ctx, tenantID)
		if llm == nil {
			return nil
		}
		superseder := memworkers.NewLLMSuperseder(memoryLLMAdapter{client: llm, tenantID: tenantID}).WithLogger(logger)
		if baseline != nil {
			if b, err := baseline(ctx, tenantID); err == nil && b.MemorySupersede != "" {
				superseder.WithJudgePrompt(b.MemorySupersede)
			}
		}
		return superseder
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

// BuildMemoryWorkers constructs memory background workers.
// TenantWatcher replaces the static per-tenant startup loop — new tenants are
// automatically picked up on the next 60s reconcile tick.
// BufferScanner is global (Redis key names encode tenantID).
func BuildMemoryWorkers(c *Container) []interface {
	Start(context.Context)
	Stop()
} {
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

	baseline := c.mechanismBaselineForTenant(c.Config.MemoryPipeline.EnrichModel)
	watcher := memworkers.NewTenantWatcher(db, func(tid string) memworkers.WorkerSet {
		ws := memworkers.WorkerSet{
			memworkers.NewExtractionWorker(tid, queue, c.Memory.Service, c.Logger),
			memworkers.NewGCWorker(tid, factRepo, c.Logger).WithQueue(queue),
		}
		return appendTenantLLMWorkers(ws, tid, factRepo, historyRepo,
			buildWorkerLLMResolver(llmRes), baseline, c.Logger)
	}, c.Logger)

	result := []interface {
		Start(context.Context)
		Stop()
	}{watcher}

	if c.Storage != nil && c.Storage.Redis != nil {
		store := persistence.NewRedisMessageBufferStore(c.Storage.Redis.Client())
		scanner := memory.NewBufferScanner(store, queue, c.Logger)
		scanner.SetMetrics(c.Platform.Metrics)
		result = append(result, scanner)
	}

	return result
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
	baseline memport.MechanismBaselineResolver,
	logger *zap.Logger,
) memworkers.WorkerSet {
	var summarizer memworkers.HistorySummarizer
	var compressor memworkers.HistoryCompressor
	if resolver != nil {
		// 启动路径无 ctx：Background + helper 内部短超时兜底 DB 悬挂。
		// 基线解析失败保持内置模板（现状行为），由机制基线解析处 Warn。
		var b memport.MechanismBaseline
		if baseline != nil {
			if bl, err := baseline(context.Background(), tenantID); err == nil {
				b = bl
			}
		}
		superseder := memworkers.NewResolvingLLMSuperseder(tenantID, resolver).WithLogger(logger)
		if b.MemorySupersede != "" {
			superseder.WithJudgePrompt(b.MemorySupersede)
		}
		workerSet = append(workerSet, memworkers.NewSupersedeWorker(tenantID, factRepo, superseder, logger))

		historyProcessor := memworkers.NewResolvingLLMHistorySummarizer(tenantID, resolver)
		if b.MemorySummarize != "" {
			historyProcessor.WithSummarizePrompt(b.MemorySummarize)
		}
		summarizer = historyProcessor
		compressor = historyProcessor
	}
	return append(workerSet, memworkers.NewHistoryWorker(tenantID, historyRepo, summarizer, compressor, logger))
}
