# Memory Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build an async memory pipeline that processes chat messages from ChatStore through NATS JetStream for embedding, entity extraction, importance scoring, and conversation summarization.

**Architecture:** Outbox pattern writes to `memory_outbox` in the same PG transaction as `chat_messages`. A poller publishes to JetStream. Stage 1 (Embedder) generates vectors and stores in Milvus. Stage 2 (Enricher) calls LLM for entity extraction, importance scoring, and triggers conversation summaries at a token budget threshold (~4096).

**Tech Stack:** Go 1.22 · NATS JetStream · PostgreSQL (per-tenant schema) · Milvus v2.4.2 · LLMGateway (structured output) · Zap · Prometheus

---

## File Structure

```
internal/memory/pipeline/
├── config.go           # PipelineConfig struct, loaded from Viper
├── events.go           # MemoryRawEvent, MemoryEnrichedEvent serialization
├── jetstream.go        # JetStream stream/consumer setup (EnsureStreams)
├── outbox_poller.go    # OutboxPoller: tick → SELECT FOR UPDATE → publish → DELETE
├── embedder.go         # EmbedderWorker: consume MEMORY_RAW → embed → Milvus → publish enriched
├── enricher.go         # EnricherWorker: consume MEMORY_ENRICHED → LLM → PG write
├── enricher_prompt.go  # Enrichment and summarization prompt templates
├── metrics.go          # Prometheus collectors for the pipeline
├── pipeline.go         # Pipeline orchestrator: Start/Stop all workers
└── pipeline_test.go    # Integration-level tests

internal/agent/chat_store.go  # MODIFY: AddMessage writes to memory_outbox in same tx
internal/migration/sql/
├── 008_memory_pipeline.up.sql    # New tables: memory_outbox, memory_summaries, memory_token_budgets + alter memory_entries
├── 008_memory_pipeline.down.sql
internal/migration/sql/tenant_schema.sql  # MODIFY: append new tables for fresh tenants
cmd/server/main.go              # MODIFY: register pipeline as Harness component
pkg/constants/memory.go         # Pipeline constants (timeouts, batch sizes, thresholds)
```

---

## Task 1: Constants & Pipeline Config

**Files:**

- Create: `pkg/constants/memory.go`
- Create: `internal/memory/pipeline/config.go`

- [ ] **Step 1: Create memory constants file**

```go
// pkg/constants/memory.go
package constants

import "time"

// Outbox poller
const (
 MemoryOutboxPollInterval = 1 * time.Second
 MemoryOutboxBatchSize    = 50
)

// JetStream
const (
 MemoryStreamMaxAge       = 72 * time.Hour
 MemoryDLQMaxAge          = 168 * time.Hour
 MemoryRawStream          = "MEMORY_RAW"
 MemoryEnrichedStream     = "MEMORY_ENRICHED"
 MemoryDLQStream          = "MEMORY_DLQ"
 MemoryRawSubject         = "memory.raw"
 MemoryEnrichedSubject    = "memory.enriched"
 MemoryDLQSubject         = "memory.dlq"
)

// Embedder
const (
 EmbedderConsumerName = "embed-worker"
 EmbedderAckWait      = 30 * time.Second
 EmbedderMaxDeliver   = 5
 EmbedderWorkerCount  = 2
)

// Enricher
const (
 EnricherConsumerName        = "enrich-worker"
 EnricherAckWait             = 60 * time.Second
 EnricherMaxDeliver          = 5
 EnricherWorkerCount         = 1
 EnricherSummaryTokenThreshold = 4096
 EnricherMaxInjectionTokens  = 500
 EnricherTopEntities         = 10
)
```

- [ ] **Step 2: Create pipeline config struct**

```go
// internal/memory/pipeline/config.go
package pipeline

import (
 "time"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

type Config struct {
 Enabled       bool          `mapstructure:"enabled"`
 NatsURL       string        `mapstructure:"nats_url"`
 PollInterval  time.Duration `mapstructure:"poll_interval"`
 BatchSize     int           `mapstructure:"batch_size"`
 EmbedWorkers  int           `mapstructure:"embed_workers"`
 EnrichWorkers int           `mapstructure:"enrich_workers"`
 EmbedAckWait  time.Duration `mapstructure:"embed_ack_wait"`
 EnrichAckWait time.Duration `mapstructure:"enrich_ack_wait"`
 MaxDeliver    int           `mapstructure:"max_deliver"`
 EnrichModel   string        `mapstructure:"enrich_model"`
 SummaryModel  string        `mapstructure:"summary_model"`
 SummaryTokenThreshold int   `mapstructure:"summary_token_threshold"`
}

func DefaultConfig() Config {
 return Config{
  Enabled:       false,
  NatsURL:       "nats://localhost:4222",
  PollInterval:  constants.MemoryOutboxPollInterval,
  BatchSize:     constants.MemoryOutboxBatchSize,
  EmbedWorkers:  constants.EmbedderWorkerCount,
  EnrichWorkers: constants.EnricherWorkerCount,
  EmbedAckWait:  constants.EmbedderAckWait,
  EnrichAckWait: constants.EnricherAckWait,
  MaxDeliver:    constants.EmbedderMaxDeliver,
  EnrichModel:   "gpt-4o-mini",
  SummaryModel:  "gpt-4o-mini",
  SummaryTokenThreshold: constants.EnricherSummaryTokenThreshold,
 }
}
```

- [ ] **Step 3: Verify compilation**

Run: `cd /home/yang/go-projects/stratum && go build ./pkg/constants/... ./internal/memory/pipeline/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add pkg/constants/memory.go internal/memory/pipeline/config.go
git commit -m "feat(memory-pipeline): add constants and config struct for async pipeline"
```

---

## Task 2: Database Migration

**Files:**

- Create: `internal/migration/sql/008_memory_pipeline.up.sql`
- Create: `internal/migration/sql/008_memory_pipeline.down.sql`
- Modify: `internal/migration/sql/tenant_schema.sql`

- [ ] **Step 1: Create up migration**

```sql
-- internal/migration/sql/008_memory_pipeline.up.sql
-- Memory pipeline tables (per-tenant schema, applied via tenant provisioning)
-- This migration is a marker; actual DDL is in tenant_schema.sql.
-- For existing tenants, run this DDL against each tenant schema.

-- Outbox for reliable message publishing
CREATE TABLE IF NOT EXISTS memory_outbox (
    id          BIGSERIAL PRIMARY KEY,
    message_id  TEXT NOT NULL,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_outbox_created ON memory_outbox (created_at);

-- Conversation summaries
CREATE TABLE IF NOT EXISTS memory_summaries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    summary         TEXT NOT NULL,
    covered_until   TIMESTAMPTZ NOT NULL,
    token_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_summaries_conv ON memory_summaries (conversation_id, created_at DESC);

-- Token budget tracking per conversation
CREATE TABLE IF NOT EXISTS memory_token_budgets (
    conversation_id UUID PRIMARY KEY REFERENCES chat_conversations(id) ON DELETE CASCADE,
    accumulated     INT NOT NULL DEFAULT 0,
    last_reset_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Extend memory_entries with pipeline fields
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS keywords TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS token_estimate INT NOT NULL DEFAULT 0;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS scope_layer INT NOT NULL DEFAULT 1;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS enriched_at TIMESTAMPTZ;

-- Extend entities with scoping fields
ALTER TABLE entities ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS agent_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS confidence FLOAT8 NOT NULL DEFAULT 0;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS scope_layer INT NOT NULL DEFAULT 1;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS occurrence_count INT NOT NULL DEFAULT 1;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_entities_scope ON entities (user_id, agent_id, scope_layer);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_name_type ON entities (user_id, COALESCE(agent_id, ''), name, type);
```

- [ ] **Step 2: Create down migration**

