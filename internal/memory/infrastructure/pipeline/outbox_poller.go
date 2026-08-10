package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
)

// maxPublishErrorsPerRound 是单租户单轮 publish 失败的行级 ERROR 上限：
// 超过后仅计数，轮末统一输出一条汇总 ERROR。NATS 故障期间逐行打日志会
// 按 batch 放大成日志风暴，限流后每轮日志量恒定。
const maxPublishErrorsPerRound = 1

// OutboxPoller polls memory_outbox tables across all tenant schemas and publishes
// events to NATS JetStream MEMORY_RAW stream.
type OutboxPoller struct {
	pool     *pgxpool.Pool
	js       jetstream.JetStream
	logger   *zap.Logger
	interval time.Duration
	batch    int
	// dynamic 提供运行期可变的调度参数；nil 时回退 interval/batch 静态值。
	dynamic  *atomic.Pointer[DynamicConfig]
	stopCh   chan struct{}
	stopOnce sync.Once
	begin    func(context.Context) (pgx.Tx, error)
}

// WithDynamic 挂载热更新配置源。d 为 nil 时 poller 完全按静态值运行。
func (p *OutboxPoller) WithDynamic(d *atomic.Pointer[DynamicConfig]) *OutboxPoller {
	p.dynamic = d
	return p
}

func (p *OutboxPoller) currentInterval() time.Duration {
	if d := p.dynamic; d != nil {
		if dc := d.Load(); dc != nil && dc.PollInterval > 0 {
			return dc.PollInterval
		}
	}
	return p.interval
}

func (p *OutboxPoller) currentBatch() int {
	if d := p.dynamic; d != nil {
		if dc := d.Load(); dc != nil && dc.BatchSize > 0 {
			return dc.BatchSize
		}
	}
	return p.batch
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
		begin:    pool.Begin,
	}
}

// Start begins the polling loop. Blocks until ctx is cancelled or Stop is called.
func (p *OutboxPoller) Start(ctx context.Context) {
	interval := p.currentInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	p.logger.Info("memory.outbox.poller_started",
		zap.Duration("interval", interval),
		zap.Int("batch", p.currentBatch()))
	for {
		select {
		case <-ctx.Done():
			p.logger.Info("memory.outbox.poller_stopped", zap.String("cause", "ctx_done"))
			return
		case <-p.stopCh:
			p.logger.Info("memory.outbox.poller_stopped", zap.String("cause", "stop_called"))
			return
		case <-ticker.C:
			if cur := p.currentInterval(); cur != interval {
				interval = cur
				ticker.Reset(interval) // 同一 goroutine 内 Reset 安全
				p.logger.Info("memory.outbox.poller_interval_changed", zap.Duration("interval", interval))
			}
			if err := p.poll(ctx); err != nil {
				p.logger.Error("memory.outbox.poll", zap.Error(err))
			}
		}
	}
}

// Stop signals the polling loop to exit. Safe to call multiple times.
func (p *OutboxPoller) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
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

// pendingOutbox is a row taken from memory_outbox, ready for delivery.
type pendingOutbox struct {
	id      int64
	subject string
	payload json.RawMessage
}

// pollTenant delivers one tenant's outbox batch:
//  1. take: 短事务内 FOR UPDATE SKIP LOCKED 取出候选行，malformed 行就地
//     quarantine，随后提交（事务内严禁网络 IO）。
//  2. deliver: 提交后 NATS publish；失败行保留在 outbox，下一轮 poll 重试
//     （at-least-once，投递失败明确暴露 ERROR 日志 + error metric）。
//  3. confirm: 已投递行在独立事务中删除；删除失败向上传播（持久化失败不吞没），
//     行保留导致下轮重复投递 —— at-least-once 语义的已知边界。
func (p *OutboxPoller) pollTenant(ctx context.Context, schema string) error {
	pending, err := p.takeOutboxBatch(ctx, schema)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	var delivered []int64
	var failed int
	for _, row := range pending {
		pubCtx, cancel := context.WithTimeout(ctx, constants.MemoryOutboxPublishTimeout)
		_, pubErr := p.js.Publish(pubCtx, row.subject, row.payload)
		cancel()
		if pubErr != nil {
			failed++
			if failed <= maxPublishErrorsPerRound {
				p.logger.Error("memory.outbox.publish_failed",
					zap.String("schema", schema),
					zap.Int64("id", row.id),
					zap.String("subject", row.subject),
					zap.Error(pubErr))
			}
			outboxPublished.With(prometheus.Labels{"tenant_id": schema, "status": "error"}).Inc()
			continue
		}
		delivered = append(delivered, row.id)
		outboxPublished.With(prometheus.Labels{"tenant_id": schema, "status": "success"}).Inc()
	}
	if len(delivered) > 0 {
		if err := p.confirmDelivered(ctx, schema, delivered); err != nil {
			return fmt.Errorf("confirm delivered outbox: %w", err)
		}
		p.logger.Info("memory.outbox.published", zap.String("schema", schema), zap.Int("count", len(delivered)))
	}
	if failed > 0 {
		// 行级 ERROR 已限流（每轮最多 maxPublishErrorsPerRound 条）；
		// 轮末汇总暴露失败总数，行保留至下一轮 poll 重试（at-least-once）。
		p.logger.Error("memory.outbox.publish_failed_summary",
			zap.String("schema", schema),
			zap.Int("failed", failed),
			zap.Int("total", len(pending)),
			zap.String("retry", "next_round"))
		// 失败行保留可重试，但本轮失败必须暴露，不静默。
		return fmt.Errorf("publish failed for %d of %d outbox rows", failed, len(pending))
	}
	return nil
}

