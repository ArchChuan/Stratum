package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// EmbedClient produces a vector embedding for a piece of text.
// Defined consumer-side so the pipeline depends on behavior rather than the concrete
// embedding.EmbeddingService implementation.
type EmbedClient interface {
	EmbedVector(ctx context.Context, text string) ([]float32, error)
	GetVectorDimension() int
}

// LLMClient performs a single non-streaming completion against an LLM provider.
// Defined consumer-side; concrete *llmgateway.Gateway satisfies it structurally.
type LLMClient interface {
	Complete(ctx context.Context, req *llmgateway.CompletionRequest) (*llmgateway.CompletionResponse, error)
}

// LLMResolver returns a per-tenant LLM client at call time. Returns nil when
// the tenant has no provider configured. Mirrors EmbedServiceResolver so the
// pipeline can drive enrich/summary jobs against tenant-private gateways
// (which is where API keys live — the global gateway has none).
type LLMResolver func(ctx context.Context, tenantID string) LLMClient

// Pipeline orchestrates all memory pipeline workers: outbox poller,
// embedder workers, and enricher workers.
type Pipeline struct {
	cfg           Config
	pool          *pgxpool.Pool
	nc            *nats.Conn
	jsm           *JetStreamManager
	embedResolver EmbedServiceResolver
	vectorDB      VectorStore
	llmResolver   LLMResolver
	logger        *zap.Logger

	poller    *OutboxPoller
	embedders []*EmbedderWorker
	enrichers []*EnricherWorker

	cancel   context.CancelFunc
	wg       sync.WaitGroup
	stopOnce sync.Once
}

// New creates a Pipeline orchestrator. Call Start to begin processing.
func New(
	cfg Config,
	pool *pgxpool.Pool,
	nc *nats.Conn,
	vectorDB VectorStore,
	logger *zap.Logger,
) *Pipeline {
	return &Pipeline{
		cfg:      cfg,
		pool:     pool,
		nc:       nc,
		vectorDB: vectorDB,
		logger:   logger,
	}
}

// SetEmbedResolver sets a per-tenant embedding resolver used by EmbedderWorkers.
// Must be called before Start.
func (p *Pipeline) SetEmbedResolver(r EmbedServiceResolver) {
	p.embedResolver = r
}

// SetLLMResolver sets a per-tenant LLM resolver used by EnricherWorkers.
// Must be called before Start. Without it, enrich/summary fall back to the
// shared p.llm (which has no provider clients in production), so callers
// running multi-tenant pipelines should always set this.
func (p *Pipeline) SetLLMResolver(r LLMResolver) {
	p.llmResolver = r
}

// Start initializes JetStream infrastructure, creates consumers, and launches
// all worker goroutines. It returns immediately after setup; workers run in the
// background until Stop is called or the parent context is cancelled.
func (p *Pipeline) Start(ctx context.Context) error {
	if !p.cfg.Enabled {
		p.logger.Info("memory pipeline disabled")
		return nil
	}

	jsm, err := NewJetStreamManager(p.nc, p.logger)
	if err != nil {
		return fmt.Errorf("jetstream manager: %w", err)
	}
	p.jsm = jsm

	if err := jsm.EnsureStreams(ctx); err != nil {
		return fmt.Errorf("ensure streams: %w", err)
	}

	js := jsm.JS()

	// Outbox poller
	p.poller = NewOutboxPoller(p.pool, js, p.logger, p.cfg)

	// 关键：worker 生命周期 ctx 必须独立于 ctx 参数。
	// Harness.Run 把 startCtx 设成 30s 超时，Start 返回后会 cancel()，
	// 如果直接 context.WithCancel(ctx) 派生，会让所有 worker 在启动 30s 后被
	// ctx_done 拖死（症状：poller_stopped cause=ctx_done，时间和启动间隔一致）。
	// Pipeline 自己持有 cancel，由 Pipeline.Stop() 触发，Harness 反向 Stop 时调用。
	pipeCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		runWithRestart(pipeCtx, "outbox-poller", p.logger, p.poller.Start)
	}()

	// Embedder workers
	embedConsumer, err := jsm.CreateConsumer(ctx,
		constants.MemoryRawStream,
		constants.EmbedderConsumerName,
		constants.MemoryRawSubject+".>",
		p.cfg.EmbedAckWait,
		p.cfg.MaxDeliver)
	if err != nil {
		cancel()
		p.wg.Wait()
		return fmt.Errorf("create embed consumer: %w", err)
	}

	for i := 0; i < p.cfg.EmbedWorkers; i++ {
		worker := NewEmbedderWorker(
			embedConsumer, js, nil, p.vectorDB, p.logger, p.cfg.EmbedAckWait, p.cfg.MaxDeliver,
		)
		if p.embedResolver != nil {
			worker.WithEmbedResolver(p.embedResolver)
		}
		p.embedders = append(p.embedders, worker)
		label := fmt.Sprintf("embed-worker-%d", i)
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			runWithRestart(pipeCtx, label, p.logger, worker.Start)
		}()
	}

	// Enricher workers
	enrichConsumer, err := jsm.CreateConsumer(ctx,
		constants.MemoryEnrichedStream,
		constants.EnricherConsumerName,
		constants.MemoryEnrichedSubject+".>",
		p.cfg.EnrichAckWait,
		p.cfg.MaxDeliver)
	if err != nil {
		cancel()
		p.wg.Wait()
		return fmt.Errorf("create enrich consumer: %w", err)
	}

	for i := 0; i < p.cfg.EnrichWorkers; i++ {
		worker := NewEnricherWorker(enrichConsumer, js, p.pool, p.logger, p.cfg)
		if p.llmResolver != nil {
			worker.WithLLMResolver(p.llmResolver)
		}
		p.enrichers = append(p.enrichers, worker)
		label := fmt.Sprintf("enrich-worker-%d", i)
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			runWithRestart(pipeCtx, label, p.logger, worker.Start)
		}()
	}

	p.logger.Info("memory pipeline started",
		zap.Int("embed_workers", p.cfg.EmbedWorkers),
		zap.Int("enrich_workers", p.cfg.EnrichWorkers),
		zap.Bool("embed_resolver_set", p.embedResolver != nil),
		zap.Bool("llm_resolver_set", p.llmResolver != nil))

	return nil
}

