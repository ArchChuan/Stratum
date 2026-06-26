package workers

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// WorkerSet is a set of per-tenant workers with Start/Stop lifecycle.
type WorkerSet []interface {
	Start(context.Context)
	Stop()
}

type tenantEntry struct {
	cancel  context.CancelFunc
	workers WorkerSet
}

// TenantWatcher polls the tenant list and dynamically spawns/stops per-tenant workers.
// New tenants added after startup are automatically covered on the next reconcile tick.
type TenantWatcher struct {
	db       *pgxpool.Pool
	build    func(tenantID string) WorkerSet
	running  map[string]tenantEntry
	mu       sync.Mutex
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewTenantWatcher(db *pgxpool.Pool, build func(string) WorkerSet, logger *zap.Logger) *TenantWatcher {
	return &TenantWatcher{
		db:      db,
		build:   build,
		running: make(map[string]tenantEntry),
		logger:  logger,
		stopCh:  make(chan struct{}),
	}
}

func (w *TenantWatcher) Start(ctx context.Context) {
	runWithRestart(ctx, w.stopCh, w.logger, "memory.tenant_watcher", w.run)
}

func (w *TenantWatcher) run(ctx context.Context) {
	w.logger.Info("memory.tenant_watcher.start")
	w.reconcile(ctx)
	ticker := time.NewTicker(constants.MemoryTenantWatchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			w.stopAll()
			return
		case <-w.stopCh:
			w.stopAll()
			return
		case <-ticker.C:
			w.reconcile(ctx)
		}
	}
}

func (w *TenantWatcher) reconcile(ctx context.Context) {
	rows, err := w.db.Query(ctx, `SELECT id::text FROM public.tenants WHERE deleted_at IS NULL`)
	if err != nil {
		w.logger.Warn("memory.tenant_watcher.list_tenants_failed", zap.Error(err))
		return
	}
	defer rows.Close()

	active := make(map[string]bool)
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			active[id] = true
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	for tid, entry := range w.running {
		if !active[tid] {
			entry.cancel()
			for _, wk := range entry.workers {
				wk.Stop()
			}
			delete(w.running, tid)
			w.logger.Info("memory.tenant_watcher.tenant_removed", zap.String("tenant_id", tid))
		}
	}
	for tid := range active {
		if _, ok := w.running[tid]; ok {
			continue
		}
		workerCtx, cancel := context.WithCancel(ctx)
		ws := w.build(tid)
		w.running[tid] = tenantEntry{cancel: cancel, workers: ws}
		for _, worker := range ws {
			go worker.Start(workerCtx)
		}
		w.logger.Info("memory.tenant_watcher.tenant_added", zap.String("tenant_id", tid))
	}
}

func (w *TenantWatcher) stopAll() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, entry := range w.running {
		entry.cancel()
		for _, wk := range entry.workers {
			wk.Stop()
		}
	}
	w.running = make(map[string]tenantEntry)
}

func (w *TenantWatcher) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}
