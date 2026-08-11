# Embedding Profile 设计（嵌入模型管理与配置）

日期：2026-08-11
状态：设计评审中
范围：嵌入模型的选择从"散落 3 处"收敛为"模型管理一个配置点 + 消费方统一读取"，换模型走新 collection 隔离，维度推导收敛为单一事实源

## 1. 背景与动机

全系统嵌入模型消费点盘点（8 处）：

**A. "用哪个模型"的决定点（4 处，各自为政）：**

1. `workspace.Config.EmbeddingModel` — knowledge workspace 配置，创建后不可变（`ErrEmbeddingModelImmutable`），白名单 `AllowedEmbeddingModels = {text-embedding-v3, embedding-3}`（domain/workspace.go:44）
2. `buildEmbedResolver`（memory pipeline 写入，wiring/knowledge.go:149）— `ListEmbeddingModelsByTenant()[0]`，**取字典序第一个**（`sort.Strings`，model_registry.go:257）
3. `buildKnowledgeEmbedResolver`（knowledge ingest/RAG，wiring/knowledge.go:169）— workspace 模型，空回退 `[0]`
4. `SeedBuiltinDocs` — 硬编码 `"text-embedding-v3"`（wiring/knowledge.go:216）

**B. 维度推导（2 套并行手写 switch，互不一致）：**
5. `EmbeddingService.GetVectorDimension()`（embedding/embedding.go:95）— v1/v2→1536、v3/v4→1024、embedding-3→2048、default→1536
6. `vectorDim()`（knowledge/application/workspace_service.go:38）— v2/v3/v4→1024、embedding-3→2048、default→1536
   **不一致点**：text-embedding-v2 在 5 是 1536（fallback），在 6 是 1024。

**C. 存储约束（1 处物理不可变）：**
7. Milvus collection `CreateCollectionWithDim` + `validateCollectionCompatibility`（client.go:312）— 维度不匹配直接报错（同时校验 agent_id 字段存在，client.go:316）。collection 命名：knowledge `kb_<workspaceID>`（constants/knowledge.go:13 `CollectionPrefix = "kb"`、:66）、memory `memory_<tenant>` / `memory_facts_<tenant>`（vector_adapter.go:13-21）——**均不含模型标识**

**D. 消费方（embed 场景共享以上链路）：**
8. memory：嵌入写入（pipeline embedder，embedder.go:165）、facts 向量写入（application/extraction.go:202 硬编码 collection 名）、召回查询（recall_tool.go:176 查 memory + memory_facts 两个 collection）、facts 专属检索（application/retrieval.go:39-44 `MemoryService.RecallMemory` 硬编码 `memory_facts_<tenant>`）、agent 上下文记忆（memory_service.go:131）
9. knowledge：文档 ingest（ingest_service.go:342）、RAG query（rag_service.go:269,561）、retrieval evaluator（委托 RAGService.Query，自动覆盖）
   （注：`MemoryInjector.BuildContext` 只读 PG 不查向量，非 embedding 消费方。）

**核心问题**："用哪个嵌入模型"散落 3 处逻辑（第一个/workspace/硬编码），维度是两套不一致的手写 switch，换模型会撞 Milvus 维度校验。产品层困惑：嵌入模型的管理和配置怎么做才合适。

**产品决策（已确认）**：记忆管理页**不暴露**嵌入模型配置——嵌入模型不是 B 端用户可感知的参数，暴露只增加认知负担且误换维度不同模型会造成记忆 collection 失效事故。配置面收敛为一个：模型管理页。记忆可用性 = 模型管理可用性，registry 无 enabled embedding 模型时记忆功能可见地失效（fail-closed），不静默空转。

## 2. 约束

