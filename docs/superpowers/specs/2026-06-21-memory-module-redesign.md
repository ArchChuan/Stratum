# Memory 模块重构设计规范

- **Date**: 2026-06-21
- **Branch**: `feat/ddd-refactor`
- **Status**: Draft → Review
- **Replaces**: `docs/superpowers/specs/2026-06-14-memory-pipeline-design.md`、`docs/superpowers/specs/2026-06-13-memory-agent-loop-design.md`

---

## 0. 背景与目标

现有 memory 模块（`internal/memory/infrastructure/pipeline/{embedder,enricher,outbox_poller}.go` + `memory_entries` / `entities` / `memory_summaries`）存在以下问题：

1. **每条消息都做 LLM 富化**：吞吐压力大、token 浪费、强依赖外部 LLM
2. **memory_entries 表混合了原文、摘要、富化结果**：召回时需要在多种 type 间过滤，语义不清晰
3. **没有 fact-level 抽象**：召回的最小单元仍然是消息片段，不利于跨对话个人画像构建
4. **无遗忘机制**：`expires_at` 形同虚设，没有衰减/配额/合并/supersede 流程
5. **管理员侧不可见**：pipeline 失败、堆积、配额接近上限时运维无感知
6. **scope 缺失**：所有记忆默认全局可见，无法支持「个人偏好」vs「Agent 私域知识」的分层

### 重写动机

- **BC（bounded context）边界重画**：memory 上下文与 agent / iam / llmgateway 解耦，跨域走 port + adapter
- **从 message-centric 转向 fact-centric**：抽取出独立的「事实条目」+「实体画像」两层，召回粒度更细，遗忘成本更低
- **Mem0 / Zep 工程实践对齐**：异步 pipeline + RRF 混合召回 + frecency 衰减 + supersede 状态机

### 设计原则

- 正确 > 清晰 > 速度
- AI 不做控制逻辑：路由 / 重试 / 状态机硬编码
- 多租户 schema 隔离：所有 SQL 必带 `SET LOCAL search_path`
- DDD 单向依赖：`pkg/` 不 import `internal/`；`domain/` 零第三方依赖
- greenfield 重写，不和老代码共存

---

## 1. 整体架构

### 1.1 BC 划分

```
api/                        ← HTTP / handler
  http/handler/
    memory_handler.go       ← 用户侧（个人画像页）
    admin_memory_handler.go ← 管理员侧（诊断页）
  wiring/
    memory.go               ← 组合根

internal/memory/
  domain/
    fact.go                 ← MemoryFact 聚合根 + 状态机
    entity.go               ← Entity 聚合根（rolling profile）
    errors.go               ← Err* 哨兵
    port/
      fact_repo.go          ← FactRepo 出向接口
      entity_repo.go        ← EntityRepo
      vector_store.go       ← VectorStore
      llm_extractor.go      ← FactExtractor / EntityProfiler
      embed_client.go       ← EmbedClient
      extraction_queue.go   ← 入站队列 port
  application/
    memory_service.go       ← BuildContext / Recall / Forget 用例
    fact_extractor.go       ← Worker 编排
    profile_rebuilder.go    ← Worker 编排
    gc_worker.go            ← Worker 编排
  infrastructure/
    persistence/
      pg_fact_repo.go
      pg_entity_repo.go
      pg_extraction_queue.go
    vector/
      milvus_adapter.go
    llm/
      qwen_extractor.go     ← 实现 FactExtractor port
    embed/
      embed_adapter.go
```

### 1.2 数据流

**写路径（异步）**：

```
Agent.AddMessage (chat_store)
   │
   ├─ Redis 累计 (K=5 / T=2min) 任一触发即 flush
   ▼
memory_extraction_queue (PG, FOR UPDATE SKIP LOCKED 出队)
   │
   ▼
FactExtractorWorker
   ├─ LLM 抽取 facts + entities + importance(0.0-1.0)
   ├─ supersede 检查（trigram 找相似旧 fact + LLM 判 KEEP/SUPERSEDE）
   ├─ entity 归一化（trigram > 0.8 复用 ID）
   ├─ tx: INSERT memory_facts / UPDATE memory_entities / INSERT pending_milvus_sync
   └─ done
   │
   ▼
MilvusSyncWorker（连续 tick）
   └─ 拉 pending → embed → Milvus.Upsert → 标记 synced

ProfileRebuilderWorker（5 min tick）
   └─ 检测 entity 触发条件 → LLM 重建 profile → Milvus 同步 entity_profile 向量
```

**读路径（同步）**：

```
Agent.BuildContext(userID, agentID, query)
   │
   ├─ 查 agent.memory_read_scope（off/user/agent）
   │   off → 跳过，直接进 ReAct
   │
   ├─ 拉 memory_entities WHERE user_id 取 active profile
   │   组装为 <USER_PROFILE> 块注入 system prompt
   │
   ▼
ReAct loop（按需）
   recall_memory(query) tool
     ├─ embed(query)
     ├─ Milvus 向量检索（含 scope filter: user_id + 可选 agent_id）
     ├─ PG trigram 检索（content / keywords）
     ├─ RRF 融合（k=60）
     ├─ frecency 重排（importance × decay × log access_count）
     └─ 返回 top-N facts
```

### 1.3 BC 边界

