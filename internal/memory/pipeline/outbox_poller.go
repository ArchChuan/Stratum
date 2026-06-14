package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// OutboxPoller polls memory_outbox tables across all tenant schemas and publishes
// events to NATS JetStream MEMORY_RAW stream.
type OutboxPoller struct {
	pool     *pgxpool.Pool
	js       jetstream.JetStream
	logger   *zap.Logger
	interval time.Duration
	batch    int
	stopCh   chan struct{}
}

// NewOutboxPoller creates an OutboxPoller with the given configuration.
func NewOutboxPoller(pool *pgxpool.Pool, js jetstream.JetStream, logger *zap.Logger, cfg Config) *OutboxPoller {
	interval := cfg.PollInterval
	if interval == 0 {
		interval = constants.MemoryOutboxPollInterval
	}
	batch := cfg.BatchSize
	if batch == 0 {
		batch = constants.MemoryOutboxBatchSize
	}
	return &OutboxPoller{
		pool:     pool,
		js:       js,
		logger:   logger,
		interval: interval,
		batch:    batch,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the polling loop. Blocks until ctx is cancelled or Stop is called.
func (p *OutboxPoller) Start(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			if err := p.poll(ctx); err != nil {
				p.logger.Error("memory.outbox.poll", zap.Error(err))
			}
		}
	}
}

// Stop signals the polling loop to exit.
func (p *OutboxPoller) Stop() {
	close(p.stopCh)
}

func (p *OutboxPoller) poll(ctx context.Context) error {
	tenants, err := tenantdb.ListTenantSchemas(ctx, p.pool)
	if err != nil {
		return fmt.Errorf("list tenant schemas: %w", err)
	}
	for _, schema := range tenants {
		if err := p.pollTenant(ctx, schema); err != nil {
			p.logger.Warn("memory.outbox.poll_tenant", zap.String("schema", schema), zap.Error(err))
		}
	}
	return nil
}

func (p *OutboxPoller) pollTenant(ctx context.Context, schema string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return fmt.Errorf("set schema: %w", err)
	}

	rows, err := tx.Query(ctx,
		"SELECT id, payload FROM memory_outbox ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED",
		p.batch)
	if err != nil {
		return fmt.Errorf("select outbox: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		var payload json.RawMessage
		if err := rows.Scan(&id, &payload); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}

		var ev MemoryRawEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			p.logger.Warn("memory.outbox.unmarshal", zap.Int64("id", id), zap.Error(err))
			ids = append(ids, id)
			continue
		}

		subject := fmt.Sprintf("%s.%s", constants.MemoryRawSubject, ev.TenantID)
		if _, err := p.js.Publish(ctx, subject, payload); err != nil {
			return fmt.Errorf("publish id=%d: %w", id, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}
	if len(ids) == 0 {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM memory_outbox WHERE id = ANY($1)", ids); err != nil {
		return fmt.Errorf("delete outbox: %w", err)
	}
	p.logger.Debug("memory.outbox.published", zap.String("schema", schema), zap.Int("count", len(ids)))
	return tx.Commit(ctx)
}
