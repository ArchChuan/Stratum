package application

import (
	"context"
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
	GetByID(ctx context.Context, id string) (*domain.AuditEvent, error)
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

// GetByID retrieves a single audit event.
func (s *AuditService) GetByID(ctx context.Context, id string) (*domain.AuditEvent, error) {
	return s.repo.GetByID(ctx, id)
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
				s.flush(&batch)
			}
		case <-ticker.C:
			s.flushIfNotEmpty(&batch)
		case <-s.closeCh:
			s.flushIfNotEmpty(&batch)
			return
		}
	}
}

func (s *AuditService) flushIfNotEmpty(batch *[]domain.AuditEvent) {
	if len(*batch) > 0 {
		s.flush(batch)
	}
}

func (s *AuditService) flush(batch *[]domain.AuditEvent) {
	toWrite := *batch
	*batch = (*batch)[:0]
	if s.metrics != nil {
		s.metrics.RecordAuditWriteQueueDepth(len(toWrite))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.repo.InsertBatch(ctx, toWrite); err != nil {
		s.logger.Error("audit: batch insert failed", zap.Int("count", len(toWrite)), zap.Error(err))
	}
}