| 边界 | 走向 | 形式 |
|---|---|---|
| memory ↔ agent | agent 调用 memory.BuildContext | agent 在自己 `domain/port/` 定 `MemoryReader` 接口，wiring thin adapter |
| memory ↔ llmgateway | memory 调 LLM 抽取 | memory 在自己 `domain/port/` 定 `LLMExtractor`，wiring 注入 llmgateway client |
| memory ↔ iam | memory 不直接依赖 | tenantID / userID 通过 ctx + 参数透传 |
| memory ↔ chat_store | chat_store 写 extraction_queue | chat_store 在 `agent/domain/port/` 定 `MemoryExtractionQueue`，memory 提供实现 |

---

## 2. 数据模型

老表全部 DROP，新建。绝不与老 pipeline 共存。

### 2.1 memory_facts（事实条目）

```sql
CREATE TABLE IF NOT EXISTS memory_facts (
    id              UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id         TEXT NOT NULL,
    agent_id        TEXT,                                -- NULL = user-scope
    scope           TEXT NOT NULL CHECK (scope IN ('user','agent')),
    content         TEXT NOT NULL,                       -- 单条事实陈述
    importance      FLOAT8 NOT NULL DEFAULT 0.5,         -- LLM 评分 0.0-1.0
    keywords        TEXT[] NOT NULL DEFAULT '{}',
    entity_refs     UUID[] NOT NULL DEFAULT '{}',        -- 引用的 entity ids
    source_msg_ids  UUID[] NOT NULL DEFAULT '{}',        -- 来源消息追溯
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','deleted','superseded','archived')),
    superseded_by   UUID REFERENCES memory_facts(id) ON DELETE SET NULL,
    access_count    INT  NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    archived_at     TIMESTAMPTZ
);
CREATE INDEX idx_memory_facts_user_active
    ON memory_facts (user_id, status, last_accessed_at DESC)
    WHERE status = 'active';
CREATE INDEX idx_memory_facts_agent_scope
    ON memory_facts (user_id, agent_id, status)
    WHERE scope = 'agent' AND status = 'active';
CREATE INDEX idx_memory_facts_content_trgm
    ON memory_facts USING GIN (content gin_trgm_ops);
CREATE INDEX idx_memory_facts_keywords
    ON memory_facts USING GIN (keywords);
CREATE UNIQUE INDEX memory_facts_one_active_supersede
    ON memory_facts (superseded_by) WHERE status = 'active';
```

### 2.2 memory_entities（实体 rolling profile）

```sql
CREATE TABLE IF NOT EXISTS memory_entities (
    id              UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id         TEXT NOT NULL,
    agent_id        TEXT,
    scope           TEXT NOT NULL CHECK (scope IN ('user','agent')),
    name            TEXT NOT NULL,
    entity_type     TEXT NOT NULL,                  -- person / project / preference / tech / location
    profile         TEXT NOT NULL DEFAULT '',       -- LLM rolling 摘要
    fact_count      INT  NOT NULL DEFAULT 0,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rebuild_after   TIMESTAMPTZ,                    -- 何时重建 profile
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','deleted')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX idx_memory_entities_uniq
    ON memory_entities (user_id, COALESCE(agent_id,''), name, entity_type)
    WHERE status = 'active';
CREATE INDEX idx_memory_entities_name_trgm
    ON memory_entities USING GIN (name gin_trgm_ops);
```

### 2.3 入站 / 维护队列

```sql
-- 抽取入站队列（替换 memory_outbox）
CREATE TABLE IF NOT EXISTS memory_extraction_queue (
    id              BIGSERIAL PRIMARY KEY,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    conversation_id UUID NOT NULL,
    message_ids     UUID[] NOT NULL,
    payload         JSONB NOT NULL,                -- {role,content}[]
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','processing','done','failed')),
    retry_count     INT  NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ
);
CREATE INDEX idx_extraction_queue_pending
    ON memory_extraction_queue (created_at)
    WHERE status = 'pending';

-- profile 重建队列
CREATE TABLE IF NOT EXISTS memory_profile_rebuild_queue (
    entity_id       UUID PRIMARY KEY REFERENCES memory_entities(id) ON DELETE CASCADE,
    scheduled_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_count     INT NOT NULL DEFAULT 0
);

-- Milvus 同步（事务一致性 outbox）
CREATE TABLE IF NOT EXISTS memory_facts_pending_milvus_sync (
    fact_id         UUID PRIMARY KEY REFERENCES memory_facts(id) ON DELETE CASCADE,
    op              TEXT NOT NULL CHECK (op IN ('upsert','delete')),
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_count     INT NOT NULL DEFAULT 0
);
```

### 2.4 agents 表扩展

```sql
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_write_scope TEXT NOT NULL DEFAULT 'user'
    CHECK (memory_write_scope IN ('off','user','agent'));
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_read_scope TEXT NOT NULL DEFAULT 'user'
    CHECK (memory_read_scope IN ('off','user','agent'));
```

### 2.5 老表清理（第一次部署）

```sql
DROP TABLE IF EXISTS memory_outbox;
DROP TABLE IF EXISTS memory_summaries;
DROP TABLE IF EXISTS memory_token_budgets;
DROP TABLE IF EXISTS memory_entries;
DROP TABLE IF EXISTS entity_relations;
DROP TABLE IF EXISTS entities;
```

### 2.6 Milvus collection 设计