1. **强制纳入模型管理**：嵌入模型的存在性（provider、API key、enabled、capability）由 tenant 模型 registry（models 表）统一管理，不建平行体系。registry 无可用模型时**无 env 回退后门**（env 只能指定模型名、不能指定 provider/key，resolve 必然走 registry——env 回退是冗余）。
2. **模型管理 tab 是展示性的**（用户确认）：它是"把所有可用模型展示出来"的目录页，不是"选哪个用途"的决策点。本设计补上选择语义，但不改变 tab 的目录定位。
3. **新模型新 collection 隔离**（用户确认）：换模型 = 新 collection，旧数据保留不参与检索，不 re-embed、不就地改写。Milvus collection 维度物理不可变是设计里最硬的技术约束。
4. **`recommended` 字段不可复用**：已被 chat fallback 候选排序占用（`candidateLess`，model_registry.go:217）——embedding 默认需要独立标记。
5. **异常感知**（用户确认）：无模型 fail-closed 不能只有 DLQ + Warn 日志，需要指标、页面提示、告警、可恢复语义四层感知。
6. **workspace 覆盖保留现状**：`workspace.EmbeddingModel` 及其不可变约束不动，不作为本次设计扩展点。

## 3. 数据模型

`pkg/storage/postgres/tenant_schema.sql` 的 `models` 表（:1454）新增一列（幂等基线，历史租户由 provision 流程的 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 升级）：

```sql
ALTER TABLE models ADD COLUMN IF NOT EXISTS default_embedding BOOLEAN NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS idx_models_default_embedding
  ON models (tenant_id)
  WHERE default_embedding AND 'embedding' = ANY(capabilities);
```

- **语义**：`default_embedding=true` 表示"该 tenant 的默认嵌入模型"——本设计新增的**唯一配置点**
- **唯一性**：partial unique index 保证同 tenant 最多一个默认（capabilities 含 embedding 的模型）。**注意 index WHERE 无 enabled 谓词**——DB 层不防"默认指向 disabled 模型"，防悬空靠应用层联动
- **悬空保护（repo 层统一自清理，覆盖全部变更路径）**：`models` 表的 enabled/capabilities 变更 SQL（Toggle disable、UpsertDiscovered 批量禁用、Update 重写 capabilities）统一追加 `default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)` 自清理表达式——禁用、移除 embedding capability 或删除默认模型时标记自动清除，无遗漏路径（手动 Update、发现同步、Toggle 三条路径全覆盖）。启用时标记不自动恢复
- **设置默认原子性**：新增 repo 方法 `SetDefaultEmbedding`（单事务：clear 同 tenant 其他标记 → set 目标标记），杜绝并发 PUT 的 clear-then-set 竞态（partial unique index 将竞态错误转为 500，单事务消除之）
- **无新表、无新实体**：Profile = models 表上的一个标记

## 4. 解析规则（统一入口）

新增统一解析函数（llmgateway application 层或 registry 层，替代 3 处分散逻辑）：

```
resolveDefaultEmbeddingModel(ctx, tenantID) (modelName string, err error):
  1. enabled 列表 = ListEmbeddingModelsByTenant(ctx, tenantID)   // 现状：enabled + provider enabled + supports(kind, embedding)，sort.Strings
  2. 若 enabled 列表为空 → 返回 ""（消费方 fail-closed，见 §6）
  3. 若 registry 有 default_embedding=true 的模型且在 enabled 列表内 → 返回之
  4. 否则 → 返回 enabled 列表第一个（现状语义保留，作为无标记时的回退）
```

消费方改造：

- `buildEmbedResolver`（memory pipeline）：`models[0]` → `resolveDefaultEmbeddingModel`
- `buildKnowledgeEmbedResolver`（knowledge ingest/RAG）：空模型回退 `models[0]` → `resolveDefaultEmbeddingModel`；workspace 指定模型时走 `ResolveEmbedding` 现状（覆盖保留）
- `SeedBuiltinDocs`：硬编码 `"text-embedding-v3"` → `resolveDefaultEmbeddingModel`（无可用模型时跳过 seed，Warn 日志）

## 5. 维度收敛

**包级统一维度函数** `embedding.DimensionForModel(name string) int`（embedding/embedding.go，唯一实现），`GetVectorDimension()` 委托它。映射修正为与旧 `vectorDim()` 一致（**v2 由 1536 修正为 1024**，消除两套 switch 的不一致）：

