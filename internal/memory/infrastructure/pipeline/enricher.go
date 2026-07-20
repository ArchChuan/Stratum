package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/tokenutil"
)

// EnrichmentResult holds the structured metadata extracted by the LLM.
type EnrichmentResult struct {
	Entities        []EntityExtraction `json:"entities"`
	Importance      float64            `json:"importance"`
	TokenEstimate   int                `json:"token_estimate"`
	Keywords        []string           `json:"keywords"`
	WorkContext     []string           `json:"work_context"`
	PersonalContext []string           `json:"personal_context"`
	TopOfMind       []string           `json:"top_of_mind"`
}

// EntityExtraction represents a single entity found in a message.
type EntityExtraction struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Confidence float64 `json:"confidence"`
}

// EnricherWorker consumes embedded events, calls the LLM for metadata
// extraction, persists enrichment results, and triggers conversation
// summaries when the token budget is exceeded.
type EnricherWorker struct {
	consumer       jetstream.Consumer
	js             dlqPublisher
	pool           *pgxpool.Pool
	llmResolver    LLMResolver
	logger         *zap.Logger
	model          string
	summaryModel   string
	threshold      int
	enrichmentTmpl string
	summaryTmpl    string
	stopCh         chan struct{}
	stopOnce       sync.Once
	ackWait        time.Duration
	maxDeliver     int
	snapshotRepo   port.ActiveSnapshotRepo
}

// NewEnricherWorker creates an enricher configured from the pipeline Config.
func NewEnricherWorker(
	consumer jetstream.Consumer,
	js dlqPublisher,
	pool *pgxpool.Pool,
	logger *zap.Logger,
	cfg Config,
) *EnricherWorker {
	model := cfg.EnrichModel
	if model == "" {
		model = "qwen-turbo"
	}
	summaryModel := cfg.SummaryModel
	if summaryModel == "" {
		summaryModel = model
	}
	threshold := cfg.SummaryTokenThreshold
	if threshold == 0 {
		threshold = constants.EnricherSummaryTokenThreshold
	}
	return &EnricherWorker{
		consumer:       consumer,
		js:             js,
		pool:           pool,
		logger:         logger,
		model:          model,
		summaryModel:   summaryModel,
		threshold:      threshold,
		enrichmentTmpl: cfg.EnrichmentPrompt,
		summaryTmpl:    cfg.SummaryPrompt,
		stopCh:         make(chan struct{}),
		ackWait:        cfg.EnrichAckWait,
		maxDeliver:     cfg.MaxDeliver,
		snapshotRepo:   persistence.NewActiveSnapshotRepo(pool),
	}
}

// WithLLMResolver sets a per-tenant LLM resolver used as the primary client
// for enrich/summary calls. The base llm is kept only as a fallback for
// resolver-less single-tenant test setups.
func (w *EnricherWorker) WithLLMResolver(r LLMResolver) *EnricherWorker {
	w.llmResolver = r
	return w
}

// llmFor returns the LLMClient for tenantID. Prefers the resolver-supplied
// per-tenant client; falls back to the base llm if the resolver is unset or
// returns nil. Returns nil only when no LLM is wired at all — callers must
// llmFor returns the per-tenant LLMClient. Returns nil when no resolver is set
// or the resolver returns nil — callers must nil-check before calling Complete.
func (w *EnricherWorker) llmFor(ctx context.Context, tenantID string) LLMClient {
	if w.llmResolver != nil {
		return w.llmResolver(ctx, tenantID)
	}
	return nil
}

// Start begins the consume loop. It blocks until ctx is cancelled or Stop is called.
func (w *EnricherWorker) Start(ctx context.Context) {
	backoff := constants.MemoryFetchBackoffBase
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		default:
		}

		msgs, err := w.consumer.Fetch(1, jetstream.FetchMaxWait(5*time.Second))
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			w.logger.Warn("memory.enrich.fetch_failed",
				zap.Error(err),
				zap.Duration("backoff", backoff))
			if !sleepCtx(ctx, w.stopCh, backoff) {
				return
			}
			if backoff < constants.MemoryFetchBackoffMax {
				backoff *= 2
				if backoff > constants.MemoryFetchBackoffMax {
					backoff = constants.MemoryFetchBackoffMax
				}
			}
			continue
		}
		backoff = constants.MemoryFetchBackoffBase

		for msg := range msgs.Messages() {
			w.safeProcessMessage(ctx, msg)
		}
	}
}