每租户单 collection：`memory_facts_{tenantID}`

| 字段 | 类型 | 说明 |
|---|---|---|
| id | VARCHAR(36) | fact_id 或 entity_id |
| user_id | VARCHAR(64) | 必索引 |
| agent_id | VARCHAR(64) | 可空，scope filter |
| scope | VARCHAR(8) | user / agent |
| doc_type | VARCHAR(16) | fact / entity_profile |
| importance | FLOAT | 用于 expr 加权 |
| status | VARCHAR(16) | 仅 active 可被检索 |
| vector | FLOAT_VECTOR(1024) | embedding |

索引：HNSW，metric = COSINE，M = 16，efConstruction = 200。

---

## 3. 写入路径

### 3.1 触发与缓冲

**单一路径，去掉「即时触发」**。

`chat_store.AddMessage` 在事务内：

1. 写 `chat_messages`（已有逻辑）
2. **不再写 outbox**
3. 调 `MemoryExtractionBuffer.Append(ctx, tenantID, userID, agentID, convID, msgID, role, content)`

`MemoryExtractionBuffer`（Redis impl）：

- key: `mem:buf:{tenantID}:{userID}:{convID}`
- 累计 K=5 条 OR 距首条 T=2min → flush 到 `memory_extraction_queue`，原子 LPOP + DEL
- 失败：log warn 并丢弃（容忍少量记忆丢失）

### 3.2 三态 Agent 配置

`memory_write_scope`:

- `off`：不写。chat_store buffer 调用直接短路返回。
- `user`：facts.scope='user', agent_id=NULL。跨 agent 共享。
- `agent`：facts.scope='agent', agent_id=该 Agent ID。仅同 agent 召回。

`memory_read_scope`:

- `off`：BuildContext 短路返回空，不注入 USER_PROFILE，不注册 recall_memory 工具
- `user`：召回 facts WHERE user_id=? AND (scope='user' OR (scope='agent' AND agent_id=?))
- `agent`：召回 facts WHERE user_id=? AND scope='agent' AND agent_id=?

`memory_enabled = false`：等同于 read_scope=off + write_scope=off

### 3.3 FactExtractorWorker

**循环**：

```go
for {
    select {
    case <-ctx.Done(): return
    default:
    }

    // 单租户串行（advisory lock 防同租户并发抽取冲突）
    rows := SELECT * FROM memory_extraction_queue
            WHERE status='pending'
            ORDER BY created_at
            FOR UPDATE SKIP LOCKED
            LIMIT 10;

    for row := range rows {
        if err := process(row); err != nil {
            UPDATE memory_extraction_queue
            SET status='failed', last_error=$1, retry_count=retry_count+1
            WHERE id=$2;  // 不自动重试，admin UI 介入
            continue
        }
        UPDATE memory_extraction_queue SET status='done', processed_at=NOW() WHERE id=$1;
    }
}
```

### 3.4 LLM 抽取 prompt（单 LLM 一次返回 facts + importance）

```
You are a memory extraction system. Extract self-contained facts from
the conversation. For each fact, score importance 0.0-1.0:
  0.9-1.0: Identity, name, location, core preferences
  0.7-0.8: Long-term projects, relationships, key skills
  0.5-0.6: Tech decisions, working preferences
  0.3-0.4: Temporary context, current task details
  0.0-0.2: Smalltalk, transient

Return JSON:
{
  "facts": [
    {"content": "...", "importance": 0.85, "keywords": [...],
     "entities": [{"name": "...", "type": "..."}]}
  ]
}

Only return facts that are stable and worth remembering.
Do NOT include LLM responses or assistant explanations as facts.
Return empty array if nothing worth remembering.
```

### 3.5 Supersede 决策

```
对每条新 fact:
  similar = SELECT id, content FROM memory_facts
            WHERE user_id=$1 AND status='active'
              AND similarity(content, $new) > 0.6
            ORDER BY similarity DESC LIMIT 3;

  if len(similar) == 0: INSERT new fact

  else:
    judgment = LLM("Given existing facts and new fact, decide for each:
      KEEP / SUPERSEDE / MERGE", existing=similar, new=newFact)

    for each existing in judgment:
      if SUPERSEDE:
        UPDATE memory_facts SET status='superseded', superseded_by=$new
        WHERE id=$existing
        INSERT enqueue Milvus delete
    INSERT new fact
```

### 3.6 Entity 归一化

```sql
-- trigram 找相似名
SELECT id FROM memory_entities
WHERE user_id=$1 AND entity_type=$2
  AND status='active'
  AND similarity(name, $name) > 0.8
ORDER BY similarity DESC LIMIT 1;

-- 命中：复用 id, fact_count++, schedule rebuild if threshold
-- 未命中：INSERT 新 entity，schedule rebuild
```

### 3.7 ProfileRebuilder 触发条件

任一满足即调度 rebuild（写入 memory_profile_rebuild_queue，幂等 ON CONFLICT DO UPDATE 取最早 scheduled_at）：

- 距上次重建 ≥ 7 天
- fact_count 自上次重建增长 ≥ 5
- 或 SUPERSEDE 命中该 entity 的 fact

Worker 每 5min tick：

