package wiring

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	memworkers "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

// Memory groups memory-system services: the user-facing manager, the
// per-tenant memory injector consumed by agents, and the async write
// pipeline (JetStream-backed) that embeds and persists memories.
//
// Pipeline is nil when MEMORY_PIPELINE_ENABLED is false or NATS is not
// reachable; downstream consumers must nil-check before use.
type Memory struct {
	Manager  *memory.MemoryManager
	Service  *memory.MemoryService
	Injector port.MemoryInjector
	Pipeline *pipeline.Pipeline
	RecallFn port.RecallMemoryFn
}

type memoryGatewayCompleter interface {
	Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error)
}

type memoryLLMAdapter struct{ client memoryGatewayCompleter }

func (a memoryLLMAdapter) Complete(ctx context.Context, req *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	if a.client == nil {
		return nil, fmt.Errorf("memory llm adapter: client is nil")
	}
	if req == nil {
		return nil, fmt.Errorf("memory llm adapter: request is nil")
	}
	messages := make([]llmdomain.Message, len(req.Messages))
	for i, message := range req.Messages {
		messages[i] = llmdomain.Message{Role: message.Role, Content: message.Content}
	}
	response, err := a.client.Complete(ctx, &llmdomain.CompletionRequest{
		Model: req.Model, Messages: messages, Temperature: float32(req.Temperature), MaxTokens: req.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, fmt.Errorf("memory llm adapter: provider returned nil response")
	}
	return &memport.CompletionResponse{Content: response.Content, CompletionTokens: response.Usage.CompletionTokens}, nil
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
		mem.Service.SetLLMExtractResolver(makeLLMExtractResolver(llmRes))
		mem.Service.SetLLMSupersederResolver(makeLLMSupersederResolver(llmRes))
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
		NatsURL:               mp.NatsURL,
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

	nc, err := nats.Connect(pipelineCfg.NatsURL)
	if err != nil {
		c.Logger.Warn("memory-pipeline: NATS connect failed", zap.Error(err))
		return nil
	}
	c.shutdown = append(c.shutdown, func(_ context.Context) error { return nc.Drain() })

	dimResolver := pipeline.DimResolver(func(ctx context.Context, tenantID string) int {
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			if ec := c.Knowledge.EmbedResolver(ctx, tenantID); ec != nil {
				if d := ec.GetVectorDimension(); d > 0 {
					return d
				}
			}
		}
		return 1536
	})

	vectorAdapter := pipeline.NewMilvusVectorAdapter(c.Storage.Milvus).WithDimResolver(dimResolver)
	p := pipeline.New(pipelineCfg, db, nc, vectorAdapter, c.Logger)
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
			return memoryLLMAdapter{client: gw}
		})
	}
	mem.Pipeline = p
	return nil
}

func makeLLMExtractResolver(llmRes *tenantCapabilityResolver) func(context.Context, string) memport.LLMExtractor {
	return func(ctx context.Context, tenantID string) memport.LLMExtractor {
		llm := llmRes.ResolveLLM(ctx, tenantID)
		if llm == nil {
			return nil
		}
		return pipeline.NewLLMExtractor(memoryLLMAdapter{client: llm})
	}
}

func makeLLMSupersederResolver(llmRes *tenantCapabilityResolver) func(context.Context, string) memport.LLMSuperseder {
	return func(ctx context.Context, tenantID string) memport.LLMSuperseder {
		llm := llmRes.ResolveLLM(ctx, tenantID)
		if llm == nil {
			return nil
		}
		return memworkers.NewLLMSuperseder(memoryLLMAdapter{client: llm})
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
	entityRepo := persistence.NewEntityRepo(db)
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
		return appendTenantLLMWorkers(ws, tid, entityRepo, factRepo, historyRepo,
			buildWorkerLLMResolver(llmRes), c.Logger)
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
		return memoryLLMAdapter{client: llm}, nil
	}
}

func appendTenantLLMWorkers(
	workerSet memworkers.WorkerSet,
	tenantID string,
	entityRepo memport.EntityRepo,
	factRepo memport.FactRepo,
	historyRepo memport.HistoryRepo,
	resolver memworkers.TenantLLMResolver,
	logger *zap.Logger,
) memworkers.WorkerSet {
	var summarizer memworkers.HistorySummarizer
	var compressor memworkers.HistoryCompressor
	if resolver != nil {
		workerSet = append(workerSet, memworkers.NewSupersedeWorker(
			tenantID,
			factRepo,
			memworkers.NewResolvingLLMSuperseder(tenantID, resolver),
			logger,
		))
		workerSet = append(workerSet, memworkers.NewProfileWorker(
			tenantID,
			entityRepo,
			factRepo,
			memworkers.NewResolvingLLMEntityProfiler(tenantID, resolver),
			logger,
		))
		historyProcessor := memworkers.NewResolvingLLMHistorySummarizer(tenantID, resolver)
		summarizer = historyProcessor
		compressor = historyProcessor
	}
	return append(workerSet, memworkers.NewHistoryWorker(tenantID, historyRepo, summarizer, compressor, logger))
}
