package wiring

import (
	"context"
	"encoding/json"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding"
	memory "github.com/byteBuilderX/stratum/internal/memory/application"
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
	Injector *pipeline.MemoryInjector
	Pipeline *pipeline.Pipeline
}

func (c *Container) buildMemory(ctx context.Context) error {
	mem := &Memory{
		Manager: memory.NewMemoryManager(memory.DefaultMemoryConfig(), c.Logger, nil, nil, nil, c.dbOrNil()),
	}

	db := c.dbOrNil()
	if db != nil && c.Storage != nil && c.Storage.Milvus != nil {
		inj := pipeline.NewMemoryInjector(db, c.Logger, nil, c.Storage.Milvus)
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			inj.SetEmbedResolver(c.Knowledge.EmbedResolver)
		}
		mem.Injector = inj
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
		if c.LLMGateway.Gateway.HasEmbeddingClient() {
			embedSvc = embedding.NewEmbeddingService(c.LLMGateway.Gateway, c.Logger)
		}

		dimResolver := pipeline.DimResolver(func(ctx context.Context, tenantID string) int {
			var settingsJSON []byte
			if err := db.QueryRow(ctx,
				"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
				tenantID,
			).Scan(&settingsJSON); err != nil {
				return 1536
			}
			var s map[string]interface{}
			if err := json.Unmarshal(settingsJSON, &s); err != nil {
				return 1536
			}
			if d, ok := s["embedding_dim"].(float64); ok && d > 0 {
				return int(d)
			}
			return 1536
		})

		vectorAdapter := pipeline.NewMilvusVectorAdapter(c.Storage.Milvus).WithDimResolver(dimResolver)
		p := pipeline.New(pipelineCfg, db, nc, embedSvc, vectorAdapter, c.LLMGateway.Gateway, c.Logger)
		if c.Knowledge != nil && c.Knowledge.EmbedResolver != nil {
			p.SetEmbedResolver(c.Knowledge.EmbedResolver)
		}
		// Pipeline lifecycle (Start/Stop) is owned by the cmd/server Harness
		// memory-pipeline component, so wiring only constructs and exposes it.
		mem.Pipeline = p
	}

	c.Memory = mem
	return nil
}