1. SELECT entity_id FROM memory_profile_rebuild_queue WHERE scheduled_at <= NOW() FOR UPDATE SKIP LOCKED LIMIT 20
2. 拉该 entity 最近 30 条 active fact
3. LLM rolling summarize → memory_entities.profile
4. INSERT pending_milvus_sync(doc_type='entity_profile')
5. DELETE FROM memory_profile_rebuild_queue WHERE entity_id=$1

---

## 4. 读取路径

### 4.1 BuildContext（system prompt 注入）

```go
func (s *MemoryService) BuildContext(ctx, tenantID, userID, agentID, query string) (string, error) {
    agent := s.agentRepo.Get(ctx, agentID)
    if !agent.MemoryEnabled || agent.MemoryReadScope == "off" {
        return "", nil
    }

    profiles := s.entityRepo.ListProfiles(ctx, userID, agentID, agent.MemoryReadScope, topN=10)
    if len(profiles) == 0 { return "", nil }

    var sb strings.Builder
    sb.WriteString("<USER_PROFILE>\n")
    for _, p := range profiles {
        fmt.Fprintf(&sb, "- %s (%s): %s\n", p.Name, p.EntityType, p.Profile)
    }
    sb.WriteString("</USER_PROFILE>")
    return sb.String(), nil
}
```

不污染 Agent 主 prompt，仅前置 `<USER_PROFILE>` 块。

### 4.2 recall_memory tool

ReAct 阶段按需调用，不在每轮强制注入。

```go
func RecallMemory(ctx, tenantID, userID, agentID, query string, topK int) ([]Fact, error) {
    // 1. 向量检索
    qvec, err := embed.EmbedVector(ctx, query)
    if err != nil {
        // fallback: keyword only
        return keywordSearch(ctx, query, topK)
    }
    vectorHits := milvus.Search(ctx, tenantID, qvec, scopeFilter, topK*2)

    // 2. 关键词检索
    keywordHits := pg.QueryWithTrigram(ctx, query, topK*2)

    // 3. RRF 融合 (k=60)
    fused := rrf.Fuse(vectorHits, keywordHits, k=60)

    // 4. frecency 重排
    for _, h := range fused {
        h.Score *= h.Importance
                 *  math.Exp(-0.05 * daysSince(h.LastAccessedAt))
                 *  math.Log(1 + float64(h.AccessCount))
    }
    sort.Slice(fused, byScoreDesc)

    // 5. flush access_count async
    go accessFlusher.Bump(fused[:topK].IDs())

    return fused[:topK]
}
```

scope filter 由 `memory_read_scope` 决定：

```python
# user
expr = f"user_id == '{userID}' and status == 'active' and (scope == 'user' or (scope == 'agent' and agent_id == '{agentID}'))"
# agent
expr = f"user_id == '{userID}' and scope == 'agent' and agent_id == '{agentID}' and status == 'active'"
```

### 4.3 forget_memory tool

参数 `target` 必填，`confirm` 可选。

```
target="all" + confirm=true → 删除该 user 全部 fact + entity（软删 30 天 GC）
target="<query>" 单匹配 → 直接软删
target="<query>" 多匹配 → 返回候选列表 [{id, content_preview}]，等下一轮 confirm=id1,id2
```

---

## 5. 前端设计与权限

### 5.1 用户侧（个人画像页）

`web/src/modules/settings/MemoryPanel.tsx`

- 入口：右上角用户菜单 "我的记忆"
- 内容：
  - Tab 1：实体画像（按类型分组显示 name + profile）
  - Tab 2：事实条目（分页 / 搜索 / 单条删除按钮）
  - 顶部："清空全部" 按钮 + Modal.confirm
- 默认接近隐形，Agent 表单提到 "你的对话被 AI 学习用以提升回答相关性，可在 [我的记忆] 查看与删除"

### 5.2 Agent 编辑表单新增字段

```tsx
<Form.Item label="记忆功能" name="memory_enabled" valuePropName="checked">
  <Switch />
</Form.Item>

<Form.Item label="写入范围" name="memory_write_scope" tooltip="off=不学习; user=跨Agent共享; agent=仅本Agent私有">
  <Radio.Group>
    <Radio value="off">不写入</Radio>
    <Radio value="user">用户级（推荐）</Radio>
    <Radio value="agent">Agent 私有</Radio>
  </Radio.Group>
</Form.Item>

<Form.Item label="读取范围" name="memory_read_scope">
  <Radio.Group>
    <Radio value="off">不读取</Radio>
    <Radio value="user">用户级</Radio>
    <Radio value="agent">Agent 私有</Radio>
  </Radio.Group>
</Form.Item>
```

### 5.3 管理员侧（记忆诊断页）

仅 `system_admin` / `global_admin` 可见。

`web/src/pages/admin/MemoryDiagnosticsPage.tsx`：

- Tab 1 全局指标：facts 总数 / 队列堆积 / sync 待办 / 失败率（Prometheus 直连）
- Tab 2 pipeline 状态：失败队列列表 + 重试 / 删除按钮
- Tab 3 单租户钻取：选择 tenant + user → 看其 facts / entities / 配额使用
- Tab 4 supersede 历史：可恢复（90 天窗口）

### 5.4 系统管理员权限模型（IAM 扩展）

#### 4.1 三层角色

