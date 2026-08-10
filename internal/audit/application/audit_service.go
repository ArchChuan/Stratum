package application

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AuditService implements both AuditRecorder (async fire-and-forget) and
// AuditQueryService (read path). Events are buffered in a channel and
// batch-inserted by a background goroutine.
type AuditService struct {
	repo    AuditRepo
	metrics observability.MetricsProvider
	logger  *zap.Logger

	buf     chan domain.AuditEvent
	closeCh chan struct{}
	done    chan struct{}
	closed  bool
	mu      sync.Mutex
}

// AuditRepo is the persistence contract consumed by AuditService.
type AuditRepo interface {
	InsertBatch(ctx context.Context, events []domain.AuditEvent) error
	Query(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error)
	Count(ctx context.Context, filter domain.AuditFilter) (int, error)
	GetByID(ctx context.Context, tenantID string, id string) (*domain.AuditEvent, error)
	DeleteOlderThan(ctx context.Context, before time.Time) error
}

// Ensure AuditService satisfies both ports.
var (
	_ port.AuditRecorder     = (*AuditService)(nil)
	_ port.AuditQueryService = (*AuditService)(nil)
)

// NewAuditService creates the audit service and starts the batch writer.
func NewAuditService(repo AuditRepo, metrics observability.MetricsProvider, logger *zap.Logger) *AuditService {
	if metrics == nil {
		metrics = observability.NoopMetrics{}
	}
	s := &AuditService{
		repo:    repo,
		metrics: metrics,
		logger:  logger,
		buf:     make(chan domain.AuditEvent, constants.AuditBufferSize),
		closeCh: make(chan struct{}),
		done:    make(chan struct{}),
	}
	go s.batchWriter()
	return s
}

// Record enqueues an audit event for async persistence. It never blocks
// the caller: if the buffer is full the event is dropped with a warning.
func (s *AuditService) Record(ctx context.Context, event domain.AuditEvent) error {
	if event.ID == "" {
		event.ID = uuid.Must(uuid.NewV7()).String()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	if event.RiskLevel == "" {
		event.RiskLevel = "low"
	}
	if event.Outcome == "" {
		event.Outcome = "success"
	}
	select {
	case s.buf <- event:
		if s.metrics != nil {
			s.metrics.IncAuditEvent(event.RiskLevel, event.Outcome)
		}
		return nil
	default:
		s.logger.Warn("audit buffer full, dropping event",
			zap.String("action", event.Action),
			zap.String("risk_level", event.RiskLevel),
		)
		return nil
	}
}

// Query reads audit events matching the filter.
func (s *AuditService) Query(ctx context.Context, filter domain.AuditFilter) ([]domain.AuditEvent, error) {
	return s.repo.Query(ctx, filter)
}

// Count returns the total number of events matching the filter.
func (s *AuditService) Count(ctx context.Context, filter domain.AuditFilter) (int, error) {
	return s.repo.Count(ctx, filter)
}

// GetByID retrieves a single audit event, scoped to the caller's tenant.
func (s *AuditService) GetByID(ctx context.Context, tenantID string, id string) (*domain.AuditEvent, error) {
	return s.repo.GetByID(ctx, tenantID, id)
}

// Stop gracefully shuts down the batch writer, flushing remaining events.
func (s *AuditService) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.closeCh)
	s.mu.Unlock()

	// Wait for batchWriter to flush its local batch and return.
	<-s.done

	// Drain any events that landed in the channel after batchWriter returned.
	remaining := make([]domain.AuditEvent, 0)
	for {
		select {
		case evt := <-s.buf:
			remaining = append(remaining, evt)
		default:
			goto flush
		}
	}
flush:
	if len(remaining) > 0 {
		if err := s.repo.InsertBatch(ctx, remaining); err != nil {
			s.logger.Error("audit: flush on stop failed", zap.Error(err))
			return err
		}
	}
	return nil
}

// DeleteOlderThan removes events older than the given time (retention cleanup).
func (s *AuditService) DeleteOlderThan(ctx context.Context, before time.Time) error {
	return s.repo.DeleteOlderThan(ctx, before)
}

