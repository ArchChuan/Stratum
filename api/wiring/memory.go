package wiring

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
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

func (c *Container) buildMemory(ctx context.Context) error {
	memRepo := persistence.NewMemoryRepo(c.dbOrNil())
	mem := &Memory{
		Manager: memory.NewMemoryManager(c.Logger, memRepo),
	}

	db := c.dbOrNil()
	if db != nil {
		factRepo := persistence.NewFactRepo(db)
		entityRepo := persistence.NewEntityRepo(db)
		queue := persistence.NewExtractionQueue(db)

		var messageBufferStore memport.MessageBufferStore
		if c.Storage != nil && c.Storage.Redis != nil {
			messageBufferStore = persistence.NewRedisMessageBufferStore(c.Storage.Redis.Client())
		}

		mem.Service = memory.NewMemoryService(factRepo, entityRepo, queue, nil, nil, nil, messageBufferStore, c.Logger)
		mem.Service.SetMemoryRepo(memRepo)

		if c.Platform != nil && c.Platform.GatewayCache != nil {
			llmRes := newTenantCapabilityResolver(
				db, c.Platform.AESKey, c.Platform.GatewayCache, c.LLMGateway.Gateway, c.Logger,
				c.Config.QwenBaseURL, c.Config.ZhipuBaseURL,
			).(*tenantCapabilityResolver)
			mem.Service.SetLLMExtractResolver(func(ctx context.Context, tenantID string) memport.LLMExtractor {
				llm := llmRes.ResolveLLM(ctx, tenantID)
				if llm == nil {
					return nil
				}
				return pipeline.NewLLMExtractor(llm)
			})
		}
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			embedRes := c.Knowledge.EmbedResolver
			mem.Service.SetEmbedClientResolver(func(ctx context.Context, tenantID string) memport.EmbedClient {
				ec := embedRes(ctx, tenantID)
				if ec == nil {
					return nil
				}
				return pipeline.NewEmbedClientAdapter(ec)
			})
		}
	}
	if db != nil {
		vectorStore := c.Storage.Milvus
		inj := pipeline.NewMemoryInjector(db, c.Logger, nil, vectorStore)
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			inj.SetEmbedResolver(c.Knowledge.EmbedResolver)
		}
		mem.Injector = injectorAdapter{inj: inj}
	}

	if db != nil && c.Storage != nil && c.Storage.Milvus != nil {
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

	// Memory pipeline — degrades to nil if disabled or NATS unavailable.
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

	if pipelineCfg.Enabled && db != nil && c.Storage != nil && c.Storage.Milvus != nil {
		// Per cmd/server/main.go, the pipeline opens its own NATS connection
		// independent of Storage.NATS so it can be torn down cleanly.
		nc, err := nats.Connect(pipelineCfg.NatsURL)
		if err != nil {
			c.Logger.Warn("memory-pipeline: NATS connect failed", zap.Error(err))
			c.Memory = mem
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
		if c.Platform != nil && c.Platform.GatewayCache != nil {
			llmRes := newTenantCapabilityResolver(
				db, c.Platform.AESKey, c.Platform.GatewayCache, c.LLMGateway.Gateway, c.Logger,
				c.Config.QwenBaseURL, c.Config.ZhipuBaseURL,
			).(*tenantCapabilityResolver)
			p.SetLLMResolver(func(ctx context.Context, tenantID string) pipeline.LLMClient {
				gw := llmRes.ResolveLLM(ctx, tenantID)
				if gw == nil {
					return nil
				}
				return gw
			})
		}
		// Pipeline lifecycle (Start/Stop) is owned by the cmd/server Harness
		// memory-pipeline component, so wiring only constructs and exposes it.
		mem.Pipeline = p
	}

	c.Memory = mem
	return nil
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
	if c.Platform != nil && c.Platform.GatewayCache != nil {
		llmRes = newTenantCapabilityResolver(
			db, c.Platform.AESKey, c.Platform.GatewayCache, c.LLMGateway.Gateway, c.Logger,
			c.Config.QwenBaseURL, c.Config.ZhipuBaseURL,
		).(*tenantCapabilityResolver)
	}

	watcher := memworkers.NewTenantWatcher(db, func(tid string) memworkers.WorkerSet {
		ws := memworkers.WorkerSet{
			memworkers.NewExtractionWorker(tid, queue, c.Memory.Service, c.Logger),
			memworkers.NewGCWorker(tid, factRepo, c.Logger).WithQueue(queue),
		}
		var workerLLMResolver memworkers.TenantLLMResolver
		if llmRes != nil {
			workerLLMResolver = func(ctx context.Context, tenantID string) (memworkers.TenantLLMClient, error) {
				return llmRes.ResolveWorkerLLM(ctx, tenantID)
			}
		}
		return appendTenantLLMWorkers(ws, tid, factRepo, historyRepo, workerLLMResolver, c.Logger)
	}, c.Logger)

	result := []interface {
		Start(context.Context)
		Stop()
	}{watcher}

	if c.Storage != nil && c.Storage.Redis != nil {
		store := persistence.NewRedisMessageBufferStore(c.Storage.Redis.Client())
		result = append(result, memory.NewBufferScanner(store, queue, c.Logger))
	}

	return result
}

func appendTenantLLMWorkers(
	workerSet memworkers.WorkerSet,
	tenantID string,
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
		historyProcessor := memworkers.NewResolvingLLMHistorySummarizer(tenantID, resolver)
		summarizer = historyProcessor
		compressor = historyProcessor
	}
	return append(workerSet, memworkers.NewHistoryWorker(tenantID, historyRepo, summarizer, compressor, logger))
}