```sql
-- internal/migration/sql/008_memory_pipeline.down.sql
DROP INDEX IF EXISTS idx_entities_name_type;
DROP INDEX IF EXISTS idx_entities_scope;
ALTER TABLE entities DROP COLUMN IF EXISTS last_seen;
ALTER TABLE entities DROP COLUMN IF EXISTS occurrence_count;
ALTER TABLE entities DROP COLUMN IF EXISTS scope_layer;
ALTER TABLE entities DROP COLUMN IF EXISTS confidence;
ALTER TABLE entities DROP COLUMN IF EXISTS agent_id;
ALTER TABLE entities DROP COLUMN IF EXISTS user_id;

ALTER TABLE memory_entries DROP COLUMN IF EXISTS enriched_at;
ALTER TABLE memory_entries DROP COLUMN IF EXISTS scope_layer;
ALTER TABLE memory_entries DROP COLUMN IF EXISTS token_estimate;
ALTER TABLE memory_entries DROP COLUMN IF EXISTS keywords;
ALTER TABLE memory_entries DROP COLUMN IF EXISTS conversation_id;

DROP TABLE IF EXISTS memory_token_budgets;
DROP TABLE IF EXISTS memory_summaries;
DROP TABLE IF EXISTS memory_outbox;
```

- [ ] **Step 3: Append new tables to tenant_schema.sql**

Add after the `agent_executions` index (line ~247):

```sql
-- Memory pipeline tables
CREATE TABLE IF NOT EXISTS memory_outbox (
    id          BIGSERIAL PRIMARY KEY,
    message_id  TEXT NOT NULL,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_outbox_created ON memory_outbox (created_at);

CREATE TABLE IF NOT EXISTS memory_summaries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat_conversations(id) ON DELETE CASCADE,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    summary         TEXT NOT NULL,
    covered_until   TIMESTAMPTZ NOT NULL,
    token_count     INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_memory_summaries_conv ON memory_summaries (conversation_id, created_at DESC);

CREATE TABLE IF NOT EXISTS memory_token_budgets (
    conversation_id UUID PRIMARY KEY REFERENCES chat_conversations(id) ON DELETE CASCADE,
    accumulated     INT NOT NULL DEFAULT 0,
    last_reset_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS conversation_id UUID REFERENCES chat_conversations(id) ON DELETE SET NULL;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS keywords TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS token_estimate INT NOT NULL DEFAULT 0;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS scope_layer INT NOT NULL DEFAULT 1;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS enriched_at TIMESTAMPTZ;

ALTER TABLE entities ADD COLUMN IF NOT EXISTS user_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS agent_id TEXT;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS confidence FLOAT8 NOT NULL DEFAULT 0;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS scope_layer INT NOT NULL DEFAULT 1;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS occurrence_count INT NOT NULL DEFAULT 1;
ALTER TABLE entities ADD COLUMN IF NOT EXISTS last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW();
CREATE INDEX IF NOT EXISTS idx_entities_scope ON entities (user_id, agent_id, scope_layer);
CREATE UNIQUE INDEX IF NOT EXISTS idx_entities_name_type ON entities (user_id, COALESCE(agent_id, ''), name, type);
```

- [ ] **Step 4: Commit**

```bash
git add internal/migration/sql/008_memory_pipeline.up.sql internal/migration/sql/008_memory_pipeline.down.sql internal/migration/sql/tenant_schema.sql
git commit -m "feat(memory-pipeline): add migration for outbox, summaries, token budgets, and entity scoping"
```

---

## Task 3: Event Types

**Files:**

- Create: `internal/memory/pipeline/events.go`

- [ ] **Step 1: Define event structs**

```go
// internal/memory/pipeline/events.go
package pipeline

import (
 "encoding/json"
 "time"
)

type MemoryRawEvent struct {
 MessageID      string    `json:"message_id"`
 ConversationID string    `json:"conversation_id"`
 TenantID       string    `json:"tenant_id"`
 UserID         string    `json:"user_id"`
 AgentID        string    `json:"agent_id"`
 Role           string    `json:"role"`
 Content        string    `json:"content"`
 CreatedAt      time.Time `json:"created_at"`
}

func (e *MemoryRawEvent) Marshal() ([]byte, error) {
 return json.Marshal(e)
}

func UnmarshalRawEvent(data []byte) (*MemoryRawEvent, error) {
 var ev MemoryRawEvent
 if err := json.Unmarshal(data, &ev); err != nil {
  return nil, err
 }
 return &ev, nil
}

type MemoryEnrichedEvent struct {
 MemoryRawEvent
 VectorID string `json:"vector_id"`
}

func (e *MemoryEnrichedEvent) Marshal() ([]byte, error) {
 return json.Marshal(e)
}

func UnmarshalEnrichedEvent(data []byte) (*MemoryEnrichedEvent, error) {
 var ev MemoryEnrichedEvent
 if err := json.Unmarshal(data, &ev); err != nil {
  return nil, err
 }
 return &ev, nil
}
```

- [ ] **Step 2: Write test for serialization round-trip**

```go
// internal/memory/pipeline/events_test.go
package pipeline

import (
 "testing"
 "time"

 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
)

func TestMemoryRawEvent_RoundTrip(t *testing.T) {
 ev := &MemoryRawEvent{
  MessageID:      "msg-123",
  ConversationID: "conv-456",
  TenantID:       "tenant-1",
  UserID:         "user-1",
  AgentID:        "agent-1",
  Role:           "user",
  Content:        "Hello world",
  CreatedAt:      time.Now().Truncate(time.Millisecond),
 }
 data, err := ev.Marshal()
 require.NoError(t, err)

 got, err := UnmarshalRawEvent(data)
 require.NoError(t, err)
 assert.Equal(t, ev, got)
}

func TestMemoryEnrichedEvent_RoundTrip(t *testing.T) {
 ev := &MemoryEnrichedEvent{
  MemoryRawEvent: MemoryRawEvent{
   MessageID:      "msg-789",
   ConversationID: "conv-abc",
   TenantID:       "tenant-2",
   UserID:         "user-2",
   AgentID:        "agent-2",
   Role:           "agent",
   Content:        "Response text",
   CreatedAt:      time.Now().Truncate(time.Millisecond),
  },
  VectorID: "vec-001",
 }
 data, err := ev.Marshal()
 require.NoError(t, err)

 got, err := UnmarshalEnrichedEvent(data)
 require.NoError(t, err)
 assert.Equal(t, ev, got)
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/memory/pipeline/... -v -run RoundTrip`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/memory/pipeline/events.go internal/memory/pipeline/events_test.go
git commit -m "feat(memory-pipeline): define MemoryRawEvent and MemoryEnrichedEvent types"
```

---

## Task 4: JetStream Setup

**Files:**

- Create: `internal/memory/pipeline/jetstream.go`

- [ ] **Step 1: Write JetStream stream provisioning**

```go
// internal/memory/pipeline/jetstream.go
package pipeline

import (
 "context"
 "fmt"
 "time"

 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/nats-io/nats.go"
 "github.com/nats-io/nats.go/jetstream"
 "go.uber.org/zap"
)

type JetStreamManager struct {
 js     jetstream.JetStream
 logger *zap.Logger
}

func NewJetStreamManager(nc *nats.Conn, logger *zap.Logger) (*JetStreamManager, error) {
 js, err := jetstream.New(nc)
 if err != nil {
  return nil, fmt.Errorf("jetstream.New: %w", err)
 }
 return &JetStreamManager{js: js, logger: logger}, nil
}

