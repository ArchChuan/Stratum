# Memory v2 Workers Implementation Plan (Phase 5)

**Goal:** Build 6 background workers for async fact processing, supersede, embedding, profile rebuild, and GC.

**Architecture:** Each worker is independent goroutine consuming from extraction_queue (PostgreSQL FOR UPDATE SKIP LOCKED) or scheduled cron. Workers wrap MemoryService methods with retry, backoff, panic recovery, and Prometheus metrics. Lifecycle managed by Harness (start in order, stop reverse).

**Tech Stack:** Go 1.22+, robfig/cron v3, Prometheus client, zap logger, pgx v5

---

## Global Constraints

- All workers implement `Start(ctx)` and `Stop()` (idempotent via sync.Once)
- Panic recovery per-message: single bad payload must not kill goroutine
- Backoff: base=1s, max=30s, exponential doubling on fetch failure
- Metrics labels: `tenant_id`, `status` (success/error/panic)
- Log fields: `trace_id`, `tenant_id`, `worker_name`, `latency_ms`
- GC workers run via cron: extraction_worker is queue-driven, others are scheduled
- All workers wired through `api/wiring/memory.go` Container
- Test coverage ≥80%, mock MemoryService

---

## File Structure

```
internal/memory/infrastructure/workers/
├── extraction_worker.go        # Polls extraction_queue, calls ExtractFacts
├── extraction_worker_test.go
├── supersede_worker.go         # Detects similar facts, runs LLM judgment
├── supersede_worker_test.go
├── embed_worker.go             # Generates embeddings, upserts to Milvus
├── embed_worker_test.go
├── profile_worker.go           # Rebuilds entity profiles (7-day or 5-fact delta)
├── profile_worker_test.go
├── gc_worker.go                # Soft-delete expired facts (30d for deleted, 90d for superseded/archived)
├── gc_worker_test.go
├── metrics.go                  # Prometheus metrics
└── helpers.go                  # sleepCtx, panic recovery helpers
```

---

## Task 1: Worker Metrics and Helpers

**Files:**

- Create: `internal/memory/infrastructure/workers/metrics.go`
- Create: `internal/memory/infrastructure/workers/helpers.go`

**Interfaces:**

- Produces: Prometheus collectors, `sleepCtx` helper

- [ ] **Step 1: Write helpers test**

```go
// internal/memory/infrastructure/workers/helpers_test.go
package workers_test

import (
 "context"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
 "github.com/stretchr/testify/require"
)

func TestSleepCtx_CancelledByContext(t *testing.T) {
 ctx, cancel := context.WithCancel(context.Background())
 stopCh := make(chan struct{})
 go func() {
  time.Sleep(10 * time.Millisecond)
  cancel()
 }()
 ok := workers.SleepCtx(ctx, stopCh, 1*time.Second)
 require.False(t, ok, "should return false when context cancelled")
}

func TestSleepCtx_CancelledByStop(t *testing.T) {
 ctx := context.Background()
 stopCh := make(chan struct{})
 go func() {
  time.Sleep(10 * time.Millisecond)
  close(stopCh)
 }()
 ok := workers.SleepCtx(ctx, stopCh, 1*time.Second)
 require.False(t, ok, "should return false when stop signalled")
}

func TestSleepCtx_FullDuration(t *testing.T) {
 ctx := context.Background()
 stopCh := make(chan struct{})
 start := time.Now()
 ok := workers.SleepCtx(ctx, stopCh, 50*time.Millisecond)
 require.True(t, ok)
 require.GreaterOrEqual(t, time.Since(start).Milliseconds(), int64(50))
}
```

- [ ] **Step 2: Verify failure**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestSleepCtx`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement helpers and metrics**

```go
// internal/memory/infrastructure/workers/helpers.go
package workers

import (
 "context"
 "time"
)

// SleepCtx sleeps for d but returns early if ctx done or stopCh closed.
// Returns true if the full duration elapsed, false if interrupted.
func SleepCtx(ctx context.Context, stopCh <-chan struct{}, d time.Duration) bool {
 t := time.NewTimer(d)
 defer t.Stop()
 select {
 case <-t.C:
  return true
 case <-ctx.Done():
  return false
 case <-stopCh:
  return false
 }
}
```

```go
// internal/memory/infrastructure/workers/metrics.go
package workers

