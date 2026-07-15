package pipeline

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// VectorStore abstracts vector database operations for the embedder.
type VectorStore interface {
	Upsert(ctx context.Context, tenantID string, userID string, id string, vector []float32, metadata map[string]any) error
}

// EmbedderWorker consumes from MEMORY_RAW stream, generates embeddings,
// stores vectors in Milvus, and publishes enriched events to MEMORY_ENRICHED.
type EmbedderWorker struct {
	consumer      jetstream.Consumer
	js            dlqPublisher
	embedSvc      EmbedClient
	embedResolver EmbedServiceResolver
	vectorDB      VectorStore
	logger        *zap.Logger
	stopCh        chan struct{}
	stopOnce      sync.Once
	ackWait       time.Duration
	maxDeliver    int
}

// NewEmbedderWorker creates an EmbedderWorker.
func NewEmbedderWorker(
	consumer jetstream.Consumer,
	js dlqPublisher,
	embedSvc EmbedClient,
	vectorDB VectorStore,
	logger *zap.Logger,
	ackWait time.Duration,
	maxDeliver int,
) *EmbedderWorker {
	return &EmbedderWorker{
		consumer:   consumer,
		js:         js,
		embedSvc:   embedSvc,
		vectorDB:   vectorDB,
		logger:     logger,
		stopCh:     make(chan struct{}),
		ackWait:    ackWait,
		maxDeliver: maxDeliver,
	}
}

// WithEmbedResolver sets a per-tenant embedding resolver as fallback when embedSvc is nil.
func (w *EmbedderWorker) WithEmbedResolver(r EmbedServiceResolver) *EmbedderWorker {
	w.embedResolver = r
	return w
}

