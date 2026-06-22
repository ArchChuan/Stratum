package wiring

import (
	"context"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
)

// Memory groups memory-system services: the user-facing manager, the
// per-tenant memory injector consumed by agents, and the async write
// pipeline (JetStream-backed) that embeds and persists memories.
//
// Pipeline is nil when MEMORY_PIPELINE_ENABLED is false or NATS is not
// reachable; downstream consumers must nil-check before use.
type Memory struct {
	Manager  *memory.MemoryManager
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
	if db != nil && c.Storage != nil && c.Storage.Milvus != nil {
		inj := pipeline.NewMemoryInjector(db, c.Logger, nil, c.Storage.Milvus)
		var embedResolver pipeline.EmbedServiceResolver
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			inj.SetEmbedResolver(c.Knowledge.EmbedResolver)
			embedResolver = c.Knowledge.EmbedResolver
		}
		mem.Injector = injectorAdapter{inj: inj}

		recallHandler := pipeline.NewRecallHandler(db, c.Logger, nil, embedResolver, c.Storage.Milvus)
		mem.RecallFn = func(ctx context.Context, tenantID, userID, agentID string, input map[string]any) (string, error) {
			return recallHandler.Handle(ctx, tenantID, userID, agentID, input)
		}
	}

	// Memory pipeline — degrades to nil if disabled or NATS unavailable.
	pipelineCfg := pipeline.DefaultConfig()
	pipelineCfg.Enabled = c.Config.MemoryPipelineEnabled
	pipelineCfg.NatsURL = c.Config.NatsURL

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

		var embedSvc pipeline.EmbedClient

		dimResolver := pipeline.DimResolver(func(ctx context.Context, tenantID string) int {
			if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
				if ec := c.Knowledge.EmbedResolver(ctx, tenantID); ec != nil {
					if d := ec.GetVectorDimension(); d > 0 {
						return d
					}
				}
			}
			if embedSvc != nil {
				if d := embedSvc.GetVectorDimension(); d > 0 {
					return d
				}
			}
			return 1536
		})

		vectorAdapter := pipeline.NewMilvusVectorAdapter(c.Storage.Milvus).WithDimResolver(dimResolver)
		p := pipeline.New(pipelineCfg, db, nc, embedSvc, vectorAdapter, c.Logger)
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			p.SetEmbedResolver(c.Knowledge.EmbedResolver)
		}
		if c.Platform != nil && c.Platform.GatewayCache != nil {
			llmRes := newTenantCapabilityResolver(db, c.Platform.AESKey, c.Platform.GatewayCache, nil, c.Logger).(*tenantCapabilityResolver)
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
	})
}