// Stop signals the worker to exit its consume loop.
func (w *EnricherWorker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

// safeProcessMessage isolates per-message panics so a single bad payload can't
// take down the whole worker goroutine.
func (w *EnricherWorker) safeProcessMessage(ctx context.Context, msg jetstream.Msg) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.enrich.panic",
				zap.Any("panic", r),
				zap.String("subject", msg.Subject()),
				zap.Stack("stack"))
			enrichTotal.With(prometheus.Labels{"tenant_id": "unknown", "status": "panic"}).Inc()
			_ = msg.Nak()
		}
	}()
	w.processMessage(ctx, msg)
}

func (w *EnricherWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
	start := time.Now()
	stopHeartbeat := startProgressHeartbeat(msg, w.ackWait/2)
	defer stopHeartbeat()

	ev, err := UnmarshalEnrichedEvent(msg.Data())
	if err != nil {
		w.logger.Error("memory.enrich.unmarshal", zap.Error(err))
		enrichTotal.With(prometheus.Labels{"tenant_id": "unknown", "status": "error"}).Inc()
		if dlqErr := deadLetterWithHeartbeat(
			ctx, w.js, msg, stopHeartbeat, deadLetterDetails{Stage: "enrich", ErrorCode: "invalid_event"},
		); dlqErr != nil {
			w.logger.Error("memory.enrich.dlq", zap.Error(dlqErr))
		}
		return
	}

	traceID := ev.TraceID
	w.logger.Debug("memory.enrich.start",
		zap.String("trace_id", traceID),
		zap.String("message_id", ev.MessageID),
		zap.String("tenant_id", ev.TenantID))

	llm := w.llmFor(ctx, ev.TenantID)
	if llm == nil {
		if dlqErr := deadLetterWithHeartbeat(ctx, w.js, msg, stopHeartbeat, deadLetterDetails{
			Stage: "enrich", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "llm_service_unavailable",
		}); dlqErr != nil {
			w.logger.Error("memory.enrich.dlq", zap.Error(dlqErr))
		}
		return
	}
	enrichment, err := w.callEnrichLLM(ctx, llm, ev.Role, ev.Content)
	if err != nil {
		w.logger.Error("memory.enrich.llm",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.Error(err))
		enrichTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "error"}).Inc()
		if retryErr := retryOrDeadLetterWithHeartbeat(ctx, w.js, msg, w.maxDeliver, stopHeartbeat, deadLetterDetails{
			Stage: "enrich", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "llm_failed",
		}); retryErr != nil {
			w.logger.Error("memory.enrich.retry_or_dlq", zap.Error(retryErr))
		}
		return
	}

	if err := w.persistEnrichment(ctx, ev, enrichment); err != nil {
		w.logger.Error("memory.enrich.persist",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.Error(err))
		enrichTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "error"}).Inc()
		if retryErr := retryOrDeadLetterWithHeartbeat(ctx, w.js, msg, w.maxDeliver, stopHeartbeat, deadLetterDetails{
			Stage: "enrich", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "persist_failed",
		}); retryErr != nil {
			w.logger.Error("memory.enrich.retry_or_dlq", zap.Error(retryErr))
		}
		return
	}
	_ = w.refreshActiveSnapshot(ctx, ev, enrichment)

	enrichDuration.Observe(time.Since(start).Seconds())
	enrichTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "success"}).Inc()
	entitiesExtracted.Add(float64(len(enrichment.Entities)))

	stopHeartbeat()
	if err := msg.Ack(); err != nil {
		w.logger.Warn("memory.enrich.ack_failed",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.Error(err))
	}
	w.logger.Info("memory.enrich.success",
		zap.String("trace_id", traceID),
		zap.String("message_id", ev.MessageID),
		zap.String("tenant_id", ev.TenantID),
		zap.Float64("importance", enrichment.Importance),
		zap.Int("entities", len(enrichment.Entities)),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()))

	// 关键：摘要触发必须在 Ack 之后、事务之外执行。
	// 原实现把 LLM 调用塞在 persistEnrichment 的事务里，单次摘要 30s+，
	// 一个慢富化能把整个 pgxpool 连接池耗尽，连带主流程 DB 调用全部超时。
	// 这里独立 ctx + 独立 tx + 独立 panic recover，失败只 warn 不影响主流程。
	w.runSummaryAsyncSafe(ctx, ev)
}