| 层 | 字段 | 范围 |
|---|---|---|
| 全局 | `users.global_role` ∈ {`global_admin`, `user`} | 跨租户 |
| 系统派生 | `system_role` ∈ {`global_admin`, `system_admin`, `user`} | 跨租户，**JWT 派生不入库** |
| 租户 | `tenant_members.role` ∈ {`owner`, `admin`, `member`} | 单租户 |

#### 4.2 派生规则

```
global_role == 'global_admin'
   → system_role = 'global_admin'

else if 默认租户中该 user 是 admin（或 owner）
   → system_role = 'system_admin'

else
   → system_role = 'user'
```

默认租户：`tenants.is_default = TRUE` 且全局唯一。其 owner 必然是 `global_admin`，其 admin 自动获得 `system_admin`。

#### 4.3 实现位置

**新文件** `internal/iam/application/system_role.go`：

```go
func DeriveSystemRole(ctx context.Context, repo OnboardRepo, userID, globalRole string) (string, error) {
    if globalRole == "global_admin" {
        return "global_admin", nil
    }
    role, err := repo.GetDefaultTenantMemberRole(ctx, userID)
    if err != nil {
        return "user", nil  // 不在默认租户也算普通用户
    }
    if role == "admin" || role == "owner" {
        return "system_admin", nil
    }
    return "user", nil
}
```

**`internal/iam/domain/port/onboard_repo.go`** 新增：

```go
GetDefaultTenantMemberRole(ctx context.Context, userID string) (string, error)
// SQL: SELECT tm.role FROM tenant_members tm
//      JOIN tenants t ON t.id=tm.tenant_id
//      WHERE t.is_default=TRUE AND tm.user_id=$1
```

**JWT Claims** 增加 `system_role` 字段，登录 / refresh 时计算。Token TTL 60min。

**Redis 紧急踢出**：`iam:revoked:user:{userID}` set，AuthMiddleware 每请求检查。

**新中间件** `api/middleware/require_role.go::RequireSystemAdmin()`：

```go
const ctxSystemRole = "auth.system_role"
func RequireSystemAdmin() gin.HandlerFunc {
    return func(c *gin.Context) {
        role, _ := c.Get(ctxSystemRole)
        if role != "global_admin" && role != "system_admin" {
            c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
                "code": http.StatusForbidden, "message": "system admin required"})
            return
        }
        c.Next()
    }
}
```

#### 4.4 SQL 索引

`pkg/migration/sql/004_add_default_tenant_admin_index.up.sql`：

```sql
CREATE INDEX IF NOT EXISTS idx_tenant_members_user_role
ON public.tenant_members (user_id, tenant_id, role);
```

#### 4.5 路由保护

```go
admin := r.Group("/api/v1/admin/memory")
admin.Use(middleware.RequireSystemAdmin())
{
    admin.GET("/stats", handler.AdminMemoryStats)
    admin.GET("/pipeline", handler.AdminMemoryPipeline)
    admin.GET("/users/:userID/facts", handler.AdminUserFacts)
    admin.POST("/facts/:id/restore", handler.AdminRestoreFact)
}
```

#### 4.6 前端路由守卫

```tsx
const SystemAdminRoute = ({ children }) => {
  const { systemRole } = useAuth();
  if (systemRole !== 'global_admin' && systemRole !== 'system_admin') {
    return <Navigate to="/403" />;
  }
  return children;
};

<Route path="/admin/memory" element={
  <SystemAdminRoute><MemoryDiagnosticsPage /></SystemAdminRoute>
} />
```

---

## 6. 遗忘与维护

### 6.1 三层遗忘

1. **主动删除**：用户在 MemoryPanel 删除 / Agent forget_memory tool。软删 status='deleted' + deleted_at。30 天后硬删。
2. **自然衰减**：frecency 公式让低分老条目排名靠后，不物理删。
3. **强制 GC**：超期 / 容量超限。

### 6.2 状态机

```
active ─────► deleted (30d 后硬删)
   │
   ├──────► superseded (90d 后硬删，UI 可手动恢复)
   │
   └──────► archived (90d 后硬删，importance<0.3 + 60天冷)

archived ─manual─► active (恢复)
```

CHECK 约束：

```sql
ALTER TABLE memory_facts ADD CONSTRAINT memory_facts_status_check
    CHECK (status IN ('active','deleted','superseded','archived'));
```

### 6.3 Worker 列表

| Worker | 周期 | 职责 |
|---|---|---|
| FactExtractorWorker | 连续 fetch | 队列 → fact + entity |
| MilvusSyncWorker | 连续 fetch | pending_milvus_sync → upsert/delete |
| ProfileRebuilderWorker | 5 min tick | rebuild_queue → LLM rolling profile |
| GCWorker | 1 h tick | 状态转换 + 硬删 |
| QuotaEnforcer | 6 h tick | 容量超限压缩 |
| AccessCountFlusher | 30s 或 batch=500 | 召回命中 → access_count++ |
| ExtractionQueueGC | 24 h tick | 7 天前 done 行删除 |

### 6.4 GC SQL

```sql
-- 硬删超期软删
DELETE FROM memory_facts
WHERE (status='deleted'    AND deleted_at < NOW()- INTERVAL '30 days')
   OR (status='superseded' AND updated_at < NOW()- INTERVAL '90 days')
   OR (status='archived'   AND archived_at < NOW()- INTERVAL '90 days');

-- 主动 archive 低质冷门
UPDATE memory_facts
SET status='archived', archived_at=NOW()
WHERE status='active'
  AND importance < 0.3
  AND last_accessed_at < NOW()- INTERVAL '60 days'
  AND access_count < 3;
```