// Stop gracefully shuts down all workers and waits for goroutines to exit.
// Safe to call multiple times — concurrent shutdown signals (Harness + OS signal)
// won't double-close internal channels.
func (p *Pipeline) Stop() {
	p.stopOnce.Do(func() {
		if p.cancel == nil {
			return
		}
		p.logger.Info("memory pipeline stopping")

		p.cancel()

		if p.poller != nil {
			p.poller.Stop()
		}
		for _, w := range p.embedders {
			w.Stop()
		}
		for _, w := range p.enrichers {
			w.Stop()
		}

		p.wg.Wait()
		p.logger.Info("memory pipeline stopped")
	})
}

// runWithRestart runs fn in a loop, recovering from panics and restarting with
// exponential backoff. Returns only when ctx is cancelled.
func runWithRestart(ctx context.Context, label string, logger *zap.Logger, fn func(context.Context)) {
	const (
		baseBackoff = 100 * time.Millisecond
		maxBackoff  = 30 * time.Second
		// 单位窗口内连续快速退出多少次后强制冷却到 maxBackoff，避免 stopCh 被关闭但 ctx 未取消时的死循环
		fastExitThreshold = 5
		fastExitWindow    = 5 * time.Second
	)
	backoff := baseBackoff
	fastExits := 0
	for {
		start := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Error("memory.pipeline.worker_panic",
						zap.String("worker", label),
						zap.Any("panic", r),
						zap.Stack("stack"))
				}
			}()
			fn(ctx)
		}()

		if ctx.Err() != nil {
			return
		}

		runtime := time.Since(start)
		if runtime > time.Minute {
			backoff = baseBackoff
			fastExits = 0
		}
		if runtime < fastExitWindow {
			fastExits++
			if fastExits >= fastExitThreshold {
				logger.Error("memory.pipeline.worker_fast_exit_loop",
					zap.String("worker", label),
					zap.Int("consecutive_fast_exits", fastExits),
					zap.Duration("last_runtime", runtime),
					zap.Duration("forced_backoff", maxBackoff))
				backoff = maxBackoff
				fastExits = 0
			}
		} else {
			fastExits = 0
		}
		logger.Warn("memory.pipeline.worker_exited",
			zap.String("worker", label),
			zap.Duration("runtime", runtime),
			zap.Duration("backoff", backoff))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// sleepCtx 等待 d，期间若 ctx 取消或 stopCh 关闭立即返回 false。
// 用于 Fetch 失败后的退避等待，确保关闭信号能立即生效。
func sleepCtx(ctx context.Context, stopCh <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-stopCh:
		return false
	case <-t.C:
		return true
	}
}
