package application

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// BufferScanner is a global worker that flushes idle/aged Redis message buffers.
// It is tenant-agnostic: key names encode tenantID so new tenants are automatically covered.
type BufferScanner struct {
	buffer   *MessageBuffer
	store    port.MessageBufferStore
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewBufferScanner(store port.MessageBufferStore, q port.ExtractionQueue, logger *zap.Logger) *BufferScanner {
	return &BufferScanner{
		buffer: NewMessageBuffer(store, q),
		store:  store,
		logger: logger,
		stopCh: make(chan struct{}),
	}
}

func (s *BufferScanner) Start(ctx context.Context) {
	s.logger.Info("memory.buffer_scanner.start")
	for {
		s.run(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		default:
			s.logger.Warn("memory.buffer_scanner.restarting_after_panic")
			time.Sleep(time.Second)
		}
	}
}

func (s *BufferScanner) run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("memory.buffer_scanner.panic", zap.Any("panic", r), zap.Stack("stack"))
		}
	}()
	ticker := time.NewTicker(constants.MemoryBufferScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *BufferScanner) scan(ctx context.Context) {
	var cursor uint64
	for {
		keys, next, err := s.store.Scan(ctx, cursor, "memory:buffer:meta:*", 100)
		if err != nil {
			s.logger.Warn("memory.buffer_scanner.scan_failed", zap.Error(err))
			return
		}
		for _, metaKey := range keys {
			s.checkAndFlush(ctx, metaKey)
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func (s *BufferScanner) checkAndFlush(ctx context.Context, metaKey string) {
	fields, err := s.store.HGetAll(ctx, metaKey)
	if err != nil || len(fields) == 0 {
		return
	}
	lastAt, err := time.Parse(time.RFC3339, fields["last_at"])
	if err != nil {
		return
	}
	firstAt, err := time.Parse(time.RFC3339, fields["first_at"])
	if err != nil {
		return
	}
	now := time.Now()
	if now.Sub(lastAt) < constants.MemoryBufferIdleTimeout && now.Sub(firstAt) < constants.MemoryBufferAgeTimeout {
		return
	}
	// metaKey = "memory:buffer:meta:{tid}:{uid}:{aid}:{cid}"
	const prefix = "memory:buffer:meta:"
	parts := strings.SplitN(metaKey[len(prefix):], ":", 4)
	if len(parts) != 4 {
		return
	}
	tid, uid, aid, cid := parts[0], parts[1], parts[2], parts[3]
	listKey := fmt.Sprintf("memory:buffer:%s:%s:%s:%s", tid, uid, aid, cid)
	if err := s.buffer.flush(ctx, listKey, tid, uid, aid, cid, fields["scope"]); err != nil {
		s.logger.Warn("memory.buffer_scanner.flush_failed", zap.String("meta_key", metaKey), zap.Error(err))
	}
}

func (s *BufferScanner) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}
