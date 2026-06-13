# Memory Pipeline 设计：异步记忆处理系统

## 概述

将记忆系统从 ChatStore 的同步双写解耦为异步下游 pipeline。ChatStore 是 source of truth，MemoryManager 作为派生索引层，通过 NATS JetStream 消息队列连接，实现 embedding、实体抽取、重要度评分和会话摘要。

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 发布时机 | Outbox 模式 | 同事务保证不丢消息，NATS 不可用时不影响核心对话 |
| 处理步骤 | 全套（Embed + Entity + Importance + Summary） | 最大化记忆系统价值 |
| Extraction 方式 | LLM structured output | 质量远超规则方案，一次调用完成多任务 |
| Summary 触发 | Token 预算驱动（累计 ~4K token） | 适配长短消息差异，逻辑封闭 |
| 消费模式 | 同进程可选 + 独立部署 | 开发简单，生产可扩展 |
| 失败处理 | JetStream NAK + MaxDeliver + DLQ | 利用原生能力，代码最简 |
| Pipeline 架构 | 双 Stage（Embed → Enrich） | Embed 快先落库支持搜索，LLM 慢不阻塞 |
| 记忆作用域 | 分层（User×Agent / User / Tenant） | 灵活覆盖私有+个人+组织三层需求 |
| Agent 注入 | 混合（始终注入 summary+实体 + recall tool 按需深查） | 低成本基础感知 + 按需精确检索 |

## 架构总览

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Agent Execute                                 │
│  ChatStore.AddMessage (single PG tx)                                │
│    ├── INSERT INTO messages                                         │
│    └── INSERT INTO memory_outbox                                    │
└────────────────────────────┬────────────────────────────────────────┘
                             │
                   ┌─────────▼──────────┐
                   │  Outbox Poller      │
                   │  (1s tick, batch 50)│
                   │  SELECT FOR UPDATE  │
                   │  SKIP LOCKED →      │
                   │  Publish → DELETE   │
                   └─────────┬──────────┘
                             │ NATS JetStream
                             │ Stream: MEMORY_RAW
                             │ Subject: memory.raw.{tenant_id}
                   ┌─────────▼──────────┐
                   │  Stage 1: Embedder  │
                   │  Consumer: "embed"  │
                   │                     │
                   │  1. EmbedVector()   │
                   │  2. Store → Milvus  │
                   │  3. Publish →       │
                   │     MEMORY_ENRICHED │
                   │  4. ACK            │
                   └─────────┬──────────┘
                             │ Stream: MEMORY_ENRICHED
                             │ Subject: memory.enriched.{tenant_id}
                   ┌─────────▼──────────┐
                   │  Stage 2: Enricher  │
                   │  Consumer: "enrich" │
                   │                     │
                   │  1. LLM structured  │
                   │     → entities +    │
                   │       importance +  │
                   │       token_estimate│
                   │  2. Check summary   │
                   │     trigger         │
                   │  3. Store → PG      │
                   │  4. ACK            │
                   └────────────────────┘