import "github.com/prometheus/client_golang/prometheus"

var (
 WorkerProcessTotal = prometheus.NewCounterVec(
  prometheus.CounterOpts{
   Name: "memory_worker_process_total",
   Help: "Total messages processed per worker",
  },
  []string{"worker", "tenant_id", "status"},
 )
 WorkerProcessDuration = prometheus.NewHistogramVec(
  prometheus.HistogramOpts{
   Name:    "memory_worker_process_duration_seconds",
   Help:    "Worker processing duration",
   Buckets: prometheus.DefBuckets,
  },
  []string{"worker"},
 )
 WorkerQueueDepth = prometheus.NewGaugeVec(
  prometheus.GaugeOpts{
   Name: "memory_worker_queue_depth",
   Help: "Pending items in extraction queue",
  },
  []string{"tenant_id"},
 )
)

func init() {
 prometheus.MustRegister(WorkerProcessTotal, WorkerProcessDuration, WorkerQueueDepth)
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestSleepCtx`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/workers/helpers.go internal/memory/infrastructure/workers/helpers_test.go internal/memory/infrastructure/workers/metrics.go
git commit -m "feat(memory): add worker helpers and Prometheus metrics

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: ExtractionWorker (Queue Poller)

**Files:**

- Create: `internal/memory/infrastructure/workers/extraction_worker.go`
- Create: `internal/memory/infrastructure/workers/extraction_worker_test.go`

**Interfaces:**

- Consumes: `application.MemoryService.ExtractFacts`, `port.ExtractionQueue.Poll`
- Produces: `ExtractionWorker.Start(ctx)`, `Stop()`

- [ ] **Step 1: Write test**

```go
// extraction_worker_test.go
package workers_test

import (
 "context"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
 "github.com/stretchr/testify/require"
 "go.uber.org/zap"
)

type stubQueue struct {
 items []*domain.ExtractionTask
 calls int
}

func (q *stubQueue) Poll(ctx context.Context, limit int) ([]*domain.ExtractionTask, error) {
 q.calls++
 if q.calls > 1 {
  return nil, nil
 }
 return q.items, nil
}
func (q *stubQueue) Enqueue(ctx context.Context, t *domain.ExtractionTask) error    { return nil }
func (q *stubQueue) MarkDone(ctx context.Context, id string) error                  { return nil }
func (q *stubQueue) MarkFailed(ctx context.Context, id string, reason string) error { return nil }

type stubService struct {
 calls int
}

func (s *stubService) ExtractFacts(ctx context.Context, req *application.ExtractFactsRequest) error {
 s.calls++
 return nil
}

func TestExtractionWorker_ProcessesQueueItems(t *testing.T) {
 q := &stubQueue{
  items: []*domain.ExtractionTask{
   {ID: "t1", TenantID: "tnt", UserID: "u1", AgentID: "a1"},
  },
 }
 svc := &stubService{}
 w := workers.NewExtractionWorker(q, svc, zap.NewNop())

 ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
 defer cancel()
 go w.Start(ctx)
 <-ctx.Done()
 w.Stop()

 require.GreaterOrEqual(t, svc.calls, 1, "should call ExtractFacts at least once")
}
```

- [ ] **Step 2: Verify failure**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestExtractionWorker`
Expected: FAIL

- [ ] **Step 3: Implement**

```go
// extraction_worker.go
package workers

import (
 "context"
 "sync"
 "time"

 "github.com/prometheus/client_golang/prometheus"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type FactExtractor interface {
 ExtractFacts(ctx context.Context, req *application.ExtractFactsRequest) error
}

type ExtractionWorker struct {
 queue    port.ExtractionQueue
 service  FactExtractor
 logger   *zap.Logger
 stopCh   chan struct{}
 stopOnce sync.Once
}

func NewExtractionWorker(q port.ExtractionQueue, svc FactExtractor, logger *zap.Logger) *ExtractionWorker {
 return &ExtractionWorker{queue: q, service: svc, logger: logger, stopCh: make(chan struct{})}
}

func (w *ExtractionWorker) Start(ctx context.Context) {
 backoff := constants.MemoryFetchBackoffBase
 for {
  select {
  case <-ctx.Done():
   return
  case <-w.stopCh:
   return
  default:
  }

  tasks, err := w.queue.Poll(ctx, constants.MemoryExtractionBatchSize)
  if err != nil {
   w.logger.Warn("memory.extraction.poll_failed", zap.Error(err), zap.Duration("backoff", backoff))
   if !SleepCtx(ctx, w.stopCh, backoff) {
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

  if len(tasks) == 0 {
   if !SleepCtx(ctx, w.stopCh, constants.MemoryExtractionIdleSleep) {
    return
   }
   continue
  }

  for _, task := range tasks {
   w.processSafe(ctx, task)
  }
 }
}

func (w *ExtractionWorker) processSafe(ctx context.Context, task *domain.ExtractionTask) {
 defer func() {
  if r := recover(); r != nil {
   w.logger.Error("memory.extraction.panic",
    zap.String("task_id", task.ID), zap.Any("panic", r), zap.Stack("stack"))
   WorkerProcessTotal.With(prometheus.Labels{
    "worker": "extraction", "tenant_id": task.TenantID, "status": "panic",
   }).Inc()
   _ = w.queue.MarkFailed(ctx, task.ID, "panic")
  }
 }()
 start := time.Now()

 req := &application.ExtractFactsRequest{
  TenantID:       task.TenantID,
  UserID:         task.UserID,
  AgentID:        task.AgentID,
  ConversationID: task.ConversationID,
  Messages:       task.Messages,
 }
 if err := w.service.ExtractFacts(ctx, req); err != nil {
  w.logger.Error("memory.extraction.failed",
   zap.String("task_id", task.ID), zap.Error(err))
  WorkerProcessTotal.With(prometheus.Labels{
   "worker": "extraction", "tenant_id": task.TenantID, "status": "error",
  }).Inc()
  _ = w.queue.MarkFailed(ctx, task.ID, err.Error())
  return
 }
 if err := w.queue.MarkDone(ctx, task.ID); err != nil {
  w.logger.Warn("memory.extraction.mark_done_failed",
   zap.String("task_id", task.ID), zap.Error(err))
 }
 WorkerProcessDuration.WithLabelValues("extraction").Observe(time.Since(start).Seconds())
 WorkerProcessTotal.With(prometheus.Labels{
  "worker": "extraction", "tenant_id": task.TenantID, "status": "success",
 }).Inc()
}

func (w *ExtractionWorker) Stop() {
 w.stopOnce.Do(func() { close(w.stopCh) })
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestExtractionWorker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/workers/extraction_worker.go internal/memory/infrastructure/workers/extraction_worker_test.go
git commit -m "feat(memory): add ExtractionWorker queue poller

FOR UPDATE SKIP LOCKED concurrent-safe; panic isolation per task

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: SupersedeWorker (Cron-Driven)

**Files:**

- Create: `internal/memory/infrastructure/workers/supersede_worker.go`
- Create: `internal/memory/infrastructure/workers/supersede_worker_test.go`

**Interfaces:**

- Consumes: `port.FactRepo.FindRecentForSupersede`, `port.LLMSuperseder.JudgeSupersede`
- Produces: `SupersedeWorker.Start(ctx)`, `Stop()` — runs every 5 min via cron

- [ ] **Step 1: Write test**

```go
func TestSupersedeWorker_RunCycle(t *testing.T) {
 repo := &stubFactRepo{
  recent: []*domain.MemoryFact{
   {ID: "f1", Content: "user prefers dark mode", Status: "active"},
   {ID: "f2", Content: "user prefers light mode", Status: "active"},
  },
 }
 judge := &stubSuperseder{decision: "SUPERSEDE", target: "f1"}
 w := workers.NewSupersedeWorker(repo, judge, zap.NewNop())
 w.RunOnce(context.Background())
 require.Equal(t, 1, judge.calls)
 require.Equal(t, "superseded", repo.facts["f1"].Status)
}
```

- [ ] **Step 2: Verify failure**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestSupersedeWorker`
Expected: FAIL

- [ ] **Step 3: Implement**

```go
// supersede_worker.go
package workers

import (
 "context"
 "sync"
 "time"

 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type SupersedeWorker struct {
 factRepo port.FactRepo
 judge    port.LLMSuperseder
 logger   *zap.Logger
 stopCh   chan struct{}
 stopOnce sync.Once
}

func NewSupersedeWorker(repo port.FactRepo, judge port.LLMSuperseder, logger *zap.Logger) *SupersedeWorker {
 return &SupersedeWorker{factRepo: repo, judge: judge, logger: logger, stopCh: make(chan struct{})}
}

func (w *SupersedeWorker) Start(ctx context.Context) {
 ticker := time.NewTicker(constants.MemorySupersedeInterval)
 defer ticker.Stop()
 w.RunOnce(ctx)
 for {
  select {
  case <-ctx.Done():
   return
  case <-w.stopCh:
   return
  case <-ticker.C:
   w.RunOnce(ctx)
  }
 }
}

func (w *SupersedeWorker) RunOnce(ctx context.Context) {
 defer func() {
  if r := recover(); r != nil {
   w.logger.Error("memory.supersede.panic", zap.Any("panic", r), zap.Stack("stack"))
  }
 }()

 candidates, err := w.factRepo.FindSupersedeCandidates(ctx, constants.MemorySupersedeBatchSize)
 if err != nil {
  w.logger.Error("memory.supersede.find_candidates", zap.Error(err))
  return
 }

 for _, pair := range candidates {
  decision, err := w.judge.JudgeSupersede(ctx, pair.OldFact, pair.NewFact)
  if err != nil {
   w.logger.Warn("memory.supersede.judge_failed",
    zap.String("old", pair.OldFact.ID), zap.String("new", pair.NewFact.ID), zap.Error(err))
   continue
  }
  if decision == "SUPERSEDE" {
   if err := pair.OldFact.MarkSuperseded(pair.NewFact.ID); err != nil {
    continue
   }
   if err := w.factRepo.Update(ctx, pair.OldFact); err != nil {
    w.logger.Error("memory.supersede.update_failed",
     zap.String("fact_id", pair.OldFact.ID), zap.Error(err))
   }
  }
 }
}

func (w *SupersedeWorker) Stop() {
 w.stopOnce.Do(func() { close(w.stopCh) })
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestSupersedeWorker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/workers/supersede_worker.go internal/memory/infrastructure/workers/supersede_worker_test.go
git commit -m "feat(memory): add SupersedeWorker with LLM KEEP/SUPERSEDE judgment

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: EmbedWorker (New Fact Embedder)

**Files:**

- Create: `internal/memory/infrastructure/workers/embed_worker.go`
- Create: `internal/memory/infrastructure/workers/embed_worker_test.go`

**Interfaces:**

- Consumes: `port.FactRepo.FindUnembedded`, `port.EmbedClient.EmbedVector`, `port.VectorStore.Upsert`
- Produces: `EmbedWorker.Start(ctx)`, `Stop()`

- [ ] **Step 1: Write test**

```go
func TestEmbedWorker_EmbedsAndUpserts(t *testing.T) {
 repo := &stubFactRepo{
  unembedded: []*domain.MemoryFact{
   {ID: "f1", TenantID: "tnt", Content: "fact content"},
  },
 }
 embed := &stubEmbedClient{vector: []float32{0.1, 0.2, 0.3}}
 store := &stubVectorStore{}
 w := workers.NewEmbedWorker(repo, embed, store, zap.NewNop())
 w.RunOnce(context.Background())
 require.Equal(t, 1, store.upsertCalls)
 require.Equal(t, []float32{0.1, 0.2, 0.3}, store.lastVector)
}
```

- [ ] **Step 2: Verify failure**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestEmbedWorker`
Expected: FAIL

- [ ] **Step 3: Implement**

```go
// embed_worker.go
package workers

import (
 "context"
 "sync"
 "time"

 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type EmbedWorker struct {
 factRepo port.FactRepo
 embed    port.EmbedClient
 store    port.VectorStore
 logger   *zap.Logger
 stopCh   chan struct{}
 stopOnce sync.Once
}

func NewEmbedWorker(repo port.FactRepo, embed port.EmbedClient, store port.VectorStore, logger *zap.Logger) *EmbedWorker {
 return &EmbedWorker{factRepo: repo, embed: embed, store: store, logger: logger, stopCh: make(chan struct{})}
}

func (w *EmbedWorker) Start(ctx context.Context) {
 ticker := time.NewTicker(constants.MemoryEmbedInterval)
 defer ticker.Stop()
 w.RunOnce(ctx)
 for {
  select {
  case <-ctx.Done():
   return
  case <-w.stopCh:
   return
  case <-ticker.C:
   w.RunOnce(ctx)
  }
 }
}

func (w *EmbedWorker) RunOnce(ctx context.Context) {
 defer func() {
  if r := recover(); r != nil {
   w.logger.Error("memory.embed_worker.panic", zap.Any("panic", r), zap.Stack("stack"))
  }
 }()

 facts, err := w.factRepo.FindUnembedded(ctx, constants.MemoryEmbedBatchSize)
 if err != nil {
  w.logger.Error("memory.embed_worker.find_failed", zap.Error(err))
  return
 }

 for _, fact := range facts {
  vec, err := w.embed.EmbedVector(ctx, fact.Content)
  if err != nil {
   w.logger.Warn("memory.embed_worker.embed_failed",
    zap.String("fact_id", fact.ID), zap.Error(err))
   continue
  }
  meta := map[string]any{
   "user_id":  fact.UserID,
   "agent_id": fact.AgentID,
   "scope":    string(fact.Scope),
  }
  if err := w.store.Upsert(ctx, fact.TenantID, fact.UserID, fact.ID, vec, meta); err != nil {
   w.logger.Error("memory.embed_worker.upsert_failed",
    zap.String("fact_id", fact.ID), zap.Error(err))
   continue
  }
  fact.MarkEmbedded()
  if err := w.factRepo.Update(ctx, fact); err != nil {
   w.logger.Error("memory.embed_worker.mark_embedded_failed",
    zap.String("fact_id", fact.ID), zap.Error(err))
  }
 }
}

func (w *EmbedWorker) Stop() {
 w.stopOnce.Do(func() { close(w.stopCh) })
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestEmbedWorker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/workers/embed_worker.go internal/memory/infrastructure/workers/embed_worker_test.go
git commit -m "feat(memory): add EmbedWorker for new fact embeddings

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: ProfileWorker (Entity Profile Rebuilder)

**Files:**

- Create: `internal/memory/infrastructure/workers/profile_worker.go`
- Create: `internal/memory/infrastructure/workers/profile_worker_test.go`

**Interfaces:**

- Consumes: `port.EntityRepo.FindRebuildCandidates`, `port.LLMProfiler.BuildProfile`, `port.FactRepo.FindByEntity`
- Produces: `ProfileWorker.Start(ctx)`, `Stop()` — runs every hour

- [ ] **Step 1: Write test**

```go
func TestProfileWorker_RebuildsProfile(t *testing.T) {
 entity := &domain.MemoryEntity{
  ID: "e1", Name: "Alice", FactCount: 10, FactCountSinceRebuild: 5,
  LastProfileRebuildAt: time.Now().AddDate(0, 0, -8),
 }
 entityRepo := &stubEntityRepo{rebuildCandidates: []*domain.MemoryEntity{entity}}
 factRepo := &stubFactRepo{
  entityFacts: []*domain.MemoryFact{
   {Content: "Alice likes coffee"},
   {Content: "Alice works at Acme"},
  },
 }
 profiler := &stubProfiler{profile: "Alice prefers coffee, works at Acme."}
 w := workers.NewProfileWorker(entityRepo, factRepo, profiler, zap.NewNop())
 w.RunOnce(context.Background())
 require.Equal(t, "Alice prefers coffee, works at Acme.", entityRepo.entities["e1"].Profile)
}
```

- [ ] **Step 2: Verify failure**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestProfileWorker`
Expected: FAIL

- [ ] **Step 3: Implement**

```go
// profile_worker.go
package workers

import (
 "context"
 "sync"
 "time"

 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type ProfileWorker struct {
 entityRepo port.EntityRepo
 factRepo   port.FactRepo
 profiler   port.LLMProfiler
 logger     *zap.Logger
 stopCh     chan struct{}
 stopOnce   sync.Once
}

func NewProfileWorker(er port.EntityRepo, fr port.FactRepo, p port.LLMProfiler, logger *zap.Logger) *ProfileWorker {
 return &ProfileWorker{entityRepo: er, factRepo: fr, profiler: p, logger: logger, stopCh: make(chan struct{})}
}

func (w *ProfileWorker) Start(ctx context.Context) {
 ticker := time.NewTicker(constants.MemoryProfileRebuildInterval)
 defer ticker.Stop()
 w.RunOnce(ctx)
 for {
  select {
  case <-ctx.Done():
   return
  case <-w.stopCh:
   return
  case <-ticker.C:
   w.RunOnce(ctx)
  }
 }
}

func (w *ProfileWorker) RunOnce(ctx context.Context) {
 defer func() {
  if r := recover(); r != nil {
   w.logger.Error("memory.profile.panic", zap.Any("panic", r), zap.Stack("stack"))
  }
 }()

 entities, err := w.entityRepo.FindRebuildCandidates(ctx, constants.MemoryProfileBatchSize)
 if err != nil {
  w.logger.Error("memory.profile.find_failed", zap.Error(err))
  return
 }

 for _, entity := range entities {
  facts, err := w.factRepo.FindByEntity(ctx, entity.UserID, entity.AgentID, entity.Name, constants.MemoryProfileFactLimit)
  if err != nil {
   w.logger.Warn("memory.profile.find_facts_failed",
    zap.String("entity_id", entity.ID), zap.Error(err))
   continue
  }
  profile, err := w.profiler.BuildProfile(ctx, entity, facts)
  if err != nil {
   w.logger.Warn("memory.profile.build_failed",
    zap.String("entity_id", entity.ID), zap.Error(err))
   continue
  }
  entity.Profile = profile
  entity.LastProfileRebuildAt = time.Now()
  entity.FactCountSinceRebuild = 0
  if err := w.entityRepo.Update(ctx, entity); err != nil {
   w.logger.Error("memory.profile.update_failed",
    zap.String("entity_id", entity.ID), zap.Error(err))
  }
 }
}

func (w *ProfileWorker) Stop() {
 w.stopOnce.Do(func() { close(w.stopCh) })
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestProfileWorker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/workers/profile_worker.go internal/memory/infrastructure/workers/profile_worker_test.go
git commit -m "feat(memory): add ProfileWorker for entity profile rebuild

Triggers: 7-day stale OR 5-fact delta

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 6: GCWorker (Soft Delete Cleanup)

**Files:**

- Create: `internal/memory/infrastructure/workers/gc_worker.go`
- Create: `internal/memory/infrastructure/workers/gc_worker_test.go`

**Interfaces:**

- Consumes: `port.FactRepo.PurgeExpired`, `port.VectorStore.Delete`
- Produces: `GCWorker.Start(ctx)`, `Stop()` — runs daily at 03:00

- [ ] **Step 1: Write test**

```go
func TestGCWorker_PurgesExpiredFacts(t *testing.T) {
 now := time.Now()
 expired := &domain.MemoryFact{ID: "f1", TenantID: "tnt", Status: "deleted", DeletedAt: now.AddDate(0, 0, -31)}
 repo := &stubFactRepo{expired: []*domain.MemoryFact{expired}}
 store := &stubVectorStore{}
 w := workers.NewGCWorker(repo, store, zap.NewNop())
 w.RunOnce(context.Background())
 require.Equal(t, 1, repo.purgeCalls)
 require.Equal(t, []string{"f1"}, store.deletedIDs["tnt"])
}
```

- [ ] **Step 2: Verify failure**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestGCWorker`
Expected: FAIL

- [ ] **Step 3: Implement**

```go
// gc_worker.go
package workers

import (
 "context"
 "sync"
 "time"

 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type GCWorker struct {
 factRepo port.FactRepo
 store    port.VectorStore
 logger   *zap.Logger
 stopCh   chan struct{}
 stopOnce sync.Once
}

func NewGCWorker(repo port.FactRepo, store port.VectorStore, logger *zap.Logger) *GCWorker {
 return &GCWorker{factRepo: repo, store: store, logger: logger, stopCh: make(chan struct{})}
}

func (w *GCWorker) Start(ctx context.Context) {
 ticker := time.NewTicker(constants.MemoryGCInterval)
 defer ticker.Stop()
 w.RunOnce(ctx)
 for {
  select {
  case <-ctx.Done():
   return
  case <-w.stopCh:
   return
  case <-ticker.C:
   w.RunOnce(ctx)
  }
 }
}

func (w *GCWorker) RunOnce(ctx context.Context) {
 defer func() {
  if r := recover(); r != nil {
   w.logger.Error("memory.gc.panic", zap.Any("panic", r), zap.Stack("stack"))
  }
 }()

 deletedRetention := constants.MemoryDeletedRetention
 supersededRetention := constants.MemorySupersededRetention

 purgeBatch, err := w.factRepo.FindExpired(ctx, deletedRetention, supersededRetention, constants.MemoryGCBatchSize)
 if err != nil {
  w.logger.Error("memory.gc.find_expired_failed", zap.Error(err))
  return
 }

 byTenant := map[string][]string{}
 ids := make([]string, 0, len(purgeBatch))
 for _, f := range purgeBatch {
  byTenant[f.TenantID] = append(byTenant[f.TenantID], f.ID)
  ids = append(ids, f.ID)
 }

 for tenantID, factIDs := range byTenant {
  if err := w.store.DeleteBatch(ctx, tenantID, factIDs); err != nil {
   w.logger.Warn("memory.gc.vector_delete_failed",
    zap.String("tenant_id", tenantID), zap.Int("count", len(factIDs)), zap.Error(err))
  }
 }

 if err := w.factRepo.PurgeByIDs(ctx, ids); err != nil {
  w.logger.Error("memory.gc.purge_failed", zap.Error(err))
  return
 }

 w.logger.Info("memory.gc.completed",
  zap.Int("purged_facts", len(ids)),
  zap.Int("tenant_count", len(byTenant)),
  zap.Time("ran_at", time.Now()))
}

func (w *GCWorker) Stop() {
 w.stopOnce.Do(func() { close(w.stopCh) })
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -v ./internal/memory/infrastructure/workers/... -run TestGCWorker`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/workers/gc_worker.go internal/memory/infrastructure/workers/gc_worker_test.go
git commit -m "feat(memory): add GCWorker for soft-delete cleanup

Purges deleted (30d) + superseded/archived (90d) facts and Milvus vectors

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 7: Wire Workers in Container

**Files:**

- Create: `api/wiring/memory.go`
- Modify: `api/wiring/container.go` (add MemoryWorkers field)

**Interfaces:**

- Consumes: All workers + MemoryService
- Produces: `BuildMemoryWorkers(deps) []HarnessComponent`

- [ ] **Step 1: Write wiring test**

```go
// api/wiring/memory_test.go
func TestBuildMemoryWorkers_StartsAll(t *testing.T) {
 deps := newTestDeps(t)
 workers := wiring.BuildMemoryWorkers(deps)
 require.Len(t, workers, 5, "should wire 5 workers: extraction, supersede, embed, profile, gc")
 for _, w := range workers {
  require.NotNil(t, w)
 }
}
```

- [ ] **Step 2: Verify failure**

Run: `go test -v ./api/wiring/... -run TestBuildMemoryWorkers`
Expected: FAIL

- [ ] **Step 3: Implement wiring**

```go
// api/wiring/memory.go
package wiring

import (
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
)

type MemoryDeps struct {
 Service     *application.MemoryService
 FactRepo    port.FactRepo
 EntityRepo  port.EntityRepo
 Queue       port.ExtractionQueue
 VectorStore port.VectorStore
 EmbedClient port.EmbedClient
 Superseder  port.LLMSuperseder
 Profiler    port.LLMProfiler
 Logger      *zap.Logger
}

type WorkerComponent interface {
 Start(ctx context.Context)
 Stop()
}

func BuildMemoryWorkers(deps MemoryDeps) []WorkerComponent {
 return []WorkerComponent{
  workers.NewExtractionWorker(deps.Queue, deps.Service, deps.Logger),
  workers.NewSupersedeWorker(deps.FactRepo, deps.Superseder, deps.Logger),
  workers.NewEmbedWorker(deps.FactRepo, deps.EmbedClient, deps.VectorStore, deps.Logger),
  workers.NewProfileWorker(deps.EntityRepo, deps.FactRepo, deps.Profiler, deps.Logger),
  workers.NewGCWorker(deps.FactRepo, deps.VectorStore, deps.Logger),
 }
}
```

- [ ] **Step 4: Verify pass**

Run: `go test -v ./api/wiring/... -run TestBuildMemoryWorkers`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/wiring/memory.go api/wiring/memory_test.go
git commit -m "feat(memory): wire 5 memory v2 workers into Container

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Complete

Workers plan finished. Workers wired through Container, started in order, stopped reverse via Harness.