func (w *EnricherWorker) refreshActiveSnapshot(ctx context.Context, ev *MemoryEnrichedEvent, enrichment *EnrichmentResult) error {
	if w.snapshotRepo == nil || (len(enrichment.WorkContext) == 0 && len(enrichment.PersonalContext) == 0 && len(enrichment.TopOfMind) == 0) {
		return nil
	}
	eventTime := ev.CreatedAt.UTC()
	if eventTime.IsZero() {
		w.logger.Warn("memory.enrich.active_snapshot_zero_event_time",
			zap.String("tenant_id", ev.TenantID), zap.String("message_id", ev.MessageID))
		return nil
	}
	snapshot := &domain.ActiveSnapshot{
		TenantID: ev.TenantID, UserID: ev.UserID, AgentID: ev.AgentID,
		WorkContext: enrichment.WorkContext, PersonalContext: enrichment.PersonalContext, TopOfMind: enrichment.TopOfMind,
		Source:    domain.SnapshotSource{Type: "message", Reference: ev.MessageID},
		ExpiresAt: eventTime.Add(constants.ActiveSnapshotTTL), UpdatedAt: eventTime, Status: domain.SnapshotStatusActive,
	}
	if err := w.snapshotRepo.Upsert(ctx, snapshot); err != nil {
		w.logger.Warn("memory.enrich.active_snapshot_failed", zap.String("tenant_id", ev.TenantID), zap.Error(err))
		enrichTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "snapshot_error"}).Inc()
	}
	return nil
}

func (w *EnricherWorker) runSummaryAsyncSafe(ctx context.Context, ev *MemoryEnrichedEvent) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.enrich.summary_panic",
				zap.String("trace_id", ev.TraceID),
				zap.String("conversation_id", ev.ConversationID),
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()
	if err := w.maybeTriggerSummary(ctx, ev); err != nil {
		w.logger.Warn("memory.enrich.summary_check",
			zap.String("trace_id", ev.TraceID),
			zap.String("conversation_id", ev.ConversationID),
			zap.Error(err))
	}
}

func (w *EnricherWorker) callEnrichLLM(ctx context.Context, llm LLMClient, role, content string) (*EnrichmentResult, error) {
	prompt := formatEnrichmentPrompt(w.enrichmentTmpl, role, content)
	req := &port.CompletionRequest{
		Model: w.model,
		Messages: []port.CompletionMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
	}

	llmCtx, cancel := context.WithTimeout(ctx, constants.MemoryEnrichLLMTimeout)
	defer cancel()
	resp, err := llm.Complete(llmCtx, req)
	if err != nil {
		return nil, fmt.Errorf("llm complete: %w", err)
	}

	var result EnrichmentResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return nil, fmt.Errorf("parse enrichment response: %w", err)
	}
	// token_estimate 由代码计算，不依赖 LLM 自填（不可靠）
	result.TokenEstimate = tokenutil.EstimateText(content)
	return &result, nil
}

