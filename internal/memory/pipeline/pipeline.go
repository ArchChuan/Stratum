package pipeline

import (
	"context"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/embedding"
	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// Pipeline orchestrates all memory pipeline workers: outbox poller,
// embedder workers, and enricher workers.
type Pipeline struct {
	cfg      Config
	pool     *pgxpool.Pool
	nc       *nats.Conn
	jsm      *JetStreamManager
	embedSvc *embedding.EmbeddingService
	vectorDB VectorStore
	llm      *llmgateway.Gateway
	logger   *zap.Logger

	poller    *OutboxPoller
	embedders []*EmbedderWorker
	enrichers []*EnricherWorker

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a Pipeline orchestrator. Call Start to begin processing.
func New(
	cfg Config,
	pool *pgxpool.Pool,
	nc *nats.Conn,
	embedSvc *embedding.EmbeddingService,
	vectorDB VectorStore,
	llm *llmgateway.Gateway,
	logger *zap.Logger,
) *Pipeline {
	return &Pipeline{
		cfg:      cfg,
		pool:     pool,
		nc:       nc,
		embedSvc: embedSvc,
		vectorDB: vectorDB,
		llm:      llm,
		logger:   logger,
	}
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

	pipeCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.poller.Start(pipeCtx)
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
		return fmt.Errorf("create embed consumer: %w", err)
	}

	for i := 0; i < p.cfg.EmbedWorkers; i++ {
		worker := NewEmbedderWorker(embedConsumer, js, p.embedSvc, p.vectorDB, p.logger)
		p.embedders = append(p.embedders, worker)
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			worker.Start(pipeCtx)
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
		return fmt.Errorf("create enrich consumer: %w", err)
	}

	for i := 0; i < p.cfg.EnrichWorkers; i++ {
		worker := NewEnricherWorker(enrichConsumer, p.pool, p.llm, p.logger, p.cfg)
		p.enrichers = append(p.enrichers, worker)
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			worker.Start(pipeCtx)
		}()
	}

	p.logger.Info("memory pipeline started",
		zap.Int("embed_workers", p.cfg.EmbedWorkers),
		zap.Int("enrich_workers", p.cfg.EnrichWorkers))

	return nil
}

// Stop gracefully shuts down all workers and waits for goroutines to exit.
func (p *Pipeline) Stop() {
	if p.cancel == nil {
		return
	}
	p.logger.Info("memory pipeline stopping")

	if p.poller != nil {
		p.poller.Stop()
	}
	for _, w := range p.embedders {
		w.Stop()
	}
	for _, w := range p.enrichers {
		w.Stop()
	}

	p.cancel()
	p.wg.Wait()
	p.logger.Info("memory pipeline stopped")
}