### 6.5 配额

```
const FactQuotaPerUser   = 5000
const EntityQuotaPerUser = 500
```

QuotaEnforcer 单租户 advisory lock，超限按 frecency 升序删到 90%。

### 6.6 forget_memory 工具

```
target="all" confirm=true → soft delete all + cascade entity
target="<text>" 单匹配 → soft delete + 入 pending_milvus_sync(delete)
target="<text>" 多匹配 → 返回 ≤5 候选 [{id, preview}]，要求 confirm=id1,id2
```

### 6.7 Entity 级联

fact soft delete 时检查 entity_refs：若 entity 的 fact_count 降到 0 且 6 hours 无新 fact → entity status='deleted'。

### 6.8 Supersede 恢复

90 天窗口内 admin UI 可见 superseded facts，可手动 status='active' + superseded_by=NULL。

### 6.9 AccessCountFlusher

```go
type bumpEvent struct{ FactID string; AccessedAt time.Time }
ch := make(chan bumpEvent, 10000)

go func() {
  ticker := time.NewTicker(30 * time.Second)
  buf := make([]bumpEvent, 0, 500)
  flush := func() {
    if len(buf) == 0 { return }
    UPDATE memory_facts SET access_count = access_count + 1, last_accessed_at = v.ts
      FROM (VALUES ...) AS v(id, ts) WHERE memory_facts.id = v.id::uuid;
    buf = buf[:0]
  }
  for {
    select {
    case ev := <-ch:
      buf = append(buf, ev)
      if len(buf) >= 500 { flush() }
    case <-ticker.C: flush()
    case <-ctx.Done(): flush(); return
    }
  }
}()
```

### 6.10 监控指标

```
memory_facts_total{tenant, status}
memory_extraction_queue_lag_seconds{tenant}
memory_milvus_sync_pending{tenant}
memory_gc_deleted_total{tenant, reason}
memory_quota_used_ratio{tenant, user}
memory_recall_latency_seconds_bucket
memory_extraction_failed_total{tenant}
```

### 6.11 常量集中（pkg/constants/memory.go）

```go
const (
    // 缓冲与 batch
    MemoryBufferFlushSize     = 5
    MemoryBufferFlushInterval = 2 * time.Minute

    // 召回
    MemoryRecallTopK           = 10
    MemoryRecallVectorMultiple = 2
    MemoryRRFK                 = 60
    MemoryFrecencyLambda       = 0.05
    MemoryProfileTopN          = 10

    // GC
    MemorySoftDeleteRetention   = 30 * 24 * time.Hour
    MemorySupersededRetention   = 90 * 24 * time.Hour
    MemoryArchivedRetention     = 90 * 24 * time.Hour
    MemoryArchiveImportanceMax  = 0.3
    MemoryArchiveColdDays       = 60
    MemoryArchiveAccessMax      = 3

    // 配额
    MemoryFactQuotaPerUser      = 5000
    MemoryEntityQuotaPerUser    = 500
    MemoryQuotaCompressionRatio = 0.9

    // Worker tick
    MemoryProfileRebuildTick    = 5 * time.Minute
    MemoryGCTick                = 1 * time.Hour
    MemoryQuotaEnforcerTick     = 6 * time.Hour
    MemoryQueueGCTick           = 24 * time.Hour

    // ProfileRebuild 触发
    MemoryProfileRebuildMinDays  = 7
    MemoryProfileRebuildFactDelta = 5

    // LLM 超时
    MemoryExtractLLMTimeout  = 30 * time.Second
    MemoryProfileLLMTimeout  = 30 * time.Second
    MemorySupersedeLLMTimeout = 20 * time.Second

    // AccessFlusher
    MemoryAccessFlushInterval = 30 * time.Second
    MemoryAccessFlushBatch    = 500

    // Entity 归一化
    MemoryEntitySimilarityMin = 0.8

    // Supersede 候选
    MemorySupersedeCandidateMin = 0.6
    MemorySupersedeCandidateMax = 3
)
```

---

## 7. 错误处理与测试策略

### 7.1 错误分层

```
domain → infrastructure → application → middleware → HTTP
```

#### Domain 错误（`internal/memory/domain/errors.go`）

```go
var (
    ErrFactNotFound        = errors.New("memory: fact not found")
    ErrEntityNotFound      = errors.New("memory: entity not found")
    ErrAgentMemoryDisabled = errors.New("memory: agent has memory disabled")
    ErrScopeMismatch       = errors.New("memory: scope mismatch")
    ErrUserIDMismatch      = errors.New("memory: user_id required")
    ErrFactQuotaExceeded   = errors.New("memory: fact quota exceeded")
    ErrFactAlreadyDeleted  = errors.New("memory: fact already deleted")
    ErrInvalidStatus       = errors.New("memory: invalid status transition")
    ErrEmptyContent        = errors.New("memory: empty content")
    ErrEmbeddingDimension  = errors.New("memory: embedding dimension mismatch")
)
```

#### Infrastructure 翻译

```go
if errors.Is(err, pgx.ErrNoRows) { return nil, domain.ErrFactNotFound }
var pgErr *pgconn.PgError
if errors.As(err, &pgErr) {
    switch pgErr.Code {
    case "23505": return nil, domain.ErrInvalidStatus
    case "57014": return nil, fmt.Errorf("query timeout: %w", err)
    }
}
```

