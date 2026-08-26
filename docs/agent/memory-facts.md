# Memory 边界（Facts / Active Snapshot / History）

Phase 0 加固既有 Facts 切片；Phase 1 为每个 tenant/user/agent 元组增加一个活跃短期快照；Phase 2 把既有 tenant-local `memory_summaries` 演化为动态长期 History，不新增平行记忆表。以下先列 Facts 边界，再列快照与 History 分层。

## Facts 边界

Phase 0 hardens the existing Facts slice only. It does not add User or History memory.

- LLM extraction keeps omitted confidence backward-compatible by using importance, while explicit `0.0` remains zero and is filtered before persistence.
- Persisted facts carry an allowlisted category, confidence in `[0,1]`, source type, and `conversation_id`. Message DTOs do not expose a safe message ID, so provenance stops at the conversation reference and never stores message payloads.
- Extraction retains facts by confidence then importance, with stable ties and a per-round cap. Explicit corrections continue through the existing supersede flow; source is provenance, not an automatic conflict winner.
- Injection reads active facts above the confidence threshold and ranks by query relevance, frecency, confidence, then importance. Category-labelled output is bounded by the centralized Facts Top-N, timeout, and character budget constants.
- Explicit user-created facts use `explicit_user` provenance and confidence `1.0`, so archival protection does not depend on their importance score.
- The existing `memory_facts_*` Milvus collection is preserved without drop/recreate. Scope uses its existing scalar field; an allowlisted, length-checked JSON payload in the existing `source_document` field persists conversation, importance, category, confidence, and source metadata without copying arbitrary keys.
- Tenant Facts DDL remains canonical in `pkg/storage/postgres/tenant_schema.sql`; migration `020` is a paired public-schema marker only.

## Active snapshot（Phase 1）

`memory_active_snapshots` 是 tenant-schema 表。其唯一 `(user_id, agent_id)` 键让刷新覆盖旧快照并递增 `version`。结构化 `work_context`、`personal_context`、`top_of_mind` 数组在领域内限制为每段 8 项、每项 240 runes、总计 1200 runes（`ActiveSnapshotSectionMaxItems` / `ActiveSnapshotItemMaxRunes` / `ActiveSnapshotTotalMaxRunes`）。source provenance 只含类型与引用；pipeline 存产生消息的 ID，不存消息 payload。

快照活跃 24 小时（`ActiveSnapshotTTL`）。Repository 读取要求 `status = 'active'` 且 `expires_at > NOW()`。支持显式范围删除；user/agent 记忆生命周期删除也会移除其持有的快照。所有操作都在既有 tenant `search_path` 事务模式内执行。

### 数据流

Chat 继续写既有 memory outbox。原始事件经既有 embedder/enricher workers。enricher 在其既有结构化响应中索要有界活跃上下文字段，且仅在正常富化持久化后 upsert 快照。快照刷新是可选派生状态：缺失源事件时间戳则跳过，校验或持久化失败仅记日志+指标，已持久化的富化继续 Ack，避免为快照失败重放 LLM 与核心富化。快照排序用源事件时间，仅当该时间严格更新时 repository 才更新——过期或相等的重试不能覆盖上下文、延长 TTL 或递增 `version`。

### 注入

injector 以 500ms 超时（`ActiveSnapshotReadTimeout`）读快照，错误当作空快照。渲染共享 1800 字符上限。活跃快照内容先渲染（600 字符配额 `ActiveSnapshotInjectionBudget`），质量过滤后的 Facts 紧随其后，相关 History 在 legacy 会话摘要/实体用余量前获得共享预算的预留份额。History 检索限制 3 行 500 字符，先按 trigram 相关性再按重要性、置信度、recency 排序，只接受 user scope 与当前 agent scope。其 500ms 读超时与查询错误降级为无 History。所有段落仍在同一 1800 字符总量内；source 引用与过期元数据不注入。

## Dynamic History（Phase 2）

`memory_summaries` 保持 legacy 会话摘要行兼容并增加 nullable History 元数据。History 行携带 user/agent scope、`recent_months`/`earlier_context`/`long_term_background` 之一、period 边界、首/末边界与精确 source IDs、importance、confidence、status、时间戳与确定性聚合键（`aggregation_key`）。不复制原始源消息或敏感 payload。部分唯一聚合键索引使并发或重试批次幂等，同时不影响 legacy 摘要行。

per-tenant History worker 经既有 `TenantWatcher` 运行。仅当可用 enriched entries 达 5 条（`HistoryAggregationMinEntries`）后聚合，单次 conversation/user/agent scope 最多读 50 条（`HistoryAggregationBatchSize`）。tenant LLM 调用在 repository 事务外运行，30 秒超时（`HistoryOperationTimeout`）。provider 缺失、超时或写错误使源窗口留待后续 pass；维护与事实归档独立运行，失败不进入 chat 路径。

90 天以上（`HistoryRecentPromotionAge`）的 recent segments 提升为 earlier context；365 天以上（`HistoryEarlierPromotionAge`）的 earlier segments 提升为 long-term background。Recent 与 earlier tiers 每个 conversation/user/agent scope 最多 12 个活跃 segment（`HistoryRecentMaxSegments` / `HistoryEarlierMaxSegments`）。Repository 返回一个有界溢出组及完整摘要；worker 在数据库事务外调用 tenant compressor，然后原子插入或确认幂等的 next-tier 替换（键由精确 source IDs 派生）并只归档这些 IDs。compressor、校验或写失败使每个源 segment 保持活跃。不使用 SQL `LEFT` 截断。Phase 2 中 Background 无自动删除。

Facts 非活跃满 180 天、且 importance 与 confidence 均低于 0.8 才可归档。preference-category facts 与 `explicit_user` facts 始终受保护，显式偏好与目标不受自动归档。Superseded/deleted 保留仍归既有 GC 行为。

## Schema 与回滚

权威幂等 DDL 与 legacy-tenant backfill 在 `pkg/storage/postgres/tenant_schema.sql`。迁移 `021`（active snapshots）与 `022`（history tiers）是配对的 public schema 标记，不含 tenant DDL，其 down markers 有意保留租户数据。运维回滚禁用 memory pipeline 或部署前版应用；additive 列与 legacy summary readers 兼容。移除 History 数据或列需经数据保留批准后单独评审的 tenant-schema 清理。

Startup 预置每个活跃 tenant schema 并聚合失败。任何 tenant DDL 失败都会从 bootstrap 传播，成功 public 标记迁移不能掩盖未升级的租户。

## 暂缓的限制

History 排序用 PostgreSQL trigram 相似度而非新 Milvus collection，保留既有抽象与有界读路径。Tier 提升当前按 age 与 per-scope 容量；History segments 尚未跟踪自身访问计数，访问频率影响 Facts 归档但不影响 History 提升。Long-term background 是压缩输入但不自动容量清理，避免在已评审的保留策略存在前造成不可逆丢失。