```

## 详细设计

### 1. Outbox 表与 Poller

**表结构**（每个 tenant schema 下）：

```sql
CREATE TABLE memory_outbox (
    id          BIGSERIAL PRIMARY KEY,
    message_id  TEXT NOT NULL,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_memory_outbox_created ON memory_outbox (created_at);
```

**Payload 格式**：

```json
{
  "message_id": "uuid",
  "conversation_id": "conv-uuid",
  "tenant_id": "tenant-123",
  "user_id": "user-456",
  "agent_id": "agent-789",
  "role": "user|agent",
  "content": "消息文本",
  "created_at": "2026-06-14T00:00:00Z"
}
```

**Poller 逻辑**：

```
每 1s tick:
  BEGIN
    rows = SELECT * FROM memory_outbox ORDER BY id LIMIT 50 FOR UPDATE SKIP LOCKED
    for each row:
      publish → NATS subject "memory.raw.{tenant_id}"
      DELETE row
  COMMIT
```

- `FOR UPDATE SKIP LOCKED`：多实例安全，无锁竞争
- Batch 50：吞吐量与延迟平衡
- 发布失败 → 事务回滚，下次重试

**JetStream Stream 配置**：

```
Stream: MEMORY_RAW
  Subjects: ["memory.raw.>"]
  Retention: WorkQueue
  MaxAge: 72h
  Replicas: 1 (dev) / 3 (prod)

Stream: MEMORY_ENRICHED
  Subjects: ["memory.enriched.>"]
  Retention: WorkQueue
  MaxAge: 72h
  Replicas: 1 (dev) / 3 (prod)

Stream: MEMORY_DLQ
  Subjects: ["memory.dlq.>"]
  Retention: Limits
  MaxAge: 168h (7 days)
```

### 2. Stage 1: Embedder

**Consumer 配置**：

| 参数 | 值 |
|------|-----|
| Stream | MEMORY_RAW |
| Consumer Name | embed-worker |
| Durable | Yes |
| AckWait | 30s |
| MaxDeliver | 5 |
| DeliverPolicy | All |
| FilterSubject | memory.raw.> |

**处理流程**：

```
收到消息 msg:
  1. 解析 payload → MemoryRawEvent
  2. vector = EmbeddingService.EmbedVector(ctx, content)
  3. Milvus.Insert(collection="memory_{tenant_id}", {
       id, conversation_id, user_id, agent_id, role, content, vector, created_at
     })
  4. Publish → "memory.enriched.{tenant_id}" (原始 payload + vector_id)
  5. msg.Ack()

失败处理:
  - EmbedVector 超时/错误 → msg.Nak() (JetStream 自动重投递)
  - 达到 MaxDeliver(5) → 自动进入 MEMORY_DLQ
```

**日志**：

```
memory.embed.start   → DEBUG: message_id, tenant_id, content_length
memory.embed.success → INFO:  message_id, tenant_id, vector_dim, latency_ms
memory.embed.error   → ERROR: message_id, tenant_id, error, attempt
```

### 3. Stage 2: Enricher

**Consumer 配置**：

| 参数 | 值 |
|------|-----|
| Stream | MEMORY_ENRICHED |
| Consumer Name | enrich-worker |
| Durable | Yes |
| AckWait | 60s |
| MaxDeliver | 5 |
| DeliverPolicy | All |
| FilterSubject | memory.enriched.> |

**LLM Structured Output Prompt**：

```
Analyze this conversation message and extract:
1. Named entities (people, products, concepts, locations) with type and confidence
2. Importance score (0.0-1.0) based on information density and future relevance
3. Estimated token count of the content

Respond in JSON:
{
  "entities": [{"name": "...", "type": "person|product|concept|location|org", "confidence": 0.0-1.0}],
  "importance": 0.0-1.0,
  "token_estimate": 123,
  "keywords": ["...", "..."]
}
```

**处理流程**：

```
收到消息 msg:
  1. 解析 payload → MemoryEnrichedEvent
  2. enrichment = LLMGateway.Complete(ctx, enrichPrompt + content) → structured JSON
  3. 解析 enrichment → entities, importance, token_estimate, keywords
  4. BEGIN transaction:
     a. UPDATE memory_entries SET importance, keywords, enriched_at
     b. UPSERT entities → memory_entities 表
     c. UPDATE conversation_token_budget += token_estimate
     d. IF conversation_token_budget >= 4096:
          summary = LLMGateway.Complete(ctx, summarizePrompt + recent_messages)
          UPSERT memory_summaries (conversation_id, summary, covered_until)
          RESET conversation_token_budget = 0
     COMMIT
  5. msg.Ack()

失败处理:
  - LLM 调用超时 → msg.Nak()
  - JSON 解析失败 → 重试一次（MaxDeliver 容许），仍失败 → DLQ
```

**日志**：

```
memory.enrich.start    → DEBUG: message_id, tenant_id
memory.enrich.llm      → INFO:  message_id, model, prompt_tokens, completion_tokens, latency_ms
memory.enrich.entities → INFO:  message_id, entity_count, importance
memory.enrich.summary  → INFO:  conversation_id, token_budget_before, summary_length
memory.enrich.error    → ERROR: message_id, tenant_id, stage, error, attempt
```

### 4. 记忆作用域（分层模型）

```
┌─────────────────────────────────────────────┐
│ Layer 3: Tenant Shared Memory               │
│ scope: tenant_id                            │
│ 内容: 组织知识、共享实体、全局偏好          │
├─────────────────────────────────────────────┤
│ Layer 2: User Cross-Agent Memory            │
│ scope: tenant_id + user_id                  │
│ 内容: 用户偏好、跨 agent 实体、个人摘要     │
├─────────────────────────────────────────────┤
│ Layer 1: User × Agent Private Memory        │
│ scope: tenant_id + user_id + agent_id       │
│ 内容: 对话历史摘要、私有实体、上下文偏好    │
└─────────────────────────────────────────────┘
```

**写入规则**：

| 数据类型 | 目标层 | 条件 |
|----------|--------|------|
| 对话摘要 | L1 | 始终（per agent × user） |
| 实体（置信度 < 0.8） | L1 | 首次出现 |
| 实体（置信度 ≥ 0.8，出现 ≥ 3 次） | L2 提升 | 跨会话重复 |
| 实体（被 ≥ 3 个用户提及） | L3 提升 | 跨用户重复 |
| 用户偏好 | L2 | LLM 标记为 preference |
| 组织级知识 | L3 | Admin API 或自动提升 |

**查询优先级**（Agent 执行时）：

```
1. L1 (User × Agent)  → 最相关，最优先
2. L2 (User)           → 补充个人上下文
3. L3 (Tenant)         → 组织级兜底
```

### 5. Agent 记忆注入

**Always-inject（每次 Agent 执行前）**：

```go
// 注入最近摘要 + top 实体
injected := fmt.Sprintf(
  "[Memory Context]\nSummary: %s\nKey Entities: %s\n",
  latestSummary,
  strings.Join(topEntityNames, ", "),
)
systemPrompt = injected + originalSystemPrompt
```

- 从 L1 取当前 agent 的最近 summary
- 从 L1+L2 取 top 10 entities（按 last_seen 排序）
- Token 预算：≤ 500 token（硬限）

**recall_memory Tool（按需深查）**：

```json
{
  "name": "recall_memory",
  "description": "Search long-term memory for relevant past context",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": {"type": "string", "description": "Search query"},
      "scope": {"type": "string", "enum": ["private", "personal", "shared"], "default": "private"},
      "limit": {"type": "integer", "default": 5, "maximum": 20}
    },
    "required": ["query"]
  }
}
```

Scope 映射：

- `private` → L1（User × Agent）
- `personal` → L1 + L2
- `shared` → L1 + L2 + L3

### 6. 可观测性

**Prometheus Metrics**：

| Metric | Type | Labels |
|--------|------|--------|
| `memory_outbox_pending` | Gauge | tenant_id |
| `memory_outbox_published_total` | Counter | tenant_id, status |
| `memory_embed_duration_seconds` | Histogram | tenant_id |
| `memory_embed_total` | Counter | tenant_id, status |
| `memory_enrich_duration_seconds` | Histogram | tenant_id |
| `memory_enrich_total` | Counter | tenant_id, status |
| `memory_summary_triggered_total` | Counter | tenant_id |
| `memory_dlq_total` | Counter | tenant_id, stage |
| `memory_entities_extracted_total` | Counter | tenant_id |
| `memory_injection_duration_seconds` | Histogram | tenant_id |

**Health Check**：

```
GET /health/memory-pipeline
{
  "outbox_lag": 12,           // pending rows
  "embed_consumer_ok": true,
  "enrich_consumer_ok": true,
  "dlq_count": 0
}
```

### 7. 配置

```yaml
memory:
  pipeline:
    enabled: true                    # 总开关
    mode: "in-process"               # "in-process" | "standalone"
    outbox:
      poll_interval: "1s"
      batch_size: 50
    jetstream:
      url: "nats://localhost:4222"
      raw_stream: "MEMORY_RAW"
      enriched_stream: "MEMORY_ENRICHED"
      dlq_stream: "MEMORY_DLQ"
      max_deliver: 5
    embedder:
      workers: 2
      ack_wait: "30s"
    enricher:
      workers: 1
      ack_wait: "60s"
      model: "gpt-4o-mini"           # 用于 entity extraction
      summary_model: "gpt-4o-mini"   # 用于 summary generation
      summary_token_threshold: 4096
    injection:
      max_tokens: 500
      top_entities: 10
    scoping:
      l2_promote_threshold: 3        # entity 出现次数提升到 L2
      l3_promote_threshold: 3        # 跨用户提及次数提升到 L3
