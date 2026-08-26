package application

import (
	"context"
	"errors"
	"time"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"go.uber.org/zap"
)

// Ingest queue-capacity retry bounds. Startup sync can burst several tenants ×
// documents into the bounded ingest queue; a full queue must be retried with
// backoff (not silently dropped) so the surge is absorbed as background workers
// drain it. The shared budget bounds total startup delay: once exhausted,
// remaining docs are deferred to the next restart (idempotent by content hash)
// rather than blocking startup on a persistently-full queue.
const (
	ingestQueueRetryBaseDelay   = 500 * time.Millisecond
	ingestQueueRetryMaxDelay    = 8 * time.Second
	ingestQueueRetryMaxAttempts = 8
)

// ingestWithQueueRetry calls ingest, retrying with bounded exponential backoff
// when the ingest queue is full (ErrIngestQueueFull). Each wait drains
// budgetLeft; once it hits zero the error is returned immediately so startup is
// never blocked on a persistently-full queue. Non-queue errors are returned
// as-is on the first attempt. ingest is a closure so tests can inject a stub.
func ingestWithQueueRetry(
	ctx context.Context,
	ingest func(context.Context, IngestDocumentRequest) (*IngestResult, error),
	req IngestDocumentRequest,
	budgetLeft *time.Duration,
	logger *zap.Logger,
) (*IngestResult, error) {
	backoff := ingestQueueRetryBaseDelay
	for attempt := 0; attempt < ingestQueueRetryMaxAttempts; attempt++ {
		res, err := ingest(ctx, req)
		if err == nil {
			return res, nil
		}
		if !errors.Is(err, domain.ErrIngestQueueFull) {
			return nil, err
		}
		if attempt >= ingestQueueRetryMaxAttempts-1 || *budgetLeft <= 0 {
			return nil, err
		}
		wait := backoff
		if wait > *budgetLeft {
			wait = *budgetLeft
		}
		*budgetLeft -= wait
		logger.Debug("knowledge.builtin_sync.queue_full_retry",
			zap.Int("attempt", attempt+1),
			zap.Duration("wait", wait))
		if err := sleepIngestBackoff(ctx, &backoff, wait); err != nil {
			return nil, err
		}
	}
	return nil, domain.ErrIngestQueueFull
}

// sleepIngestBackoff waits the given delay (interrupting on ctx cancel) and
// grows backoff up to ingestQueueRetryMaxDelay for the next attempt. The budget
// drain happens in the caller so the elapsed-time accounting stays next to the
// sleep decision it mirrors.
func sleepIngestBackoff(ctx context.Context, backoff *time.Duration, wait time.Duration) error {
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return ctx.Err()
	}
	*backoff *= 2
	if *backoff > ingestQueueRetryMaxDelay {
		*backoff = ingestQueueRetryMaxDelay
	}
	return nil
}