```
text-embedding-v1 → 1536
text-embedding-v2 / v3 / v4 → 1024
embedding-3 → 2048
default → 1536
```

knowledge 侧改造：

- `ingest_service.go:342`、`rag_service.go:561`：`vectorDim(req.EmbeddingModel)` → `embedding.DimensionForModel(req.EmbeddingModel)`（模型名静态查表，不依赖 embedder 实例）
- `workspace_service.go:144`（创建 workspace 预建 collection）：**embedder 在此不可达**（WorkspaceService 无 embed resolver），且 collection dim 必须按 `ws.Config.EmbeddingModel`（静态白名单值，可能不在 tenant registry）键控——用 `embedding.DimensionForModel(ws.Config.EmbeddingModel)`，**不能**用 registry 默认模型的 `GetVectorDimension()`
- 删除 `workspace_service.go:38` 的 `vectorDim()` 私有实现

**发布耦合（关键）**：v2 维度修正（1536→1024）改变 memory `DimResolver` 输出（wiring/memory.go:169-178）——存量 `memory_<tenant>` collection 是 1536 维，若不 gate 会导致 legacy 回退查询 dim mismatch（§6.3）。**§5 与 §6 必须同一 PR 发布**：命名改造 + 维度修正 + legacy 回退 dim 检查同步落地。

**校验闭环**：`validateCollectionCompatibility`（Milvus 层）保留为最后防线，dim 不匹配仍报错——但按 §6 的命名规则，dim 不匹配只会在手工误操作时发生。

## 6. 变更策略：新模型新 collection 隔离

**collection 命名规则改造**（模型标识编码进 collection 名；memory 侧 `san` 复用 `milvusUnsafe` 清洗，注意与现状 memory 命名用 `strings.ReplaceAll(tenantID, "-", "_")` 的清洗方式不同，需单独测试）：

| 现状 | 改造后 |
|---|---|
| `memory_<tenant>`（vector_adapter.go:13） | `memory_<tenant>_<san(model)>` |
| `memory_facts_<tenant>`（vector_adapter.go:21） | `memory_facts_<tenant>_<san(model)>` |
| `kb_<workspaceID>`（constants/knowledge.go:66，前缀 `kb`） | `kb_<workspaceID>_<san(model)>` |

`san` 复用现有 `milvusUnsafe` 清洗（`[^a-zA-Z0-9_]` → `_`）。`CollectionName` 忽略 tenantID（UUID 键控），后缀只加模型。

**换默认模型的语义**：默认模型从 A 换成 B → 所有写入走 `_<B>` collection（新 collection 自动创建），旧 `_<A>` collection 数据保留但不参与新检索。**同维度换模型也隔离**（向量语义空间不同，混存退化）。

**存量兼容（一次性升级路径）**：存量 `memory_<tenant>` / `memory_facts_<tenant>` / `kb_<workspaceID>` collection 已存在（含 Task 7 验收数据）。读取路径做 legacy 回退——查询时若新名 collection 不存在，则查旧名（旧名 = 当前默认模型的隐式 collection）。写路径总是走新名。legacy 回退只覆盖存量部署升级场景；换第二次模型后旧 collection 数据不再被查询（保留在 Milvus，删除策略不属本设计）。

**legacy 回退的 dim 检查**：升级后 DimResolver 输出可能已变化（§5 v2 修正）。legacy 回退查询前先检查旧 collection 维度，与当前模型 dim 不一致 → Warn + 跳过该 collection（数据保留，不参与检索），**不 fail-closed**——避免"升级即召回失败"。

**knowledge legacy 回退先于 drift 分类**：`handleMissingCollection`（rag_service.go:577-595）把"collection 缺失 + PG chunks>0"判为 drift → ErrRAGDependency。legacy 回退必须在 drift 分类**之前**执行，否则升级后未 re-ingest 的 workspace 全部误判 drift。

改造点：