// batchWriter is the background goroutine that flushes the buffer.
func (s *AuditService) batchWriter() {
	defer close(s.done)
	ticker := time.NewTicker(time.Duration(constants.AuditFlushInterval) * time.Millisecond)
	defer ticker.Stop()

	batch := make([]domain.AuditEvent, 0, constants.AuditBatchSize)
	for {
		select {
		case evt := <-s.buf:
			batch = append(batch, evt)
			if len(batch) >= constants.AuditBatchSize {
				// 失败已在 flush 内记录日志/metrics，batch 保留供下次重试。
				_ = s.flush(&batch)
			}
		case <-ticker.C:
			_ = s.flushIfNotEmpty(&batch)
		case <-s.closeCh:
			s.flushOrRequeueOnShutdown(&batch)
			return
		}
	}
}

// flushOrRequeueOnShutdown flushes the final batch once at shutdown. On
// failure the retained events are handed back to the channel so Stop's drain
// retries them instead of dropping them with the goroutine.
func (s *AuditService) flushOrRequeueOnShutdown(batch *[]domain.AuditEvent) {
	if err := s.flushIfNotEmpty(batch); err != nil {
		for _, evt := range *batch {
			select {
			case s.buf <- evt:
			default:
				// Buffer full at shutdown; nothing else can be done.
				s.logger.Error("audit: buffer full during shutdown, dropping event",
					zap.String("action", evt.Action))
			}
		}
	}
}

func (s *AuditService) flushIfNotEmpty(batch *[]domain.AuditEvent) error {
	if len(*batch) > 0 {
		return s.flush(batch)
	}
	return nil
}

// flush persists the accumulated batch. The batch is only cleared after a
// successful insert: on failure the events stay in place so the next flush
// (next event or ticker tick) retries them. Events must never vanish from
// memory before the database acknowledged them — a dropped batch is a
// permanent audit gap. The write is idempotent (INSERT ... ON CONFLICT DO
// NOTHING in the repo), so a retry after a partial write only fills the
// remaining rows instead of failing on duplicate IDs.
//
// The retry must not grow the batch without bound: capBatch bounds the
// in-memory footprint during a store outage (bounded memory wins over not
// losing events; the newest events are kept).
func (s *AuditService) flush(batch *[]domain.AuditEvent) error {
	if len(*batch) == 0 {
		return nil
	}
	toWrite := s.capBatch(batch)
	if s.metrics != nil {
		s.metrics.RecordAuditWriteQueueDepth(len(toWrite))
	}
	ctx, cancel := context.WithTimeout(context.Background(), constants.AuditFlushTimeout)
	defer cancel()
	if err := s.repo.InsertBatch(ctx, toWrite); err != nil {
		s.logger.Error("audit: batch insert failed, events retained for retry",
			zap.Int("count", len(toWrite)), zap.Error(err))
		if s.metrics != nil {
			s.metrics.IncComponentError("audit-batch-writer", "flush")
		}
		return fmt.Errorf("audit: flush: %w", err)
	}
	*batch = (*batch)[:0]
	return nil
}

// capBatch bounds the retained batch at MaxAuditBatchPending. On overflow the
// oldest events are dropped (the head of the slice) and the newest kept: with
// the store down, recent events matter most and the process must not OOM.
// 只影响「待重试的内存 batch」，不影响「成功才清空」的契约——被丢弃的是
// 内存上限之外的最旧事件，不是已确认写入的事件。
func (s *AuditService) capBatch(batch *[]domain.AuditEvent) []domain.AuditEvent {
	events := *batch
	overflow := len(events) - constants.MaxAuditBatchPending
	if overflow <= 0 {
		return events
	}
	*batch = events[overflow:]
	s.logger.Error("audit: batch overflow, dropping oldest events",
		zap.Int("dropped", overflow),
		zap.Int("retained", len(*batch)))
	if s.metrics != nil {
		s.metrics.IncComponentError("audit-batch-writer", "overflow")
	}
	return *batch
}