// setTenantSearchPath scopes the transaction to the tenant schema.
func setTenantSearchPath(ctx context.Context, tx pgx.Tx, schema string) error {
	_, err := tx.Exec(ctx,
		fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize()))
	if err != nil {
		return fmt.Errorf("set schema: %w", err)
	}
	return nil
}

// poisonRow 是 malformed 行的隔离记录。
type poisonRow struct {
	id   int64
	hash string
}

// takeOutboxBatch 在单事务内取出候选行并隔离 malformed 行，提交后返回待投递行。
func (p *OutboxPoller) takeOutboxBatch(ctx context.Context, schema string) ([]pendingOutbox, error) {
	tx, err := p.begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenantSearchPath(ctx, tx, schema); err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx,
		"SELECT id, payload FROM memory_outbox ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED",
		p.currentBatch())
	if err != nil {
		return nil, fmt.Errorf("select outbox: %w", err)
	}

	pending, poisonRows, err := p.scanOutboxRows(rows)
	if err != nil {
		return nil, err
	}
	if err := p.quarantineMalformed(ctx, tx, poisonRows); err != nil {
		return nil, err
	}
	outboxPending.Set(float64(len(pending) + len(poisonRows)))
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit outbox take: %w", err)
	}
	return pending, nil
}

// scanOutboxRows 读取候选行：合法行进入 pending，malformed 行记入 poisonRows。
// 关闭 rows 后再返回，保证调用方可以在同一事务上继续执行（pgx 禁止连接上
// 同时存在未消费的 rows 与新语句）。
func (p *OutboxPoller) scanOutboxRows(rows pgx.Rows) ([]pendingOutbox, []poisonRow, error) {
	defer rows.Close()
	var (
		pending    []pendingOutbox
		poisonRows []poisonRow
	)
	for rows.Next() {
		var id int64
		var payload json.RawMessage
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, nil, fmt.Errorf("scan row: %w", err)
		}

		var ev MemoryRawEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			p.logger.Warn("memory.outbox.unmarshal", zap.Int64("id", id), zap.Error(err))
			hash := fmt.Sprintf("%x", sha256.Sum256(payload))
			poisonRows = append(poisonRows, poisonRow{id: id, hash: hash})
			continue
		}
		pending = append(pending, pendingOutbox{
			id:      id,
			subject: fmt.Sprintf("%s.%s", constants.MemoryRawSubject, ev.TenantID),
			payload: payload,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("rows iteration: %w", err)
	}
	return pending, poisonRows, nil
}

// quarantineMalformed 就地隔离 malformed 行：写入 quarantine 表并从未投递队列删除。
func (p *OutboxPoller) quarantineMalformed(ctx context.Context, tx pgx.Tx, poisonRows []poisonRow) error {
	for _, poison := range poisonRows {
		if _, err := tx.Exec(ctx,
			`INSERT INTO memory_outbox_quarantine (outbox_id, payload_hash, error_class)
			 VALUES ($1, $2, $3) ON CONFLICT (outbox_id) DO NOTHING`,
			poison.id, poison.hash, "invalid_json",
		); err != nil {
			return fmt.Errorf("quarantine malformed outbox id=%d: %w", poison.id, err)
		}
	}
	if len(poisonRows) > 0 {
		poisonIDs := make([]int64, 0, len(poisonRows))
		for _, poison := range poisonRows {
			poisonIDs = append(poisonIDs, poison.id)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM memory_outbox WHERE id = ANY($1)", poisonIDs); err != nil {
			return fmt.Errorf("delete quarantined outbox: %w", err)
		}
	}
	return nil
}

// confirmDelivered 在独立事务中删除已投递行（commit 后执行，不再持有取出行锁）。
func (p *OutboxPoller) confirmDelivered(ctx context.Context, schema string, ids []int64) error {
	tx, err := p.begin(ctx)
	if err != nil {
		return fmt.Errorf("begin confirm tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenantSearchPath(ctx, tx, schema); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM memory_outbox WHERE id = ANY($1)", ids); err != nil {
		return fmt.Errorf("delete delivered outbox: %w", err)
	}
	return tx.Commit(ctx)
}