- memory：`vector_adapter.go`（raw 写入 + DimResolver 按模型解析维度）、`application/extraction.go:202`（facts 向量 upsert 硬编码名）、`application/retrieval.go:39-44`（`MemoryService.RecallMemory` facts 专属检索）、`memory_service_v2.go:307`（facts 删除）、`milvus_adapter.go:57,205,217`（facts collection 隐式创建按向量长度定维 + 双 collection 删除路径）、`recall_tool.go:176`（查询候选 = [新名, legacy 名]，memory + memory_facts 两个 collection 一致处理）
- knowledge：`constants.CollectionName` 签名加模型参数（rag_service.go:269、:694、ingest_service.go:342、workspace_service.go:144 同步）；RAG/ingest 查询路径做 legacy 回退（先于 drift 分类）

## 7. 前端

1. **模型管理页**（web/src/modules/llm/pages/ModelListPage.tsx）：embedding 模型行加"设为默认"操作 + 默认标识展示（tag）；调用新 API（见 §8）；禁用默认模型时前端提示联动清除
2. **记忆管理页**（web/src/modules/memory）：pipeline 健康状态区——无可用嵌入模型时显示"未配置嵌入模型"提示 + 引导链接到模型管理页；后端 stats 响应新增 `embedModelConfigured` 字段驱动

## 8. API

`internal/llmgateway` 模型管理现有路由基础上新增：

- `PUT /admin/models/:id/default-embedding`（body: `{enabled: true|false}`）— 设为默认/取消默认；**挂 `RequireTenantRole("admin")` 的 `/admin/models` 组**（router.go:551-557 现有组，与模型写操作同权限面；GET-only 的 member 组 `GET /models` 不承载写语义）。应用层校验：目标模型 capability 含 embedding 且 enabled（fail-closed，不默认放行）；设置 true 时经 `SetDefaultEmbedding` 单事务清除同 tenant 其他默认标记
- `GET /models` 响应模型字段新增 `defaultEmbedding`（已有 DTO 结构扩展，只读展示）

## 9. 异常感知（四层）

1. **指标层**：embedder.go `embedSvc == nil` 分支（现状 Warn + DLQ）新增 `memory_embed_unavailable_total{tenant_id}` counter（metrics.go，与 `memory_dlq_total` 并列）——"配置缺失"成为独立可查询信号
2. **页面层**：记忆管理页健康提示（§7.2）
3. **运维层**：grafana/ 新增告警规则——`memory_embed_unavailable_total` 增长或 `memory_dlq_total{stage="embed"}` 持续增长 → 告警（接入现有 kps 监控栈）
4. **恢复语义**：`embed_service_unavailable` 已是独立 error_code（embedder.go:151 deadLetterDetails）——新增**管理 API 定向重放**。**前置改造（必做）**：现状 `DeadLetterEvent` 只存元数据（MessageID/TenantID/Stage/ErrorCode/...，dead_letter.go:90-116），且 DLQ 时原消息被 `TermWithReason` 销毁（:111-113）——不存原始 payload 则重放无法重建消息。改造：DeadLetterEvent 新增 `Payload []byte` 字段（入库原始消息 body），TermWithReason 前先读出 payload 写入事件。

   重放数据流：`POST /admin/memory/dlq/replay`（body: `{errorCode: "embed_service_unavailable"}`，**tenantID 不来自 body**——从每条死信事件的 TenantID 字段派生，防止跨租户重放越权；global admin 鉴权走现有 `/admin` 组）→ 从 DLQ subject 读取死信消息 → 按 error_code 过滤 → 校验 `Payload` 非空 → 重新发布回 `memory.raw.<tenantID>` subject（**先发布后标记**：publish 成功才置 `replayed=true` 标记，标记写入失败不重发）→ 记忆流水线重新消费，配置已修复时正常嵌入。

   幂等：每事件 `replayed` 标记 + 重放计数上限（`replay_count`，常数 `MaxDLQReplay`，超限拒绝）——重复调用同 error_code 重放不产生重复消息（已重放事件被跳过）。重放范围一次一个 error_code，不做全量 drain（全量重放归运维脚本，避免重放风暴）。