// Start begins consuming messages. Blocks until ctx is cancelled or Stop is called.
func (w *EmbedderWorker) Start(ctx context.Context) {
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
			w.logger.Warn("memory.embed.fetch_failed",
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

// Stop signals the worker to exit.
func (w *EmbedderWorker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

// safeProcessMessage isolates per-message panics so a single bad payload can't
// take down the whole worker goroutine.
func (w *EmbedderWorker) safeProcessMessage(ctx context.Context, msg jetstream.Msg) {
	defer func() {
		if r := recover(); r != nil {
			w.logger.Error("memory.embed.panic",
				zap.Any("panic", r),
				zap.String("subject", msg.Subject()),
				zap.Stack("stack"))
			embedTotal.With(prometheus.Labels{"tenant_id": "unknown", "status": "panic"}).Inc()
			_ = msg.Nak()
		}
	}()
	w.processMessage(ctx, msg)
}

func (w *EmbedderWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
	start := time.Now()
	stopHeartbeat := startProgressHeartbeat(msg, w.ackWait/2)
	defer stopHeartbeat()

	ev, err := UnmarshalRawEvent(msg.Data())
	if err != nil {
		w.logger.Error("memory.embed.unmarshal", zap.Error(err))
		embedTotal.With(prometheus.Labels{"tenant_id": "unknown", "status": "error"}).Inc()
		if dlqErr := deadLetter(ctx, w.js, msg, deadLetterDetails{Stage: "embed", ErrorCode: "invalid_event"}); dlqErr != nil {
			w.logger.Error("memory.embed.dlq", zap.Error(dlqErr))
		}
		return
	}

	traceID := ev.TraceID
	w.logger.Debug("memory.embed.start",
		zap.String("trace_id", traceID),
		zap.String("message_id", ev.MessageID),
		zap.String("tenant_id", ev.TenantID),
		zap.Int("content_length", len(ev.Content)))

	embedSvc := w.embedSvc
	if embedSvc == nil && w.embedResolver != nil {
		embedSvc = w.embedResolver(ctx, ev.TenantID)
	}
	if embedSvc == nil {
		w.logger.Warn("memory.embed.skip: no embedding service",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.String("tenant_id", ev.TenantID))
		if dlqErr := deadLetter(ctx, w.js, msg, deadLetterDetails{
			Stage: "embed", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "embed_service_unavailable",
		}); dlqErr != nil {
			w.logger.Error("memory.embed.dlq", zap.Error(dlqErr))
		}
		return
	}

	vector, err := embedSvc.EmbedVector(ctx, ev.Content)
	if err != nil {
		w.logger.Error("memory.embed.error",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.String("tenant_id", ev.TenantID),
			zap.Error(err))
		embedTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "error"}).Inc()
		if retryErr := retryOrDeadLetter(ctx, w.js, msg, w.maxDeliver, deadLetterDetails{
			Stage: "embed", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "embedding_failed",
		}); retryErr != nil {
			w.logger.Error("memory.embed.retry_or_dlq", zap.Error(retryErr))
		}
		return
	}

	metadata := map[string]any{
		"conversation_id": ev.ConversationID,
		"user_id":         ev.UserID,
		"agent_id":        ev.AgentID,
		"scope":           ev.Scope,
		"role":            ev.Role,
		"content":         ev.Content,
		"created_at":      ev.CreatedAt.Format(time.RFC3339),
	}
	if w.vectorDB == nil {
		w.logger.Warn("memory.embed.skip: vector store not configured",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.String("tenant_id", ev.TenantID))
		if dlqErr := deadLetter(ctx, w.js, msg, deadLetterDetails{
			Stage: "embed", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "vector_store_unavailable",
		}); dlqErr != nil {
			w.logger.Error("memory.embed.dlq", zap.Error(dlqErr))
		}
		return
	}
	if err := w.vectorDB.Upsert(ctx, ev.TenantID, ev.UserID, ev.MessageID, vector, metadata); err != nil {
		w.logger.Error("memory.embed.milvus",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.Error(err))
		embedTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "error"}).Inc()
		if retryErr := retryOrDeadLetter(ctx, w.js, msg, w.maxDeliver, deadLetterDetails{
			Stage: "embed", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "vector_upsert_failed",
		}); retryErr != nil {
			w.logger.Error("memory.embed.retry_or_dlq", zap.Error(retryErr))
		}
		return
	}

	enrichedEv := &MemoryEnrichedEvent{
		MemoryRawEvent: *ev,
		VectorID:       ev.MessageID,
	}
	data, err := enrichedEv.Marshal()
	if err != nil {
		w.logger.Error("memory.embed.marshal_enriched", zap.String("trace_id", traceID), zap.Error(err))
		embedTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "error"}).Inc()
		if retryErr := retryOrDeadLetter(ctx, w.js, msg, w.maxDeliver, deadLetterDetails{
			Stage: "embed", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "marshal_enriched_failed",
		}); retryErr != nil {
			w.logger.Error("memory.embed.retry_or_dlq", zap.Error(retryErr))
		}
		return
	}

	subject := fmt.Sprintf("%s.%s", constants.MemoryEnrichedSubject, ev.TenantID)
	if _, err := w.js.Publish(ctx, subject, data); err != nil {
		w.logger.Error("memory.embed.publish_enriched",
			zap.String("trace_id", traceID),
			zap.String("message_id", ev.MessageID),
			zap.Error(err))
		embedTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "error"}).Inc()
		if retryErr := retryOrDeadLetter(ctx, w.js, msg, w.maxDeliver, deadLetterDetails{
			Stage: "embed", TenantID: ev.TenantID, MessageID: ev.MessageID, ErrorCode: "publish_enriched_failed",
		}); retryErr != nil {
			w.logger.Error("memory.embed.retry_or_dlq", zap.Error(retryErr))
		}
		return
	}

	embedDuration.Observe(time.Since(start).Seconds())
	embedTotal.With(prometheus.Labels{"tenant_id": ev.TenantID, "status": "success"}).Inc()

	stopHeartbeat()
	_ = msg.Ack()
	w.logger.Info("memory.embed.success",
		zap.String("trace_id", traceID),
		zap.String("message_id", ev.MessageID),
		zap.String("tenant_id", ev.TenantID),
		zap.Int("vector_dim", len(vector)),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()))
}
