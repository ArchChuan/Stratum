package pipeline

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// EmbedClient produces a vector embedding for a piece of text.
// Defined consumer-side so the pipeline depends on behavior rather than the concrete
// embedding.EmbeddingService implementation.
type EmbedClient interface {
	EmbedVector(ctx context.Context, text string) ([]float32, error)
	GetVectorDimension() int
	// Model returns the embedding model name in use (collection 命名依赖).
	Model() string
}

// LLMClient performs a single non-streaming completion against an LLM provider.
// Defined consumer-side; concrete *llmgateway.Gateway satisfies it structurally.
type LLMClient = llmdomain.Completer

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
	vectorCleaner entryVectorDeleter
	llmResolver   LLMResolver
	paramResolver port.PlatformParamResolver
	logger        *zap.Logger

	// dynamic 是热更新调度参数源（Nacos 经 wiring 桥接），每轮由 poller re-read。
	dynamic *atomic.Pointer[DynamicConfig]

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

// SetEntryVectorDeleter wires the cleaner used to remove orphan vectors when a
// pipeline stage dead-letters after the embedder already wrote the vector.
// Must be called before Start.
func (p *Pipeline) SetEntryVectorDeleter(d entryVectorDeleter) {
	p.vectorCleaner = d
}

// SetLLMResolver sets a per-tenant LLM resolver used by EnricherWorkers.
// Must be called before Start. Without it, enrich/summary fall back to the
// shared p.llm (which has no provider clients in production), so callers
// running multi-tenant pipelines should always set this.
func (p *Pipeline) SetLLMResolver(r LLMResolver) {
	p.llmResolver = r
}

// SetPlatformParamResolver sets the platform parameter resolver used by
// enricher workers to resolve per-call LLM content settings (model /
// temperature / prompt / threshold). Must be called before Start. Without it,
// workers keep their const defaults, matching pre-config behaviour.
func (p *Pipeline) SetPlatformParamResolver(r port.PlatformParamResolver) {
	p.paramResolver = r
}

// WithDynamic 挂载热更新调度参数源（Nacos 经 wiring 桥接）。
// 必须在 Start 之前调用。
func (p *Pipeline) WithDynamic(d *atomic.Pointer[DynamicConfig]) *Pipeline {
	p.dynamic = d
	return p
}

// buildPoller 创建 outbox poller 并挂载热更新配置源（若有）。
// 抽出为独立函数以保持 Start 复杂度在门禁内。
func (p *Pipeline) buildPoller(js jetstream.JetStream) {
	p.poller = NewOutboxPoller(p.pool, js, p.logger, p.cfg)
	if p.dynamic != nil {
		p.poller.WithDynamic(p.dynamic)
	}
}

// buildEnricher 创建单个 enricher worker 并挂载可选的 LLM/平台参数解析器。
// 抽出为独立函数以保持 Start 复杂度在门禁内。
func (p *Pipeline) buildEnricher(consumer jetstream.Consumer, js dlqPublisher, i int) *EnricherWorker {
	worker := NewEnricherWorker(consumer, js, p.pool, p.logger, p.cfg)
	if p.llmResolver != nil {
		worker.WithLLMResolver(p.llmResolver)
	}
	if p.paramResolver != nil {
		worker.WithParamResolver(p.paramResolver)
	}
	if p.vectorCleaner != nil {
		worker.WithEntryVectorDeleter(p.vectorCleaner)
	}
	return worker
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
	p.buildPoller(js)

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
		if p.vectorCleaner != nil {
			worker.WithEntryVectorDeleter(p.vectorCleaner)
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
		worker := p.buildEnricher(enrichConsumer, js, i)
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
		baseBackoff       = 100 * time.Millisecond
		maxBackoff        = 30 * time.Second
		fastExitThreshold = 5
		fastExitWindow    = 5 * time.Second
	)
	b := newBackoffController(baseBackoff, maxBackoff, fastExitThreshold, fastExitWindow)
	for {
		start := time.Now()
		runWithRecovery(label, logger, fn, ctx)

		if ctx.Err() != nil {
			return
		}

		runtime := time.Since(start)
		backoff := b.compute(runtime)
		logger.Warn("memory.pipeline.worker_exited",
			zap.String("worker", label),
			zap.Duration("runtime", runtime),
			zap.Duration("backoff", backoff))

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func runWithRecovery(label string, logger *zap.Logger, fn func(context.Context), ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("memory.pipeline.worker_panic",
				zap.String("worker", label),
				zap.Any("panic", r),
				zap.Stack("stack"))
			pipelinePanics.WithLabelValues(label).Inc()
		}
	}()
	fn(ctx)
}

type backoffController struct {
	base, max         time.Duration
	fastExitThreshold int
	fastExitWindow    time.Duration
	current           time.Duration
	fastExits         int
}

func newBackoffController(base, max time.Duration, threshold int, window time.Duration) *backoffController {
	return &backoffController{
		base:              base,
		max:               max,
		fastExitThreshold: threshold,
		fastExitWindow:    window,
		current:           base,
	}
}

func (b *backoffController) compute(runtime time.Duration) time.Duration {
	if runtime > time.Minute {
		b.current = b.base
		b.fastExits = 0
		return b.current
	}
	if runtime < b.fastExitWindow {
		b.fastExits++
		if b.fastExits >= b.fastExitThreshold {
			b.current = b.max
			b.fastExits = 0
		}
	} else {
		b.fastExits = 0
	}
	result := b.current
	b.current = min(b.current*2, b.max)
	return result
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