#### Middleware 映射（扩展 `api/http/middleware/error_handler.go`）

| 错误 | HTTP |
|---|---|
| `ErrFactNotFound` / `ErrEntityNotFound` | 404 |
| `ErrAgentMemoryDisabled` / `ErrScopeMismatch` | 403 |
| `ErrFactQuotaExceeded` | 429 |
| `ErrEmptyContent` / `ErrEmbeddingDimension` | 400 |
| 其他 | 500 |

### 7.2 异常路径处理矩阵

| 路径 | 异常 | 处理 | 用户感知 |
|---|---|---|---|
| 写入 | LLM 提取失败 | NATS Nak + 重试 3 次 → status='failed' | 无 |
| 写入 | LLM 返回非 JSON | log error + Nak | 无 |
| 写入 | embed 超时 | 仅 PG 落盘，pending_milvus_sync 等下次 | 无 |
| 写入 | Milvus 不可用 | 同上，召回降级 keyword | 召回精度短暂下降 |
| 写入 | PG 死锁 | tx rollback + 退避 3 次 | 无 |
| 写入 | extraction_queue 满 | log warn 跳过 | 无 |
| 召回 | embed 超时（500ms） | fallback keyword | 召回精度下降 |
| 召回 | Milvus 失败 | fallback keyword | 召回精度下降 |
| 召回 | PG 慢查 | 1s 超时 + 返回空 USER_PROFILE | Agent 失去记忆上下文 |
| 召回 | 0 命中 | 返回空块，正常 ReAct | 无 |
| 召回 | scope 越权 | middleware 401/403 | 错误页 |
| 维护 | GC 中崩溃 | tx rollback 下次 tick 继续 | 无 |
| 维护 | Milvus 删失败 | log error，PG 已 deleted 不影响（status filter） | 无 |
| 维护 | profile 重建 LLM 失败 | scheduled_at +1h，3 次后人工 | profile 略陈旧 |
| 维护 | 配额计算 race | advisory lock 单租户串行 | 无 |

### 7.3 关键不变量

1. `memory_facts.status='active'` ⇒ Milvus 必有对应向量（除 pending 期间）
2. `memory_entities.profile != ''` ⇒ Milvus 必有 entity_profile 向量
3. `user_id` 永不为 NULL，所有 query 双向校验
4. status 转移仅允许：active → deleted/superseded/archived；archived → active
5. 同一 entity 同时仅一个 profile_rebuild 运行（advisory lock）
6. 同一 fact 不同时被两个 supersede 决策覆盖（unique partial index）
7. 跨租户绝对隔离：`SET LOCAL search_path` 强制
8. scope='agent' 仅同 agent_id 召回（SQL WHERE 强制）

### 7.4 测试策略

| 层 | 工具 | 目标 |
|---|---|---|
| Domain 单元 | go test | frecency 公式、状态机、scope 过滤 |
| Application 单元 | go test + mockery | 用例编排，mock 所有 port |
| Infrastructure 集成 | testcontainers (pg + milvus + nats) | SQL/向量/MQ 真实交互 |
| HTTP 契约 | golden file | API 响应稳定性 |
| 端到端 | docker-compose 全栈 | 完整对话→提取→召回链路 |
| 前端组件 | vitest + @testing-library | MemoryPanel / Agent 表单 / SystemAdminRoute |

#### 关键测试用例

**Domain**：

- TestFrecencyScore_FreshHighImportance / OldHighImportance / FrequentLowImportance / HalfLife14Days
- TestStatus_ActiveToDeleted_Allowed / DeletedToActive_Forbidden / ArchivedToActive_AllowedManualOnly
- TestScope_UserFact_VisibleToAllAgents / AgentFact_OnlySameAgent / OffWriteScope_NoExtraction

**Application**：

- TestBuildContext_NoMemory_ReturnsEmpty / WithUserProfile_Injects / FallbackMilvusDown / ReadScopeOff_ShortCircuits
- TestExtractFacts_Success / LLMReturnsInvalidJSON_Naks / SupersedeDetected_MarksOldSuperseded / SimilarEntity_ReusesID
- TestForgetMemory_TargetAll_RequiresConfirm / AmbiguousMatch_ReturnsCandidates / SingleMatch_DeletesImmediately

**Infrastructure**：

- TestPgFactRepo_InsertWithMilvusOutbox_Atomic
- TestPgFactRepo_FullTextSearch_TrigramHits
- TestMilvusVectorAdapter_TenantIsolation_NoLeakage
- TestMilvusVectorAdapter_ScopeFilter_AgentScoped
- TestExtractionQueue_BatchPolling_FOR_UPDATE_SKIP_LOCKED
- TestProfileRebuilder_SameEntity_DeduplicateInWindow

**Contract（golden file）**：

```
testdata/contracts/
  memory_get_profile.golden.json
  memory_list_facts.golden.json
  memory_delete_fact.golden.json
  admin_memory_stats.golden.json
  admin_memory_pipeline.golden.json
```

**端到端**：

- TestE2E_ChatToMemory_FullPipeline
- TestE2E_ForgetMemory_RemovesFromRecall
- TestE2E_AgentScopeIsolation