func (w *EnricherWorker) persistEnrichment(ctx context.Context, ev *MemoryEnrichedEvent, enrichment *EnrichmentResult) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	schema := "tenant_" + ev.TenantID
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return fmt.Errorf("set schema: %w", err)
	}

	keywords := enrichment.Keywords
	if keywords == nil {
		keywords = []string{}
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO memory_entries (id, user_id, agent_id, scope, role, content, type, importance, keywords, token_estimate, enriched_at, conversation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'long_term', $7, $8, $9, NOW(), $10, $11)
		ON CONFLICT (id) DO UPDATE SET
			importance = EXCLUDED.importance,
			keywords = EXCLUDED.keywords,
			token_estimate = EXCLUDED.token_estimate,
			enriched_at = NOW()`,
		ev.MessageID, ev.UserID, ev.AgentID, ev.Scope, ev.Role, ev.Content,
		enrichment.Importance, keywords, enrichment.TokenEstimate,
		ev.ConversationID, ev.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert memory_entries: %w", err)
	}

	for _, entity := range enrichment.Entities {
		_, err = tx.Exec(ctx, `
			INSERT INTO memory_entities (name, entity_type, user_id, agent_id, scope, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, NOW())
			ON CONFLICT (name, entity_type, user_id, COALESCE(agent_id, '')) DO UPDATE SET
				last_seen_at = NOW()`,
			entity.Name, entity.Type, ev.UserID, ev.AgentID, ev.Scope)
		if err != nil {
			return fmt.Errorf("upsert entity %s: %w", entity.Name, err)
		}
	}

	return tx.Commit(ctx)
}

// maybeTriggerSummary 走完全独立的事务生命周期：
//  1. 短事务读阈值 + 历史消息（持锁 ≤几十 ms）
//  2. Rollback 后调 LLM（30s+，与 DB 解耦）
//  3. 新事务写 memory_summaries
//
// 老实现把 LLM Complete 塞在 persistEnrichment 的事务里，单条记录持锁 30s+，
// 高 QPS 下 pgxpool 连接耗尽，主流程全部 DB 调用排队超时甚至拖崩 worker。
func (w *EnricherWorker) maybeTriggerSummary(ctx context.Context, ev *MemoryEnrichedEvent) error {
	if ev.ConversationID == "" {
		return nil
	}

	schema := "tenant_" + ev.TenantID
	accumulated, prevSummary, sb, err := w.fetchSummaryInputs(ctx, schema, ev.ConversationID)
	if err != nil {
		return err
	}
	if sb == nil {
		return nil
	}

	llm := w.llmFor(ctx, ev.TenantID)
	if llm == nil {
		return fmt.Errorf("no llm client configured for tenant %s", ev.TenantID)
	}
	input := sb.String()
	if prevSummary != "" {
		input = "[Previous Summary]: " + prevSummary + "\n\n[New Messages]:\n" + input
	}
	prompt := formatSummaryPrompt(w.summaryTmpl, input)
	req := &port.CompletionRequest{
		Model: w.summaryModel,
		Messages: []port.CompletionMessage{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
	}
	llmCtx, cancel := context.WithTimeout(ctx, constants.MemorySummaryLLMTimeout)
	defer cancel()
	resp, err := llm.Complete(llmCtx, req)
	if err != nil {
		return fmt.Errorf("summary llm: %w", err)
	}
	summary := strings.TrimSpace(resp.Content)

	if err := w.writeSummary(ctx, schema, ev, summary, resp.CompletionTokens); err != nil {
		return err
	}

	summaryTriggered.Inc()
	w.logger.Info("memory.enrich.summary",
		zap.String("trace_id", ev.TraceID),
		zap.String("conversation_id", ev.ConversationID),
		zap.Int("token_budget", accumulated),
		zap.Int("summary_length", len(summary)))
	return nil
}

// fetchSummaryInputs 在短事务里检查阈值并捞历史消息，立即释放事务后再去调 LLM。
// 返回 (累计 token, 历史消息文本 builder, err)。当 builder 为 nil 时表示无需触发摘要。
func (w *EnricherWorker) fetchSummaryInputs(ctx context.Context, schema, convID string) (int, string, *strings.Builder, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return 0, "", nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return 0, "", nil, fmt.Errorf("set schema: %w", err)
	}

	var accumulated int
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(token_estimate), 0) FROM memory_entries
		WHERE conversation_id = $1 AND enriched_at IS NOT NULL
		AND created_at > COALESCE(
			(SELECT covered_until FROM memory_summaries WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1),
			'1970-01-01'
		)`, convID).Scan(&accumulated); err != nil {
		return 0, "", nil, fmt.Errorf("check token budget: %w", err)
	}
	if accumulated < w.threshold {
		return accumulated, "", nil, nil
	}

	var prevSummary string
	_ = tx.QueryRow(ctx,
		"SELECT summary FROM memory_summaries WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1",
		convID).Scan(&prevSummary)

	rows, err := tx.Query(ctx, `
		SELECT role, content FROM memory_entries
		WHERE conversation_id = $1
		AND created_at > COALESCE(
			(SELECT covered_until FROM memory_summaries WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1),
			'1970-01-01'
		)
		ORDER BY created_at ASC LIMIT $2`,
		convID, constants.EnricherSummaryMaxMessages)
	if err != nil {
		return 0, "", nil, fmt.Errorf("fetch messages: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return 0, "", nil, fmt.Errorf("scan message: %w", err)
		}
		fmt.Fprintf(&sb, "[%s]: %s\n", role, content)
	}
	if err := rows.Err(); err != nil {
		return 0, "", nil, fmt.Errorf("rows err: %w", err)
	}
	return accumulated, prevSummary, &sb, nil
}

func (w *EnricherWorker) writeSummary(ctx context.Context, schema string, ev *MemoryEnrichedEvent, summary string, tokens int) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin write tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return fmt.Errorf("set schema: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memory_summaries (conversation_id, user_id, agent_id, scope, summary, covered_until, token_count)
		VALUES ($1, $2, $3, $4, $5, NOW(), $6)`,
		ev.ConversationID, ev.UserID, ev.AgentID, ev.Scope, summary, tokens); err != nil {
		return fmt.Errorf("insert summary: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit summary: %w", err)
	}
	return nil
}