## 10. 测试与验证

**数据模型与 repo（G1-G2）：**

- **G1 唯一性 + 并发原子性**：同 tenant 设第二个默认 → partial unique index 拒绝；并发 `SetDefaultEmbedding` 设两个不同模型 → 单事务 clear-then-set，最终恰好一个默认、无 500 竞态（pgxmock 事务序列断言 + env-gated PG 集成）
- **G2 悬空自清理**：Toggle disable / UpsertDiscovered 批量禁用 / Update 重写 capabilities（移除 embedding）/ DELETE 默认模型 → `default_embedding` 自动清除；启用后标记不自动恢复（repo 单测逐路径覆盖）
- **G11 DDL 文本测试**：`tenant_schema.sql` 包含 `ADD COLUMN IF NOT EXISTS default_embedding` + partial unique index（schema 顺序测试 + 文本断言，幂等可重跑）

**解析与维度（G3、G9、G10）：**

- **解析规则表驱动**：默认存在→返回默认；无默认→返回第一个；列表为空→返回空；默认指向 disabled（防御）→回退第一个；**字典序 pin**（`sort.Strings` 现状保留，回退语义依赖）
- **G3 维度 canonical pin**：`DimensionForModel` 全模型映射显式断言（v1→1536、v2/v3/v4→1024、embedding-3→2048、default→1536）；`GetVectorDimension` 与旧 `vectorDim` 对照（v2 修正点 1536→1024 显式覆盖）
- **G9 workspace 覆盖 bypass**：workspace 指定模型 → 走 `ResolveEmbedding` 现状，registry 默认模型不影响
- **G10 seed 跳过**：无可用 embedding 模型时 `SeedBuiltinDocs` 跳过 + Warn，不 panic

**变更策略与 legacy（G7-G8）：**

- **G7 legacy 生命周期状态机**（pipeline 集成测试）：①升级态：新 collection 不存在 + 旧 collection 存在 → legacy 回退命中旧名；②换模型态：新名存在 → 只查新名；③dim mismatch：旧 collection 维度 ≠ 当前模型 dim → Warn + 跳过，不 fail-closed；④未 re-ingest：collection 缺失 + PG chunks>0 → legacy 回退先于 drift 分类，不误判 ErrRAGDependency
- **G8 memory_facts 一致性**：facts 写入（extraction.go:202）、删除（memory_service_v2.go:307）、`RecallMemory`（retrieval.go:39-44）、recall_tool 双 collection 查询——四处全按新命名 + legacy 回退，无硬编码旧名残留（fakeVectorSearcher byCollection map 断言 collection 名集合）

**fail-closed 与重放（G4-G6）：**

- **fail-closed**：无模型时 embedder 走 DLQ（error_code=embed_service_unavailable）+ `memory_embed_unavailable_total` 计数
- **G4 重放 filter-miss**：error_code 不匹配的事件不重发（embedded NATS 集成测试）
- **G5 跨租户隔离**：重放目标 subject 由事件 TenantID 派生，A 租户事件绝不发到 B 租户 subject
- **G6 重放幂等**：重复调用 → 已 `replayed` 事件跳过；`replay_count` 超 `MaxDLQReplay` 拒绝；先发布后标记，publish 失败不置标记

**前端（vitest + build）：** 设为默认操作流、默认标识 tag、禁用默认模型联动提示、记忆页健康提示（`make fe-lint && make fe-build`）

**端到端**：`make test-verify-before-pr` 走 `stratum-e2e-development` skill 验收（含换模型后新旧 collection 数据隔离验证）

## 11. 范围外（YAGNI）

- **re-embed 迁移任务**：旧 collection 数据重建向量（本次只做隔离，不迁移）
- **DLQ 重放管理 UI**：本次只做 API，前端按钮后续
- **agent 级 embedding 差异化**：每个 agent 一个嵌入模型——scope 爆炸，不做
- **workspace embedding_model 解除不可变**：保留现状
- **模型管理 tab 重构**：仍是目录页，只加"设为默认"操作