### 7.5 覆盖率目标

| 包 | 目标 |
|---|---|
| `internal/memory/domain` | ≥95% |
| `internal/memory/application` | ≥85% |
| `internal/memory/infrastructure/persistence` | ≥80% |
| `internal/memory/infrastructure/pipeline` | ≥80% |
| `api/http/handler/memory_*` | ≥80% |
| `web/src/modules/settings/MemoryPanel` | ≥70% |

### 7.6 灰度与回滚

**部署节奏**：

```
Phase 1: 后端 + 数据库迁移
  - drop 老表 + create 新表
  - 部署 worker
  - 验证 pipeline 入队/出队/落地正常

Phase 2: 前端发布
  - 用户设置页 / Agent 表单 / 诊断页 同步上线
  - feature flag: ENABLE_MEMORY_UI（默认灰度租户 true）

Phase 3: 全量
  - 移除 feature flag，Agent 默认 memory_enabled=true
```

**回滚**：

| 故障 | 动作 |
|---|---|
| pipeline 阻塞 / OOM | `agents.memory_enabled=false` 全租户停写 |
| Milvus 数据损坏 | DROP collection + 后台 worker 从 PG 重建 |
| LLM 提取质量差 | 调高 GC importance 阈值快速归档低质 |
| 召回延迟突增 | feature flag 关 BuildContext 注入，仅保留 recall_memory |
| schema 迁移失败 | golang-migrate force version → down → up |

### 7.7 性能基线

| 指标 | SLA |
|---|---|
| chat_store.AddMessage 延迟（p99） | ≤ 50ms |
| BuildContext 延迟（p99） | ≤ 400ms |
| recall_memory 延迟（p99） | ≤ 300ms |
| Fact Extractor 单条（p99） | ≤ 5s |
| Profile Rebuilder 单 entity | ≤ 8s |
| extraction_queue 堆积稳态 | ≤ 100 行 |
| pending_milvus_sync 堆积稳态 | ≤ 50 行 |
| GCWorker 单租户耗时 | ≤ 30s |

### 7.8 安全检查清单

- 不记录 PII / token / api_key（log 字段白名单 review）
- 不打印 LLM 原始 response（仅 model + status + token_count）
- Milvus 跨租户 collection-per-tenant 隔离
- PG `SET LOCAL search_path` 强制
- 所有 query 必含 user_id 条件
- forget_memory 多候选必须 confirm
- forget all 强制 confirm=true
- JWT system_role 派生在 sign / refresh
- 紧急踢出 revoke set TTL 60min
- API 输入校验：content 长度上限、target 字符集

---

## 8. 实施阶段（粗粒度，由 writing-plans 细化）

| Phase | 内容 |
|---|---|
| P1 | DB schema + constants + domain port + 错误哨兵 |
| P2 | 老表 drop migration + 新表 + Milvus collection 重建脚本 |
| P3 | infrastructure 层（pg_fact_repo / pg_entity_repo / milvus_adapter / qwen_extractor） |
| P4 | application 层（MemoryService.BuildContext / Recall / Forget） |
| P5 | Worker 实现（FactExtractor / MilvusSync / ProfileRebuilder / GC / QuotaEnforcer / AccessFlusher） |
| P6 | chat_store 接入 buffer + Agent ports 接入 BuildContext |
| P7 | IAM `system_role` 派生 + `RequireSystemAdmin()` + JWT claim 改造 |
| P8 | HTTP handler（memory_handler / admin_memory_handler）+ contract golden |
| P9 | 前端：MemoryPanel + Agent 表单字段 + MemoryDiagnosticsPage + SystemAdminRoute |
| P10 | E2E 测试 + Prometheus dashboard + 回滚手册验证 |

---

## 附：决策记录

| # | 决策 | 替代方案 | 理由 |
|---|---|---|---|
| 1 | greenfield 重写而非渐进迁移 | 双轨运行 | 老 message-centric 与新 fact-centric 不兼容，混跑增加复杂度 |
| 2 | 单批量路径，去掉即时触发 | 关键词正则即时 | 正则准确率低（false-pos / false-neg）；K=5 / T=2min 足够低延迟 |
| 3 | importance 由 LLM 评分 | Agent 自报 | 不污染主 prompt，避免 Agent 错答 |
| 4 | RRF 融合（k=60） | reranker 模型 | reranker 增加延迟和成本，RRF 在 fact 粒度足够 |
| 5 | frecency λ=0.05 | 简单 importance 排序 | λ=0.05 半衰期 ≈14 天，匹配会话粒度 |
| 6 | trigram 0.8 entity 归一化 | Levenshtein / embedding | trigram 在 PG 内置，零额外服务，对中文 / 英文 / 缩写都鲁棒 |
| 7 | system_role 派生不入库 | 加 users.system_role 字段 | 派生规则可能演化，存字段会有同步 race；JWT TTL 60min 足够 |
| 8 | Milvus collection per tenant | 单 collection + 字段 filter | 强隔离，删租户直接 drop collection |
| 9 | 软删 30 天 → 硬删 | 立即硬删 | 用户后悔窗口 + admin 取证 |
| 10 | UI 几乎隐形 | 显式记忆开关每会话 | 降低决策疲劳，重要信息再让用户管 |
| 11 | 全用户级共享 | per-conversation 隔离 | 跨会话连续性是核心价值 |