func (m *JetStreamManager) EnsureStreams(ctx context.Context) error {
 streams := []jetstream.StreamConfig{
  {
   Name:      constants.MemoryRawStream,
   Subjects:  []string{constants.MemoryRawSubject + ".>"},
   Retention: jetstream.WorkQueuePolicy,
   MaxAge:    constants.MemoryStreamMaxAge,
   Storage:   jetstream.FileStorage,
  },
  {
   Name:      constants.MemoryEnrichedStream,
   Subjects:  []string{constants.MemoryEnrichedSubject + ".>"},
   Retention: jetstream.WorkQueuePolicy,
   MaxAge:    constants.MemoryStreamMaxAge,
   Storage:   jetstream.FileStorage,
  },
  {
   Name:      constants.MemoryDLQStream,
   Subjects:  []string{constants.MemoryDLQSubject + ".>"},
   Retention: jetstream.LimitsPolicy,
   MaxAge:    constants.MemoryDLQMaxAge,
   Storage:   jetstream.FileStorage,
  },
 }

 for _, cfg := range streams {
  _, err := m.js.CreateOrUpdateStream(ctx, cfg)
  if err != nil {
   return fmt.Errorf("ensure stream %s: %w", cfg.Name, err)
  }
  m.logger.Info("jetstream stream ensured", zap.String("stream", cfg.Name))
 }
 return nil
}

func (m *JetStreamManager) JS() jetstream.JetStream {
 return m.js
}

func (m *JetStreamManager) CreateConsumer(ctx context.Context, stream, name, filterSubject string, ackWait time.Duration, maxDeliver int) (jetstream.Consumer, error) {
 consumer, err := m.js.CreateOrUpdateConsumer(ctx, stream, jetstream.ConsumerConfig{
  Durable:       name,
  AckWait:       ackWait,
  MaxDeliver:    maxDeliver,
  FilterSubject: filterSubject,
  AckPolicy:     jetstream.AckExplicitPolicy,
 })
 if err != nil {
  return nil, fmt.Errorf("create consumer %s on %s: %w", name, stream, err)
 }
 return consumer, nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/memory/pipeline/...`
Expected: no errors (may need `go get github.com/nats-io/nats.go/jetstream`)

- [ ] **Step 3: Add nats.go/jetstream dependency if needed**

Run: `go get github.com/nats-io/nats.go/jetstream@latest`

- [ ] **Step 4: Commit**

```bash
git add internal/memory/pipeline/jetstream.go go.mod go.sum
git commit -m "feat(memory-pipeline): add JetStream stream/consumer manager"
```

---

## Task 5: Outbox Poller

**Files:**

- Create: `internal/memory/pipeline/outbox_poller.go`
- Modify: `internal/agent/chat_store.go` (add outbox write to AddMessage tx)

- [ ] **Step 1: Write OutboxPoller**

```go
// internal/memory/pipeline/outbox_poller.go
package pipeline

import (
 "context"
 "encoding/json"
 "fmt"
 "time"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/nats-io/nats.go/jetstream"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/byteBuilderX/stratum/pkg/tenantdb"
)

type OutboxPoller struct {
 pool     *pgxpool.Pool
 js       jetstream.JetStream
 logger   *zap.Logger
 interval time.Duration
 batch    int
 stopCh   chan struct{}
}

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
 }
}

func (p *OutboxPoller) Start(ctx context.Context) {
 ticker := time.NewTicker(p.interval)
 defer ticker.Stop()

 for {
  select {
  case <-ctx.Done():
   return
  case <-p.stopCh:
   return
  case <-ticker.C:
   if err := p.poll(ctx); err != nil {
    p.logger.Error("memory.outbox.poll", zap.Error(err))
   }
  }
 }
}

func (p *OutboxPoller) Stop() {
 close(p.stopCh)
}

func (p *OutboxPoller) poll(ctx context.Context) error {
 tenants, err := tenantdb.ListTenantSchemas(ctx, p.pool)
 if err != nil {
  return fmt.Errorf("list tenant schemas: %w", err)
 }

 for _, schema := range tenants {
  if err := p.pollTenant(ctx, schema); err != nil {
   p.logger.Warn("memory.outbox.poll_tenant",
    zap.String("schema", schema), zap.Error(err))
  }
 }
 return nil
}

func (p *OutboxPoller) pollTenant(ctx context.Context, schema string) error {
 tx, err := p.pool.Begin(ctx)
 if err != nil {
  return fmt.Errorf("begin tx: %w", err)
 }
 defer tx.Rollback(ctx) //nolint:errcheck

 if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
  return fmt.Errorf("set schema: %w", err)
 }

 rows, err := tx.Query(ctx,
  "SELECT id, payload FROM memory_outbox ORDER BY id LIMIT $1 FOR UPDATE SKIP LOCKED",
  p.batch)
 if err != nil {
  return fmt.Errorf("select outbox: %w", err)
 }
 defer rows.Close()

 var ids []int64
 for rows.Next() {
  var id int64
  var payload json.RawMessage
  if err := rows.Scan(&id, &payload); err != nil {
   return fmt.Errorf("scan row: %w", err)
  }

  var ev MemoryRawEvent
  if err := json.Unmarshal(payload, &ev); err != nil {
   p.logger.Warn("memory.outbox.unmarshal", zap.Int64("id", id), zap.Error(err))
   ids = append(ids, id)
   continue
  }

  subject := fmt.Sprintf("%s.%s", constants.MemoryRawSubject, ev.TenantID)
  if _, err := p.js.Publish(ctx, subject, payload); err != nil {
   return fmt.Errorf("publish id=%d: %w", id, err)
  }
  ids = append(ids, id)
 }
 if err := rows.Err(); err != nil {
  return fmt.Errorf("rows iteration: %w", err)
 }

 if len(ids) == 0 {
  return tx.Commit(ctx)
 }

 _, err = tx.Exec(ctx, "DELETE FROM memory_outbox WHERE id = ANY($1)", ids)
 if err != nil {
  return fmt.Errorf("delete outbox: %w", err)
 }

 p.logger.Debug("memory.outbox.published", zap.String("schema", schema), zap.Int("count", len(ids)))
 return tx.Commit(ctx)
}
```

- [ ] **Step 2: Modify ChatStore.AddMessage to write outbox in same transaction**

In `internal/agent/chat_store.go`, find the `AddMessage` method of `PgChatStore`. After the INSERT into `chat_messages`, add:

```go
// Write to memory outbox for async pipeline processing
outboxPayload, _ := json.Marshal(map[string]interface{}{
    "message_id":      msg.ID,
    "conversation_id": msg.ConversationID,
    "tenant_id":       tenantID,
    "user_id":         "", // filled by caller context if available
    "agent_id":        "", // filled by caller context if available
    "role":            msg.Role,
    "content":         msg.Content,
    "created_at":      msg.CreatedAt,
})
_, err = tx.Exec(ctx,
    "INSERT INTO memory_outbox (message_id, payload) VALUES ($1, $2)",
    msg.ID, outboxPayload)