```

### 8. 数据库 Schema（PG，per tenant）

```sql
-- 记忆条目（enriched 后的完整记录）
CREATE TABLE memory_entries (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    role            TEXT NOT NULL,
    content         TEXT NOT NULL,
    importance      REAL DEFAULT 0,
    keywords        TEXT[] DEFAULT '{}',
    token_estimate  INT DEFAULT 0,
    scope_layer     INT DEFAULT 1,  -- 1=private, 2=personal, 3=shared
    enriched_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_memory_entries_conv ON memory_entries (conversation_id, created_at);
CREATE INDEX idx_memory_entries_user_agent ON memory_entries (user_id, agent_id, created_at DESC);

-- 实体
CREATE TABLE memory_entities (
    id          TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    user_id     TEXT,
    agent_id    TEXT,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL,
    confidence  REAL DEFAULT 0,
    scope_layer INT DEFAULT 1,
    occurrence_count INT DEFAULT 1,
    first_seen  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    attributes  JSONB DEFAULT '{}'
);
CREATE INDEX idx_memory_entities_scope ON memory_entities (tenant_id, user_id, agent_id, scope_layer);
CREATE UNIQUE INDEX idx_memory_entities_name ON memory_entities (tenant_id, user_id, COALESCE(agent_id, ''), name, type);

-- 会话摘要
CREATE TABLE memory_summaries (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    summary         TEXT NOT NULL,
    covered_until   TIMESTAMPTZ NOT NULL,
    token_count     INT DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_memory_summaries_conv ON memory_summaries (conversation_id, created_at DESC);

-- Token 预算追踪
CREATE TABLE memory_token_budgets (
    conversation_id TEXT PRIMARY KEY,
    accumulated     INT DEFAULT 0,
    last_reset_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 9. 实现顺序

| Phase | 内容 | 依赖 |
|-------|------|------|
| P0 | DB migration + outbox 表 + poller | ChatStore.AddMessage 改为同事务写 outbox |
| P1 | JetStream 连接 + stream 创建 + Embedder consumer | Milvus collection ready |
| P2 | Enricher consumer + LLM structured output | LLMGateway |
| P3 | Summary trigger + memory_summaries | P2 |
| P4 | 分层 scoping + entity promotion | P2 |
| P5 | Agent injection + recall_memory tool | P3 + P4 |
| P6 | 前端 MemoryPage 接真实数据 + GetStats 重构 | P2 |
| P7 | Prometheus metrics + health endpoint + Grafana dashboard | P1 |

### 10. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| LLM 调用延迟高 | Enricher 积压 | AckWait 60s + workers 可扩 + summary 异步 |
| Milvus 不可用 | Embedder 阻塞 | NAK 重试 + DLQ + 降级（跳过向量，纯文本搜索） |
| Outbox 膨胀 | PG 空间 | 发布即删 + 监控 pending gauge |
| JetStream 重复投递 | 重复处理 | Milvus upsert + PG ON CONFLICT |
| Token 预算漂移 | Summary 提前/延后 | 允许 ±20% 容差，重启时重算 |
