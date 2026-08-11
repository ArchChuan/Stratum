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
7. Milvus collection `CreateCollectionWithDim` + `validateCollectionCompatibility`（client.go:312）— 维度不匹配直接报错。collection 命名：knowledge `kn_<workspaceID>`（constants/knowledge.go:66）、memory `memory_<tenant>` / `memory_facts_<tenant>`（vector_adapter.go:13-21）——**均不含模型标识**

**D. 消费方（3 个场景共享以上链路）：**
8. memory：嵌入写入（pipeline embedder，embedder.go:165）、上下文注入（BuildContext）、召回查询（recall_tool.go:176，查 memory + memory_facts 两个 collection）
9. knowledge：文档 ingest（ingest_service.go:342）、RAG query（rag_service.go:269,561）、retrieval evaluator

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
- **唯一性**：partial unique index 保证同 tenant 最多一个默认（capabilities 含 embedding 的模型）
- **悬空保护**（应用层联动，model_mgmt_service 更新路径）：模型被 disabled 时联动清除其 `default_embedding`（避免标记指向不可用模型）；启用 embedding 模型时标记不自动恢复
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

单一事实源 = `EmbeddingService.GetVectorDimension()`（embedding/embedding.go:95，唯一实现），修正为与 `vectorDim()` 一致（v2→1024 而非 fallback 1536）：

```
text-embedding-v1 → 1536
text-embedding-v2 / v3 / v4 → 1024
embedding-3 → 2048
default → 1536
```

knowledge 侧改造：

- `ingest_service.go:342`、`rag_service.go:561`：`vectorDim(req.EmbeddingModel)` → `embedder.GetVectorDimension()`（Embedder 接口已有该方法，embedder 在调用点可及）
- `workspace_service.go:144`（创建 workspace 预建 collection）：先 resolve embedder 再取维度，或调用统一维度函数
- 删除 `workspace_service.go:38` 的 `vectorDim()` 私有实现

**校验闭环**：`validateCollectionCompatibility`（Milvus 层）保留为最后防线，dim 不匹配仍报错——但按 §6 的命名规则，dim 不匹配只会在手工误操作时发生。

## 6. 变更策略：新模型新 collection 隔离

**collection 命名规则改造**（模型标识编码进 collection 名）：

| 现状 | 改造后 |
|---|---|
| `memory_<tenant>`（vector_adapter.go:13） | `memory_<tenant>_<san(model)>` |
| `memory_facts_<tenant>`（vector_adapter.go:21） | `memory_facts_<tenant>_<san(model)>` |
| `kn_<workspaceID>`（constants.CollectionName） | `kn_<workspaceID>_<san(model)>` |

`san` 复用现有 `milvusUnsafe` 清洗（`[^a-zA-Z0-9_]` → `_`）。

**换默认模型的语义**：默认模型从 A 换成 B → 所有写入走 `_<B>` collection（新 collection 自动创建），旧 `_<A>` collection 数据保留但不参与新检索。**同维度换模型也隔离**（向量语义空间不同，混存退化）。

**存量兼容（一次性升级路径）**：存量 `memory_<tenant>` / `memory_facts_<tenant>` / `kn_<workspaceID>` collection 已存在（含 Task 7 验收数据）。读取路径做 legacy 回退——查询时若新名 collection 不存在，则查旧名（旧名 = 当前默认模型的隐式 collection）。写路径总是走新名。legacy 回退只覆盖存量部署升级场景；换第二次模型后旧 collection 数据不再被查询（保留在 Milvus，删除策略不属本设计）。

改造点：

- memory：`vector_adapter.go`（写入 2 处 + DimResolver 按模型解析维度）、`recall_tool.go:176`（查询候选集合 = [新名, legacy 名]）
- knowledge：`constants.CollectionName` 签名加模型参数（rag_service.go:269、:694、ingest_service.go、workspace_service.go:144 同步）；RAG/ingest 查询路径做 legacy 回退

## 7. 前端

1. **模型管理页**（web/src/modules/llm/pages/ModelListPage.tsx）：embedding 模型行加"设为默认"操作 + 默认标识展示（tag）；调用新 API（见 §8）；禁用默认模型时前端提示联动清除
2. **记忆管理页**（web/src/modules/memory）：pipeline 健康状态区——无可用嵌入模型时显示"未配置嵌入模型"提示 + 引导链接到模型管理页；后端 stats 响应新增 `embedModelConfigured` 字段驱动

## 8. API

`internal/llmgateway` 模型管理现有路由基础上新增：

- `PUT /models/:id/default-embedding`（body: `{enabled: true|false}`）— 设为默认/取消默认；应用层校验：目标模型 capability 含 embedding 且 enabled；设置 true 时先清除同 tenant 其他默认标记
- `GET /models` 响应模型字段新增 `defaultEmbedding`（已有 DTO 结构扩展）

## 9. 异常感知（四层）

1. **指标层**：embedder.go `embedSvc == nil` 分支（现状 Warn + DLQ）新增 `memory_embed_unavailable_total{tenant_id}` counter（metrics.go，与 `memory_dlq_total` 并列）——"配置缺失"成为独立可查询信号
2. **页面层**：记忆管理页健康提示（§7.2）
3. **运维层**：grafana/ 新增告警规则——`memory_embed_unavailable_total` 增长或 `memory_dlq_total{stage="embed"}` 持续增长 → 告警（接入现有 kps 监控栈）
4. **恢复语义**：`embed_service_unavailable` 已是独立 error_code（embedder.go:151 deadLetterDetails）——新增**管理 API 定向重放**：`POST /admin/memory/dlq/replay`（body: `{errorCode: "embed_service_unavailable", tenantID?: ""}`），从 DLQ subject 读取死信消息（error_code 过滤）、重新发布回 `memory.raw.<tenant>` subject，让配置修复后记忆可恢复。重放 API 走现有 global admin 鉴权。

## 10. 测试与验证

- **唯一性**：同 tenant 设第二个默认 → partial unique index 拒绝（tenant_schema.sql 顺序测试覆盖）
- **解析规则表驱动**（registry/application 层单测）：默认存在→返回默认；无默认→返回第一个；列表为空→返回空；默认指向 disabled（防御）→回退第一个
- **collection 命名**：san 清洗、换模型新名、legacy 回退查询命中（memory vector_adapter / knowledge constants 单测 + 集成验证）
- **维度一致性**：`GetVectorDimension` 与旧 `vectorDim` 全模型对照单测（v2 修正点覆盖）
- **fail-closed**：无模型时 embedder 走 DLQ（error_code=embed_service_unavailable）+ 新指标计数
- **重放**：DLQ 消息按 error_code 过滤重发回 raw subject（pipeline 集成测试或本地 NATS 验证）
- **前端**：设为默认操作流、默认标识、记忆页健康提示（`make fe-lint && make fe-build`）
- **端到端**：`make test-verify-before-pr` 走 `stratum-e2e-development` skill 验收

## 11. 范围外（YAGNI）

- **re-embed 迁移任务**：旧 collection 数据重建向量（本次只做隔离，不迁移）
- **DLQ 重放管理 UI**：本次只做 API，前端按钮后续
- **agent 级 embedding 差异化**：每个 agent 一个嵌入模型——scope 爆炸，不做
- **workspace embedding_model 解除不可变**：保留现状
- **模型管理 tab 重构**：仍是目录页，只加"设为默认"操作