if err != nil {
    return fmt.Errorf("insert memory_outbox: %w", err)
}
```

Note: The `AddMessage` method already uses a transaction via `execTenantID`. The outbox insert must be added INSIDE that transaction closure, after the chat_messages insert. The user_id and agent_id can be extracted from the conversation row or passed through ChatMessage struct.

We need to add `UserID` and `AgentID` fields to `ChatMessage`:

```go
type ChatMessage struct {
    ID             string
    ConversationID string
    Role           string
    Content        string
    StepsJSON      json.RawMessage
    IsError        bool
    CreatedAt      time.Time
    UserID         string // for outbox payload
    AgentID        string // for outbox payload
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/memory/pipeline/... ./internal/agent/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/memory/pipeline/outbox_poller.go internal/agent/chat_store.go
git commit -m "feat(memory-pipeline): implement outbox poller and wire ChatStore.AddMessage"
```

---

## Task 6: Embedder Worker (Stage 1)

**Files:**

- Create: `internal/memory/pipeline/embedder.go`

- [ ] **Step 1: Write EmbedderWorker**

```go
// internal/memory/pipeline/embedder.go
package pipeline

import (
 "context"
 "fmt"
 "time"

 "github.com/nats-io/nats.go/jetstream"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/embedding"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type VectorStore interface {
 Upsert(ctx context.Context, tenantID string, id string, vector []float32, metadata map[string]interface{}) error
}

type EmbedderWorker struct {
 consumer  jetstream.Consumer
 js        jetstream.JetStream
 embedSvc  *embedding.EmbeddingService
 vectorDB  VectorStore
 logger    *zap.Logger
 stopCh    chan struct{}
}

func NewEmbedderWorker(
 consumer jetstream.Consumer,
 js jetstream.JetStream,
 embedSvc *embedding.EmbeddingService,
 vectorDB VectorStore,
 logger *zap.Logger,
) *EmbedderWorker {
 return &EmbedderWorker{
  consumer: consumer,
  js:       js,
  embedSvc: embedSvc,
  vectorDB: vectorDB,
  logger:   logger,
  stopCh:   make(chan struct{}),
 }
}

func (w *EmbedderWorker) Start(ctx context.Context) {
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

func (w *EmbedderWorker) Stop() {
 close(w.stopCh)
}

func (w *EmbedderWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
 start := time.Now()

 ev, err := UnmarshalRawEvent(msg.Data())
 if err != nil {
  w.logger.Error("memory.embed.unmarshal", zap.Error(err))
  msg.Ack() // discard malformed
  return
 }

 w.logger.Debug("memory.embed.start",
  zap.String("message_id", ev.MessageID),
  zap.String("tenant_id", ev.TenantID),
  zap.Int("content_length", len(ev.Content)))

 vector, err := w.embedSvc.EmbedVector(ctx, ev.Content)
 if err != nil {
  w.logger.Error("memory.embed.error",
   zap.String("message_id", ev.MessageID),
   zap.String("tenant_id", ev.TenantID),
   zap.Error(err))
  msg.Nak() // retry
  return
 }

 metadata := map[string]interface{}{
  "conversation_id": ev.ConversationID,
  "user_id":         ev.UserID,
  "agent_id":        ev.AgentID,
  "role":            ev.Role,
  "content":         ev.Content,
  "created_at":      ev.CreatedAt.Format(time.RFC3339),
 }
 if err := w.vectorDB.Upsert(ctx, ev.TenantID, ev.MessageID, vector, metadata); err != nil {
  w.logger.Error("memory.embed.milvus",
   zap.String("message_id", ev.MessageID),
   zap.Error(err))
  msg.Nak()
  return
 }

 enrichedEv := &MemoryEnrichedEvent{
  MemoryRawEvent: *ev,
  VectorID:       ev.MessageID,
 }
 data, err := enrichedEv.Marshal()
 if err != nil {
  w.logger.Error("memory.embed.marshal_enriched", zap.Error(err))
  msg.Nak()
  return
 }

 subject := fmt.Sprintf("%s.%s", constants.MemoryEnrichedSubject, ev.TenantID)
 if _, err := w.js.Publish(ctx, subject, data); err != nil {
  w.logger.Error("memory.embed.publish_enriched",
   zap.String("message_id", ev.MessageID),
   zap.Error(err))
  msg.Nak()
  return
 }

 msg.Ack()
 w.logger.Info("memory.embed.success",
  zap.String("message_id", ev.MessageID),
  zap.String("tenant_id", ev.TenantID),
  zap.Int("vector_dim", len(vector)),
  zap.Int64("latency_ms", time.Since(start).Milliseconds()))
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/memory/pipeline/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/memory/pipeline/embedder.go
git commit -m "feat(memory-pipeline): implement Stage 1 embedder worker"
```

---

## Task 7: Enricher Worker (Stage 2)

**Files:**

- Create: `internal/memory/pipeline/enricher.go`
- Create: `internal/memory/pipeline/enricher_prompt.go`

- [ ] **Step 1: Write enrichment prompts**

```go
// internal/memory/pipeline/enricher_prompt.go
package pipeline

const enrichmentPrompt = `Analyze this conversation message and extract:
1. Named entities (people, products, concepts, locations) with type and confidence
2. Importance score (0.0-1.0) based on information density and future relevance
3. Estimated token count of the content

Respond in JSON only, no markdown:
{
  "entities": [{"name": "...", "type": "person|product|concept|location|org", "confidence": 0.0-1.0}],
  "importance": 0.0-1.0,
  "token_estimate": 123,
  "keywords": ["...", "..."]
}

Message (role: %s):
%s`

const summaryPrompt = `Summarize the following conversation messages into a concise paragraph that captures:
- Key topics discussed
- Important decisions or conclusions
- User preferences or requirements mentioned
- Any action items or next steps

Keep the summary under 200 words. Focus on information that would be useful context for future conversations.

Messages:
%s`
```

- [ ] **Step 2: Write EnricherWorker**

```go
// internal/memory/pipeline/enricher.go
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

type EnrichmentResult struct {
 Entities      []ExtractedEntity `json:"entities"`
 Importance    float64           `json:"importance"`
 TokenEstimate int               `json:"token_estimate"`
 Keywords      []string          `json:"keywords"`
}

type ExtractedEntity struct {
 Name       string  `json:"name"`
 Type       string  `json:"type"`
 Confidence float64 `json:"confidence"`
}

type EnricherWorker struct {
 consumer  jetstream.Consumer
 pool      *pgxpool.Pool
 llm       *llmgateway.Gateway
 logger    *zap.Logger
 model     string
 summaryModel string
 threshold int
 stopCh    chan struct{}
}

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

func (w *EnricherWorker) Stop() {
 close(w.stopCh)
}

func (w *EnricherWorker) processMessage(ctx context.Context, msg jetstream.Msg) {
 start := time.Now()

 ev, err := UnmarshalEnrichedEvent(msg.Data())
 if err != nil {
  w.logger.Error("memory.enrich.unmarshal", zap.Error(err))
  msg.Ack()
  return
 }

 w.logger.Debug("memory.enrich.start",
  zap.String("message_id", ev.MessageID),
  zap.String("tenant_id", ev.TenantID))

 enrichment, err := w.callEnrichLLM(ctx, ev.Role, ev.Content)
 if err != nil {
  w.logger.Error("memory.enrich.llm_error",
   zap.String("message_id", ev.MessageID),
   zap.Error(err))
  msg.Nak()
  return
 }

 if err := w.persistEnrichment(ctx, ev, enrichment); err != nil {
  w.logger.Error("memory.enrich.persist_error",
   zap.String("message_id", ev.MessageID),
   zap.Error(err))
  msg.Nak()
  return
 }

 msg.Ack()
 w.logger.Info("memory.enrich.success",
  zap.String("message_id", ev.MessageID),
  zap.String("tenant_id", ev.TenantID),
  zap.Int("entity_count", len(enrichment.Entities)),
  zap.Float64("importance", enrichment.Importance),
  zap.Int64("latency_ms", time.Since(start).Milliseconds()))
}

func (w *EnricherWorker) callEnrichLLM(ctx context.Context, role, content string) (*EnrichmentResult, error) {
 prompt := fmt.Sprintf(enrichmentPrompt, role, content)
 req := &llmgateway.CompletionRequest{
  Model: w.model,
  Messages: []llmgateway.Message{
   {Role: "user", Content: prompt},
  },
  Temperature: floatPtr(0.1),
 }

 resp, err := w.llm.Complete(ctx, req)
 if err != nil {
  return nil, fmt.Errorf("llm complete: %w", err)
 }

 w.logger.Info("memory.enrich.llm",
  zap.String("model", w.model),
  zap.Int("prompt_tokens", resp.Usage.PromptTokens),
  zap.Int("completion_tokens", resp.Usage.CompletionTokens))

 var result EnrichmentResult
 text := strings.TrimSpace(resp.Content)
 text = strings.TrimPrefix(text, "```json")
 text = strings.TrimPrefix(text, "```")
 text = strings.TrimSuffix(text, "```")
 text = strings.TrimSpace(text)

 if err := json.Unmarshal([]byte(text), &result); err != nil {
  return nil, fmt.Errorf("parse enrichment JSON: %w", err)
 }
 return &result, nil
}

func (w *EnricherWorker) persistEnrichment(ctx context.Context, ev *MemoryEnrichedEvent, enrichment *EnrichmentResult) error {
 tx, err := w.pool.Begin(ctx)
 if err != nil {
  return fmt.Errorf("begin tx: %w", err)
 }
 defer tx.Rollback(ctx) //nolint:errcheck

 // Set tenant schema — extract from TenantID using tenantdb convention
 schema := "tenant_" + ev.TenantID
 if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
  return fmt.Errorf("set schema: %w", err)
 }

 // Upsert memory_entries with enrichment data
 _, err = tx.Exec(ctx, `
  INSERT INTO memory_entries (id, session_id, user_id, agent_id, role, content, type, importance, keywords, token_estimate, scope_layer, enriched_at, conversation_id, created_at)
  VALUES ($1, NULL, $2, $3, $4, $5, 'long_term', $6, $7, $8, 1, NOW(), $9, $10)
  ON CONFLICT (id) DO UPDATE SET
   importance = EXCLUDED.importance,
   keywords = EXCLUDED.keywords,
   token_estimate = EXCLUDED.token_estimate,
   enriched_at = NOW()`,
  ev.MessageID, ev.UserID, ev.AgentID, ev.Role, ev.Content,
  enrichment.Importance, enrichment.Keywords, enrichment.TokenEstimate,
  ev.ConversationID, ev.CreatedAt)
 if err != nil {
  return fmt.Errorf("upsert memory_entries: %w", err)
 }

 // Upsert entities
 for _, entity := range enrichment.Entities {
  _, err = tx.Exec(ctx, `
   INSERT INTO entities (name, type, user_id, agent_id, confidence, scope_layer, occurrence_count, last_seen, properties)
   VALUES ($1, $2, $3, $4, $5, 1, 1, NOW(), '{}')
   ON CONFLICT ON CONSTRAINT idx_entities_name_type DO UPDATE SET
    occurrence_count = entities.occurrence_count + 1,
    confidence = GREATEST(entities.confidence, EXCLUDED.confidence),
    last_seen = NOW()`,
   entity.Name, entity.Type, ev.UserID, ev.AgentID, entity.Confidence)
  if err != nil {
   w.logger.Warn("memory.enrich.entity_upsert",
    zap.String("entity", entity.Name), zap.Error(err))
  }
 }

 // Update token budget and check summary trigger
 var accumulated int
 err = tx.QueryRow(ctx, `
  INSERT INTO memory_token_budgets (conversation_id, accumulated, last_reset_at)
  VALUES ($1, $2, NOW())
  ON CONFLICT (conversation_id) DO UPDATE SET
   accumulated = memory_token_budgets.accumulated + EXCLUDED.accumulated
  RETURNING accumulated`,
  ev.ConversationID, enrichment.TokenEstimate).Scan(&accumulated)
 if err != nil {
  return fmt.Errorf("update token budget: %w", err)
 }

 if accumulated >= w.threshold {
  if err := w.triggerSummary(ctx, tx, ev, accumulated); err != nil {
   w.logger.Warn("memory.enrich.summary_error",
    zap.String("conversation_id", ev.ConversationID),
    zap.Error(err))
   // Non-fatal: still commit the enrichment
  }
 }

 return tx.Commit(ctx)
}

func (w *EnricherWorker) triggerSummary(ctx context.Context, tx pgx.Tx, ev *MemoryEnrichedEvent, budgetBefore int) error {
 // Fetch recent messages for this conversation
 rows, err := tx.Query(ctx,
  "SELECT role, content FROM chat_messages WHERE conversation_id = $1 ORDER BY created_at ASC",
  ev.ConversationID)
 if err != nil {
  return fmt.Errorf("fetch messages: %w", err)
 }
 defer rows.Close()

 var sb strings.Builder
 for rows.Next() {
  var role, content string
  if err := rows.Scan(&role, &content); err != nil {
   return fmt.Errorf("scan message: %w", err)
  }
  sb.WriteString(fmt.Sprintf("[%s]: %s\n", role, content))
 }
 if err := rows.Err(); err != nil {
  return fmt.Errorf("rows err: %w", err)
 }

 prompt := fmt.Sprintf(summaryPrompt, sb.String())
 req := &llmgateway.CompletionRequest{
  Model: w.summaryModel,
  Messages: []llmgateway.Message{
   {Role: "user", Content: prompt},
  },
  Temperature: floatPtr(0.3),
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

 // Reset token budget
 _, err = tx.Exec(ctx,
  "UPDATE memory_token_budgets SET accumulated = 0, last_reset_at = NOW() WHERE conversation_id = $1",
  ev.ConversationID)
 if err != nil {
  return fmt.Errorf("reset budget: %w", err)
 }

 w.logger.Info("memory.enrich.summary",
  zap.String("conversation_id", ev.ConversationID),
  zap.Int("token_budget_before", budgetBefore),
  zap.Int("summary_length", len(summary)))
 return nil
}

func floatPtr(f float32) *float32 { return &f }
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/memory/pipeline/...`
Expected: no errors (check that `llmgateway.Message` and `llmgateway.CompletionRequest` fields match existing types)

- [ ] **Step 4: Commit**

```bash
git add internal/memory/pipeline/enricher.go internal/memory/pipeline/enricher_prompt.go
git commit -m "feat(memory-pipeline): implement Stage 2 enricher worker with LLM extraction and summary"
```

---

## Task 8: Pipeline Orchestrator

**Files:**

- Create: `internal/memory/pipeline/pipeline.go`

- [ ] **Step 1: Write Pipeline struct that coordinates all workers**

```go
// internal/memory/pipeline/pipeline.go
package pipeline

import (
 "context"
 "fmt"
 "sync"

 "github.com/jackc/pgx/v5/pgxpool"
 "github.com/nats-io/nats.go"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/embedding"
 "github.com/byteBuilderX/stratum/internal/llmgateway"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type Pipeline struct {
 cfg       Config
 pool      *pgxpool.Pool
 nc        *nats.Conn
 jsm       *JetStreamManager
 embedSvc  *embedding.EmbeddingService
 vectorDB  VectorStore
 llm       *llmgateway.Gateway
 logger    *zap.Logger

 poller    *OutboxPoller
 embedders []*EmbedderWorker
 enrichers []*EnricherWorker

 cancel context.CancelFunc
 wg     sync.WaitGroup
}

func New(
 cfg Config,
 pool *pgxpool.Pool,
 nc *nats.Conn,
 embedSvc *embedding.EmbeddingService,
 vectorDB VectorStore,
 llm *llmgateway.Gateway,
 logger *zap.Logger,
) *Pipeline {
 return &Pipeline{
  cfg:      cfg,
  pool:     pool,
  nc:       nc,
  embedSvc: embedSvc,
  vectorDB: vectorDB,
  llm:      llm,
  logger:   logger,
 }
}

func (p *Pipeline) Start(ctx context.Context) error {
 if !p.cfg.Enabled {
  p.logger.Info("memory pipeline disabled")
  return nil
 }

 jsm, err := NewJetStreamManager(p.nc, p.logger)
 if err != nil {
  return fmt.Errorf("jetstream manager: %w", err)
 }
 p.jsm = jsm

 if err := jsm.EnsureStreams(ctx); err != nil {
  return fmt.Errorf("ensure streams: %w", err)
 }

 js := jsm.JS()

 // Start outbox poller
 p.poller = NewOutboxPoller(p.pool, js, p.logger, p.cfg)

 pipeCtx, cancel := context.WithCancel(ctx)
 p.cancel = cancel

 p.wg.Add(1)
 go func() {
  defer p.wg.Done()
  p.poller.Start(pipeCtx)
 }()

 // Start embedder workers
 embedConsumer, err := jsm.CreateConsumer(ctx,
  constants.MemoryRawStream,
  constants.EmbedderConsumerName,
  constants.MemoryRawSubject+".>",
  p.cfg.EmbedAckWait,
  p.cfg.MaxDeliver)
 if err != nil {
  cancel()
  return fmt.Errorf("create embed consumer: %w", err)
 }

 for i := 0; i < p.cfg.EmbedWorkers; i++ {
  worker := NewEmbedderWorker(embedConsumer, js, p.embedSvc, p.vectorDB, p.logger)
  p.embedders = append(p.embedders, worker)
  p.wg.Add(1)
  go func() {
   defer p.wg.Done()
   worker.Start(pipeCtx)
  }()
 }

 // Start enricher workers
 enrichConsumer, err := jsm.CreateConsumer(ctx,
  constants.MemoryEnrichedStream,
  constants.EnricherConsumerName,
  constants.MemoryEnrichedSubject+".>",
  p.cfg.EnrichAckWait,
  p.cfg.MaxDeliver)
 if err != nil {
  cancel()
  return fmt.Errorf("create enrich consumer: %w", err)
 }

 for i := 0; i < p.cfg.EnrichWorkers; i++ {
  worker := NewEnricherWorker(enrichConsumer, p.pool, p.llm, p.logger, p.cfg)
  p.enrichers = append(p.enrichers, worker)
  p.wg.Add(1)
  go func() {
   defer p.wg.Done()
   worker.Start(pipeCtx)
  }()
 }

 p.logger.Info("memory pipeline started",
  zap.Int("embed_workers", p.cfg.EmbedWorkers),
  zap.Int("enrich_workers", p.cfg.EnrichWorkers))
 return nil
}

func (p *Pipeline) Stop(ctx context.Context) error {
 if p.cancel == nil {
  return nil
 }
 p.logger.Info("memory pipeline stopping")
 p.cancel()
 p.wg.Wait()
 p.logger.Info("memory pipeline stopped")
 return nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/memory/pipeline/...`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/memory/pipeline/pipeline.go
git commit -m "feat(memory-pipeline): add pipeline orchestrator (Start/Stop all workers)"
```

---

## Task 9: Harness Registration (main.go)

**Files:**

- Modify: `cmd/server/main.go`

- [ ] **Step 1: Add pipeline import and initialization**

After the Hermes component registration, add:

```go
// 2b. Memory Pipeline component (depends on NATS + PG + LLM)
var memPipeline *pipeline.Pipeline
pipelineCfg := pipeline.DefaultConfig()
pipelineCfg.Enabled = cfg.MemoryPipelineEnabled // add to config struct
pipelineCfg.NatsURL = cfg.NatsURL

pipelineComponent := harnesspkg.NewSimpleComponent("memory-pipeline", logger,
    harnesspkg.WithStartFunc(func(ctx context.Context) error {
        if !pipelineCfg.Enabled {
            logger.Info("Memory pipeline disabled, skipping")
            return nil
        }
        nc, err := nats.Connect(pipelineCfg.NatsURL)
        if err != nil {
            logger.Warn("memory-pipeline: NATS connect failed", zap.Error(err))
            return nil
        }
        memPipeline = pipeline.New(pipelineCfg, pgPool.DB(), nc, embeddingSvc, vectorStore, gateway, logger)
        return memPipeline.Start(ctx)
    }),
    harnesspkg.WithStopFunc(func(ctx context.Context) error {
        if memPipeline != nil {
            return memPipeline.Stop(ctx)
        }
        return nil
    }),
    harnesspkg.WithHealthCheckFunc(func(ctx context.Context) error {
        if !pipelineCfg.Enabled {
            return nil
        }
        if memPipeline == nil {
            return fmt.Errorf("memory pipeline not initialized")
        }
        return nil
    }),
)
if err := appHarness.Register(pipelineComponent); err != nil {
    logger.Fatal("Failed to register memory pipeline component", zap.Error(err))
}
```

- [ ] **Step 2: Add `MemoryPipelineEnabled` to config struct**

In `internal/config/config.go`, add:

```go
MemoryPipelineEnabled bool `mapstructure:"MEMORY_PIPELINE_ENABLED"`
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./cmd/server/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go internal/config/config.go
git commit -m "feat(memory-pipeline): register pipeline as harness component in main.go"
```

---

## Task 10: Prometheus Metrics

**Files:**

- Create: `internal/memory/pipeline/metrics.go`

- [ ] **Step 1: Define pipeline metrics**

```go
// internal/memory/pipeline/metrics.go
package pipeline

import "github.com/prometheus/client_golang/prometheus"

var (
 outboxPending = prometheus.NewGauge(prometheus.GaugeOpts{
  Name: "memory_outbox_pending",
  Help: "Number of pending outbox messages",
 })
 outboxPublished = prometheus.NewCounterVec(prometheus.CounterOpts{
  Name: "memory_outbox_published_total",
  Help: "Total outbox messages published",
 }, []string{"tenant_id", "status"})
 embedDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
  Name:    "memory_embed_duration_seconds",
  Help:    "Embedding processing duration",
  Buckets: prometheus.DefBuckets,
 })
 embedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
  Name: "memory_embed_total",
  Help: "Total embed operations",
 }, []string{"tenant_id", "status"})
 enrichDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
  Name:    "memory_enrich_duration_seconds",
  Help:    "Enrichment processing duration",
  Buckets: prometheus.DefBuckets,
 })
 enrichTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
  Name: "memory_enrich_total",
  Help: "Total enrich operations",
 }, []string{"tenant_id", "status"})
 summaryTriggered = prometheus.NewCounter(prometheus.CounterOpts{
  Name: "memory_summary_triggered_total",
  Help: "Total summary generations triggered",
 })
 dlqTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
  Name: "memory_dlq_total",
  Help: "Total messages sent to DLQ",
 }, []string{"tenant_id", "stage"})
 entitiesExtracted = prometheus.NewCounter(prometheus.CounterOpts{
  Name: "memory_entities_extracted_total",
  Help: "Total entities extracted",
 })
)

func RegisterMetrics(reg prometheus.Registerer) {
 reg.MustRegister(
  outboxPending, outboxPublished,
  embedDuration, embedTotal,
  enrichDuration, enrichTotal,
  summaryTriggered, dlqTotal, entitiesExtracted,
 )
}
```

- [ ] **Step 2: Wire metrics into workers**

Add metric recording calls to EmbedderWorker.processMessage and EnricherWorker.processMessage (observe durations, increment counters on success/error).

- [ ] **Step 3: Commit**

```bash
git add internal/memory/pipeline/metrics.go
git commit -m "feat(memory-pipeline): add Prometheus metrics for pipeline observability"
```

---

## Task 11: Memory Injection into Agent

**Files:**

- Modify: `internal/agent/agent.go` (BuildInitMessages or Execute)
- Create: `internal/memory/pipeline/injector.go`

- [ ] **Step 1: Write MemoryInjector**

```go
// internal/memory/pipeline/injector.go
package pipeline

import (
 "context"
 "fmt"
 "strings"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

type MemoryInjector struct {
 pool   *pgxpool.Pool
 logger *zap.Logger
}

func NewMemoryInjector(pool *pgxpool.Pool, logger *zap.Logger) *MemoryInjector {
 return &MemoryInjector{pool: pool, logger: logger}
}

type InjectionContext struct {
 TenantID       string
 UserID         string
 AgentID        string
 ConversationID string
}

func (inj *MemoryInjector) BuildContext(ctx context.Context, ic InjectionContext) (string, error) {
 schema := "tenant_" + ic.TenantID
 conn, err := inj.pool.Acquire(ctx)
 if err != nil {
  return "", fmt.Errorf("acquire conn: %w", err)
 }
 defer conn.Release()

 _, err = conn.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize()))
 if err != nil {
  return "", fmt.Errorf("set schema: %w", err)
 }

 // Fetch latest summary for this conversation
 var summary string
 err = conn.QueryRow(ctx,
  "SELECT summary FROM memory_summaries WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1",
  ic.ConversationID).Scan(&summary)
 if err != nil && err != pgx.ErrNoRows {
  return "", fmt.Errorf("fetch summary: %w", err)
 }

 // Fetch top entities (L1 + L2) ordered by last_seen
 rows, err := conn.Query(ctx, `
  SELECT name FROM entities
  WHERE user_id = $1 AND (agent_id = $2 OR agent_id IS NULL)
  ORDER BY last_seen DESC
  LIMIT $3`,
  ic.UserID, ic.AgentID, constants.EnricherTopEntities)
 if err != nil {
  return "", fmt.Errorf("fetch entities: %w", err)
 }
 defer rows.Close()

 var entityNames []string
 for rows.Next() {
  var name string
  if err := rows.Scan(&name); err != nil {
   continue
  }
  entityNames = append(entityNames, name)
 }

 if summary == "" && len(entityNames) == 0 {
  return "", nil
 }

 var sb strings.Builder
 sb.WriteString("[Memory Context]\n")
 if summary != "" {
  sb.WriteString("Summary: ")
  sb.WriteString(summary)
  sb.WriteString("\n")
 }
 if len(entityNames) > 0 {
  sb.WriteString("Key Entities: ")
  sb.WriteString(strings.Join(entityNames, ", "))
  sb.WriteString("\n")
 }

 return sb.String(), nil
}
```

- [ ] **Step 2: Integrate into BaseAgent.Execute**

In `internal/agent/agent.go`, before `BuildInitMessages` call (~line 313), add memory injection:

```go
// Inject memory context into system prompt if pipeline provides context
if a.MemoryInjector != nil && cfg.ConversationID != "" {
    ic := pipeline.InjectionContext{
        TenantID:       cfg.TenantID,
        UserID:         cfg.UserID,
        AgentID:        agentID,
        ConversationID: cfg.ConversationID,
    }
    if memCtx, err := a.MemoryInjector.BuildContext(ctx, ic); err != nil {
        a.Logger.Warn("memory injection failed", zap.Error(err))
    } else if memCtx != "" {
        systemPrompt = memCtx + systemPrompt
    }
}
```

Add `MemoryInjector *pipeline.MemoryInjector` field to `BaseAgent` struct.

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/memory/pipeline/... ./internal/agent/...`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add internal/memory/pipeline/injector.go internal/agent/agent.go
git commit -m "feat(memory-pipeline): add memory injector for agent system prompt enrichment"
```

---

## Task 12: recall_memory Tool

**Files:**

- Modify: `internal/agent/agent.go` (add tool definition to ReAct available tools)
- Create: `internal/memory/pipeline/recall_tool.go`

- [ ] **Step 1: Write recall tool handler**

```go
// internal/memory/pipeline/recall_tool.go
package pipeline

import (
 "context"
 "encoding/json"
 "fmt"
 "strings"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/embedding"
)

type RecallToolHandler struct {
 pool     *pgxpool.Pool
 embedSvc *embedding.EmbeddingService
 logger   *zap.Logger
}

func NewRecallToolHandler(pool *pgxpool.Pool, embedSvc *embedding.EmbeddingService, logger *zap.Logger) *RecallToolHandler {
 return &RecallToolHandler{pool: pool, embedSvc: embedSvc, logger: logger}
}

type RecallInput struct {
 Query string `json:"query"`
 Scope string `json:"scope"`
 Limit int    `json:"limit"`
}

type RecallResult struct {
 Memories []RecallEntry `json:"memories"`
}

type RecallEntry struct {
 Content    string  `json:"content"`
 Role       string  `json:"role"`
 Importance float64 `json:"importance"`
 CreatedAt  string  `json:"created_at"`
}

func (h *RecallToolHandler) Execute(ctx context.Context, tenantID, userID, agentID string, input json.RawMessage) (string, error) {
 var params RecallInput
 if err := json.Unmarshal(input, &params); err != nil {
  return "", fmt.Errorf("parse recall input: %w", err)
 }
 if params.Limit == 0 || params.Limit > 20 {
  params.Limit = 5
 }
 if params.Scope == "" {
  params.Scope = "private"
 }

 // Generate query vector
 queryVec, err := h.embedSvc.EmbedVector(ctx, params.Query)
 if err != nil {
  return "", fmt.Errorf("embed query: %w", err)
 }

 schema := "tenant_" + tenantID
 conn, err := h.pool.Acquire(ctx)
 if err != nil {
  return "", fmt.Errorf("acquire conn: %w", err)
 }
 defer conn.Release()

 _, err = conn.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize()))
 if err != nil {
  return "", fmt.Errorf("set schema: %w", err)
 }

 // For now, use keyword-based fallback (Milvus vector search would be the production path)
 _ = queryVec // TODO: integrate with Milvus vector search

 var scopeFilter string
 switch params.Scope {
 case "private":
  scopeFilter = fmt.Sprintf("AND user_id = '%s' AND agent_id = '%s'", userID, agentID)
 case "personal":
  scopeFilter = fmt.Sprintf("AND user_id = '%s'", userID)
 case "shared":
  scopeFilter = "" // all scopes
 }

 query := fmt.Sprintf(`
  SELECT content, role, importance, created_at
  FROM memory_entries
  WHERE enriched_at IS NOT NULL %s
  ORDER BY importance DESC, created_at DESC
  LIMIT $1`, scopeFilter)

 rows, err := conn.Query(ctx, query, params.Limit)
 if err != nil {
  return "", fmt.Errorf("query memories: %w", err)
 }
 defer rows.Close()

 var entries []RecallEntry
 for rows.Next() {
  var e RecallEntry
  var ts interface{}
  if err := rows.Scan(&e.Content, &e.Role, &e.Importance, &ts); err != nil {
   continue
  }
  entries = append(entries, e)
 }

 result := RecallResult{Memories: entries}
 data, _ := json.Marshal(result)
 return string(data), nil
}

func RecallToolDefinition() map[string]interface{} {
 return map[string]interface{}{
  "name":        "recall_memory",
  "description": "Search long-term memory for relevant past context",
  "input_schema": map[string]interface{}{
   "type": "object",
   "properties": map[string]interface{}{
    "query": map[string]interface{}{
     "type":        "string",
     "description": "Search query",
    },
    "scope": map[string]interface{}{
     "type":        "string",
     "enum":        []string{"private", "personal", "shared"},
     "default":     "private",
     "description": "Memory scope: private (user×agent), personal (user), shared (tenant)",
    },
    "limit": map[string]interface{}{
     "type":        "integer",
     "default":     5,
     "maximum":     20,
     "description": "Max results",
    },
   },
   "required": []string{"query"},
  },
 }
}
```

- [ ] **Step 2: Register recall_memory as an available tool in agent.Execute**

In the ReAct case of `BaseAgent.Execute`, add the recall_memory tool definition to `availableTools` when the pipeline is enabled:

```go
if a.MemoryInjector != nil {
    toolDef := pipeline.RecallToolDefinition()
    availableTools = append(availableTools, capgateway.ToolDefinition{
        Name:        toolDef["name"].(string),
        Description: toolDef["description"].(string),
        InputSchema: toolDef["input_schema"].(map[string]interface{}),
    })
}
```

- [ ] **Step 3: Commit**

```bash
git add internal/memory/pipeline/recall_tool.go internal/agent/agent.go
git commit -m "feat(memory-pipeline): add recall_memory tool for agent long-term memory search"
```

---

## Task 13: GetStats Refactor

**Files:**

- Modify: `internal/memory/manager.go` (GetStats to query real data)

- [ ] **Step 1: Rewrite GetStats to use actual pipeline tables**

Replace the current hardcoded GetStats implementation to query:

- `memory_entries` count → TotalEntries
- `memory_entries WHERE enriched_at IS NOT NULL` → LongTermCount
- `memory_entries WHERE enriched_at IS NULL` → ShortTermCount
- `entities` count → EntityCount
- `chat_conversations` count → SessionsCount
- Distinct `user_id` from `memory_entries` → ActiveUsers
- `memory_entries WHERE enriched_at IS NOT NULL` count → VectorCount (approximation)
- MAX(`created_at`) from `memory_entries` → LastAccessTime

```go
func (m *MemoryManager) GetStats(ctx context.Context, sessionCtx *SessionContext) (*MemoryStats, error) {
    if m.pool == nil || sessionCtx == nil || sessionCtx.TenantID == "" {
        return &MemoryStats{}, nil
    }

    stats := &MemoryStats{}
    err := m.execTenant(ctx, sessionCtx.TenantID, func(ctx context.Context, tx pgx.Tx) error {
        tx.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entries").Scan(&stats.TotalEntries)
        tx.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entries WHERE enriched_at IS NOT NULL").Scan(&stats.LongTermCount)
        stats.ShortTermCount = stats.TotalEntries - stats.LongTermCount
        tx.QueryRow(ctx, "SELECT COUNT(*) FROM entities").Scan(&stats.EntityCount)
        tx.QueryRow(ctx, "SELECT COUNT(*) FROM chat_conversations").Scan(&stats.SessionsCount)
        tx.QueryRow(ctx, "SELECT COUNT(DISTINCT user_id) FROM memory_entries WHERE user_id IS NOT NULL").Scan(&stats.ActiveUsers)
        stats.VectorCount = stats.LongTermCount
        tx.QueryRow(ctx, "SELECT COALESCE(MAX(created_at), '1970-01-01') FROM memory_entries").Scan(&stats.LastAccessTime)
        return nil
    })
    if err != nil {
        return &MemoryStats{}, nil
    }
    return stats, nil
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./internal/memory/...`

- [ ] **Step 3: Commit**

```bash
git add internal/memory/manager.go
git commit -m "fix(memory): refactor GetStats to query actual pipeline tables instead of hardcoded zeros"
```

---

## Task 14: tenantdb.ListTenantSchemas Helper

**Files:**

- Modify or Create: `pkg/tenantdb/list_schemas.go`

The OutboxPoller needs to iterate all tenant schemas. Check if `tenantdb.ListTenantSchemas` exists; if not, implement:

- [ ] **Step 1: Write ListTenantSchemas**

```go
// pkg/tenantdb/list_schemas.go
package tenantdb

import (
 "context"
 "fmt"

 "github.com/jackc/pgx/v5/pgxpool"
)

func ListTenantSchemas(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
 rows, err := pool.Query(ctx,
  "SELECT schema_name FROM information_schema.schemata WHERE schema_name LIKE 'tenant_%'")
 if err != nil {
  return nil, fmt.Errorf("list tenant schemas: %w", err)
 }
 defer rows.Close()

 var schemas []string
 for rows.Next() {
  var s string
  if err := rows.Scan(&s); err != nil {
   return nil, fmt.Errorf("scan schema: %w", err)
  }
  schemas = append(schemas, s)
 }
 return schemas, rows.Err()
}
```

- [ ] **Step 2: Commit**

```bash
git add pkg/tenantdb/list_schemas.go
git commit -m "feat(tenantdb): add ListTenantSchemas helper for pipeline poller"
```

---

## Task 15: Integration Test

**Files:**

- Create: `internal/memory/pipeline/pipeline_test.go`

- [ ] **Step 1: Write integration test with embedded NATS**

```go
// internal/memory/pipeline/pipeline_test.go
package pipeline

import (
 "context"
 "encoding/json"
 "testing"
 "time"

 "github.com/nats-io/nats-server/v2/server"
 natsserver "github.com/nats-io/nats-server/v2/test"
 "github.com/nats-io/nats.go"
 "github.com/stretchr/testify/assert"
 "github.com/stretchr/testify/require"
 "go.uber.org/zap/zaptest"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

func startJetStreamServer(t *testing.T) (*server.Server, *nats.Conn) {
 t.Helper()
 opts := natsserver.DefaultTestOptions
 opts.JetStream = true
 opts.Port = -1
 s := natsserver.RunServer(&opts)
 t.Cleanup(s.Shutdown)

 nc, err := nats.Connect(s.ClientURL())
 require.NoError(t, err)
 t.Cleanup(nc.Close)
 return s, nc
}

func TestJetStreamManager_EnsureStreams(t *testing.T) {
 _, nc := startJetStreamServer(t)
 logger := zaptest.NewLogger(t)

 jsm, err := NewJetStreamManager(nc, logger)
 require.NoError(t, err)

 ctx := context.Background()
 err = jsm.EnsureStreams(ctx)
 require.NoError(t, err)

 // Verify streams exist
 js := jsm.JS()
 stream, err := js.Stream(ctx, constants.MemoryRawStream)
 require.NoError(t, err)
 assert.Equal(t, constants.MemoryRawStream, stream.CachedInfo().Config.Name)
}

func TestEventPublishConsume(t *testing.T) {
 _, nc := startJetStreamServer(t)
 logger := zaptest.NewLogger(t)

 jsm, err := NewJetStreamManager(nc, logger)
 require.NoError(t, err)

 ctx := context.Background()
 require.NoError(t, jsm.EnsureStreams(ctx))

 ev := &MemoryRawEvent{
  MessageID:      "test-msg-1",
  ConversationID: "conv-1",
  TenantID:       "tenant-test",
  UserID:         "user-1",
  AgentID:        "agent-1",
  Role:           "user",
  Content:        "Hello from test",
  CreatedAt:      time.Now().Truncate(time.Millisecond),
 }
 data, err := json.Marshal(ev)
 require.NoError(t, err)

 js := jsm.JS()
 _, err = js.Publish(ctx, constants.MemoryRawSubject+".tenant-test", data)
 require.NoError(t, err)

 consumer, err := jsm.CreateConsumer(ctx,
  constants.MemoryRawStream,
  "test-consumer",
  constants.MemoryRawSubject+".>",
  10*time.Second, 3)
 require.NoError(t, err)

 msgs, err := consumer.Fetch(1, nats.MaxWait(2*time.Second))
 require.NoError(t, err)

 var received *MemoryRawEvent
 for msg := range msgs.Messages() {
  received, err = UnmarshalRawEvent(msg.Data())
  require.NoError(t, err)
  msg.Ack()
 }
 require.NotNil(t, received)
 assert.Equal(t, ev.MessageID, received.MessageID)
 assert.Equal(t, ev.Content, received.Content)
}
```

- [ ] **Step 2: Run tests**

Run: `go test ./internal/memory/pipeline/... -v -short -timeout 30s`
Expected: PASS (requires nats-server/v2 test dependency)

- [ ] **Step 3: Add test dependency if needed**

Run: `go get github.com/nats-io/nats-server/v2@latest`

- [ ] **Step 4: Commit**

```bash
git add internal/memory/pipeline/pipeline_test.go go.mod go.sum
git commit -m "test(memory-pipeline): add integration tests for JetStream streams and event round-trip"
```

---

## Task 16: Full Build Verification

- [ ] **Step 1: Run full build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 2: Run full test suite**

Run: `go test -short -race ./... -timeout 60s`
Expected: all existing tests pass, new pipeline tests pass

- [ ] **Step 3: Run go vet**

Run: `go vet ./...`
Expected: no issues

---

## Summary: Dependency Graph

```
Task 1 (Config)
    ↓
Task 2 (Migration) ─── Task 3 (Events) ─── Task 4 (JetStream)
                                                    ↓
Task 14 (ListSchemas) ──────────────────── Task 5 (Outbox Poller)
                                                    ↓
                                           Task 6 (Embedder)
                                                    ↓
                                           Task 7 (Enricher)
                                                    ↓
                                           Task 8 (Pipeline Orchestrator)
                                                    ↓
                                           Task 9 (Harness Registration)
                                                    ↓
                                    Task 10 (Metrics)  Task 11 (Injector)  Task 12 (Recall Tool)
                                                    ↓
                                           Task 13 (GetStats Refactor)
                                                    ↓
                                           Task 15 (Integration Test)
                                                    ↓
                                           Task 16 (Full Verification)
```
