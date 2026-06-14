package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go/jetstream"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// EnrichmentResult holds the structured metadata extracted by the LLM.
type EnrichmentResult struct {
	Entities      []EntityExtraction `json:"entities"`
	Importance    float64            `json:"importance"`
	TokenEstimate int                `json:"token_estimate"`
	Keywords      []string           `json:"keywords"`
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
	consumer     jetstream.Consumer
	pool         *pgxpool.Pool
	llm          *llmgateway.Gateway
	logger       *zap.Logger
	model        string
	summaryModel string
	threshold    int
	stopCh       chan struct{}
}

// NewEnricherWorker creates an enricher configured from the pipeline Config.
func NewEnricherWorker(
	consumer jetstream.Consumer,
	pool *pgxpool.Pool,
	llm *llmgateway.Gateway,
	logger *zap.Logger,
	cfg Config,
) *EnricherWorker {
	model := cfg.EnrichModel
	if model == "" {
		model = "gpt-4o-mini"
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
		consumer:     consumer,
		pool:         pool,
		llm:          llm,
		logger:       logger,
		model:        model,
		summaryModel: summaryModel,
		threshold:    threshold,
		stopCh:       make(chan struct{}),
	}
}

// Start begins the consume loop. It blocks until ctx is cancelled or Stop is called.
func (w *EnricherWorker) Start(ctx context.Context) {
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
			continue
		}

		for msg := range msgs.Messages() {
			w.processMessage(ctx, msg)
		}
	}
}

// Stop signals the worker to exit its consume loop.
func (w *EnricherWorker) Stop() {
	close(w.stopCh)
}

func (w *EnricherWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
	start := time.Now()

	ev, err := UnmarshalEnrichedEvent(msg.Data())
	if err != nil {
		w.logger.Error("memory.enrich.unmarshal", zap.Error(err))
		_ = msg.Ack()
		return
	}

	w.logger.Debug("memory.enrich.start",
		zap.String("message_id", ev.MessageID),
		zap.String("tenant_id", ev.TenantID))

	enrichment, err := w.callEnrichLLM(ctx, ev.Role, ev.Content)
	if err != nil {
		w.logger.Error("memory.enrich.llm",
			zap.String("message_id", ev.MessageID),
			zap.Error(err))
		_ = msg.Nak()
		return
	}

	if err := w.persistEnrichment(ctx, ev, enrichment); err != nil {
		w.logger.Error("memory.enrich.persist",
			zap.String("message_id", ev.MessageID),
			zap.Error(err))
		_ = msg.Nak()
		return
	}

	_ = msg.Ack()
	w.logger.Info("memory.enrich.success",
		zap.String("message_id", ev.MessageID),
		zap.String("tenant_id", ev.TenantID),
		zap.Float64("importance", enrichment.Importance),
		zap.Int("entities", len(enrichment.Entities)),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()))
}

func (w *EnricherWorker) callEnrichLLM(ctx context.Context, role, content string) (*EnrichmentResult, error) {
	prompt := formatEnrichmentPrompt(role, content)
	req := &llmgateway.CompletionRequest{
		Model: w.model,
		Messages: []llmgateway.Message{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.1,
	}

	resp, err := w.llm.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm complete: %w", err)
	}

	var result EnrichmentResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return nil, fmt.Errorf("parse enrichment response: %w", err)
	}
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

	keywordsJSON, _ := json.Marshal(enrichment.Keywords)

	_, err = tx.Exec(ctx, `
		INSERT INTO memory_entries (id, user_id, agent_id, role, content, type, importance, keywords, token_estimate, scope_layer, enriched_at, conversation_id, created_at)
		VALUES ($1, $2, $3, $4, $5, 'long_term', $6, $7, $8, 1, NOW(), $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			importance = EXCLUDED.importance,
			keywords = EXCLUDED.keywords,
			token_estimate = EXCLUDED.token_estimate,
			enriched_at = NOW()`,
		ev.MessageID, ev.UserID, ev.AgentID, ev.Role, ev.Content,
		enrichment.Importance, keywordsJSON, enrichment.TokenEstimate,
		ev.ConversationID, ev.CreatedAt)
	if err != nil {
		return fmt.Errorf("upsert memory_entries: %w", err)
	}

	for _, entity := range enrichment.Entities {
		_, err = tx.Exec(ctx, `
			INSERT INTO memory_entities (name, type, confidence, first_seen, last_seen, tenant_id, user_id)
			VALUES ($1, $2, $3, NOW(), NOW(), $4, $5)
			ON CONFLICT (name, type, tenant_id, user_id) DO UPDATE SET
				confidence = GREATEST(memory_entities.confidence, EXCLUDED.confidence),
				last_seen = NOW()`,
			entity.Name, entity.Type, entity.Confidence, ev.TenantID, ev.UserID)
		if err != nil {
			return fmt.Errorf("upsert entity %s: %w", entity.Name, err)
		}
	}

	// Check token budget for summary trigger
	if err := w.maybeTriggerSummary(ctx, tx, ev); err != nil {
		w.logger.Warn("memory.enrich.summary_check",
			zap.String("conversation_id", ev.ConversationID),
			zap.Error(err))
	}

	return tx.Commit(ctx)
}

func (w *EnricherWorker) maybeTriggerSummary(ctx context.Context, tx pgx.Tx, ev *MemoryEnrichedEvent) error {
	if ev.ConversationID == "" {
		return nil
	}

	var accumulated int
	err := tx.QueryRow(ctx,
		"SELECT COALESCE(SUM(token_estimate), 0) FROM memory_entries WHERE conversation_id = $1 AND enriched_at IS NOT NULL",
		ev.ConversationID).Scan(&accumulated)
	if err != nil {
		return fmt.Errorf("check token budget: %w", err)
	}

	if accumulated < w.threshold {
		return nil
	}

	// Fetch recent messages for summary
	rows, err := tx.Query(ctx,
		"SELECT role, content FROM memory_entries WHERE conversation_id = $1 ORDER BY created_at ASC",
		ev.ConversationID)
	if err != nil {
		return fmt.Errorf("fetch messages for summary: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err != nil {
			return fmt.Errorf("scan message: %w", err)
		}
		fmt.Fprintf(&sb, "[%s]: %s\n", role, content)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows err: %w", err)
	}

	prompt := formatSummaryPrompt(sb.String())
	req := &llmgateway.CompletionRequest{
		Model: w.summaryModel,
		Messages: []llmgateway.Message{
			{Role: "user", Content: prompt},
		},
		Temperature: 0.3,
	}
	resp, err := w.llm.Complete(ctx, req)
	if err != nil {
		return fmt.Errorf("summary llm: %w", err)
	}

	summary := strings.TrimSpace(resp.Content)

	_, err = tx.Exec(ctx, `
		INSERT INTO memory_summaries (conversation_id, user_id, agent_id, summary, covered_until, token_count)
		VALUES ($1, $2, $3, $4, NOW(), $5)`,
		ev.ConversationID, ev.UserID, ev.AgentID, summary, resp.Usage.CompletionTokens)
	if err != nil {
		return fmt.Errorf("insert summary: %w", err)
	}

	w.logger.Info("memory.enrich.summary",
		zap.String("conversation_id", ev.ConversationID),
		zap.Int("token_budget", accumulated),
		zap.Int("summary_length", len(summary)))

	return nil
}
