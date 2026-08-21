# 记忆嵌入模型平台参数化 · 运营修复（迁移卡片下线 / 模型目录可删除 / 加载展示 / 日志补全）

日期：2026-08-21 · 状态：待复核 · 关联：`2026-08-20-model-availability-fallback-memory-embed-design.md`（本设计对其 D7/D16 的修正：嵌入模型从租户级配置改为平台全局参数）

## 1. 背景与目标

生产排查发现两个管理租户的记忆实体自 08-14 起停止增长，根因是记忆嵌入模型（`embedding-3`）解析链路长时间不可用（消息全部死信到 MEMORY_DLQ）。现状中记忆嵌入模型只存在 `tenants.settings.memory_embedding_model`（租户级 JSONB），**前端没有配置入口**，只能改库。

本次目标：

1. 记忆嵌入模型改为**平台参数（全局）**配置，入口落在平台参数页（`/admin/settings` 全局参数区），热更新、无需重启。
2. 租户设置页的"记忆嵌入模型迁移卡片"下线（模型已全局化，"租户级切换"语义消失；切换入口收敛到平台参数页）。
3. 平台参数页刷新后不再先出现"纯展示/骨架"前置页，改为明确加载态后一次性渲染可编辑配置页。
4. 模型目录支持删除模型（放开 `provider_managed` 限制，当前目录 25 个模型全部为 provider-managed，删除按钮形同虚设）。
5. extraction worker 失败日志补全 `err` 字段（现仅记 error_code，无法定位根因）。
6. 嵌入模型平台参数**未配置即失败 + 告警**，启动路径不再自动 seed 兜底（S1，用户确认）。
7. 存量两处写死提示词（`pkg/constants/memory.go` 的 `memory.*_prompt` 定义默认、`llm_extractor.go` 的 `extractionIdentityPrompt`）**不再作为兜底**：提示词必须显式配置，未配置即失败 + 告警（S2，用户确认）。

## 2. 现状盘点（代码证据）

### 2.1 嵌入模型解析

- `tenantEmbeddingModelResolver.ResolveMemoryEmbeddingModel`（`api/wiring/embedding_model.go:48`）读 `public.tenants.settings.memory_embedding_model`，fail-closed；消费方：memory pipeline embed resolver（`api/wiring/knowledge.go:195`）、collection 维度推导（`api/wiring/memory.go:360`）、记忆页 `embedModelConfigured`（`api/http/handler/user_memory_handler.go:53`）、迁移起点（`api/http/handler/memory_migration_handler.go:161`）。
- 启动 seed（`seedMemoryEmbeddingModels`，`api/wiring/embedding_model.go:107`）对未配置租户逐租户回填 `tenants.settings`。
- 平台参数机制已存在：注册表（`internal/parameters/domain/registry.go`）、`platform_settings` 存储、`PUT /admin/parameters` 热更新、`PlatformSettingsPage` schema 驱动渲染。现有 `memory.enrich_model` / `memory.summary_model` 即 ScopePlatform 模型参数先例。
- `ControlModel` 控件（`ProviderModelSelect`）只拉 **chat** 模型（`web/src/modules/parameters/components/ProviderModelSelect.tsx:24`），嵌入模型选择需要 capability=embedding 的新控件分支。

### 2.2 迁移卡片

- `MemoryMigrationCard`（`web/src/modules/iam/components/MemoryMigrationCard.tsx`）渲染在租户设置页（`SettingsPage.tsx:74`），读取 `tenants.settings.memory_embedding_model`（`useMemoryMigration.ts:98`）；后端 `EffectiveModelSetter` 写 `tenants.settings`（`api/wiring/memory.go:207`）。
- `memory_migrations` 表当前无记录；模型改为全局后该卡片语义失效。

### 2.3 模型删除

- `DELETE /admin/models/:id` 已存在（`api/http/router.go:684`、`model_mgmt_handler.go:126`）。
- `PgModelRepo.Delete`（`internal/llmgateway/infrastructure/model_repo.go:414`）带 `provider_managed=false` 守卫；`model_mgmt_service.go:330` 注释"removes a non-provider-managed model"。
- 远端目录 25 个模型全部 `provider_managed=true` → 删除必然失败；`TestPgModelRepo_DeleteProviderManaged` 固化该行为。
- `models` 表无外键引用（`pg_constraint` 空），删除为纯行删除；provider 再次"发现模型"会按 `upsertSyncModel` 重新插入（删除语义为"直到下次发现前不出现"）。

### 2.4 平台参数页加载

- `PlatformSettingsPage` 初始 `loading=true` 时仍渲染标题、分区标题、Card 骨架与保存按钮（`web/src/modules/parameters/pages/PlatformSettingsPage.tsx`），表现为"纯展示页"→ 可编辑表单的两段式。
- 站内先例：`EditMCPPage.tsx:24` `if (loading) return <Skeleton active />`。

## 3. 设计

### 3.1 记忆嵌入模型平台参数（全局）

**参数定义**（`internal/parameters/domain/registry.go` 的 `registerMemoryWorkerParams`）：

```go
{
    Key: "memory.embedding_model", Scope: ScopePlatform, Category: "memory",
    DisplayName: "记忆嵌入模型",
    Description: "全局记忆嵌入模型（模型管理目录选择）；未设置时记忆写入 fail-closed",
    ValueType: TypeString, Default: "",
    VisualHint: VisualHint{Control: ControlEmbeddingModel},
    Optimizable: false,
},
```

**控件**：

- `internal/parameters/domain/parameter.go` 新增 `ControlEmbeddingModel Control = "embedding_model"`。
- `web/src/modules/parameters/model/parameters.ts` zod enum 增加 `"embedding_model"`。
- `ProviderModelSelect` 增加 `capability?: 'chat' | 'embedding'` 参数（默认 `'chat'`，现有行为不变）；`ParameterControl` 新增 `case 'embedding_model'` 用 `capability="embedding"` 渲染。

**解析链路**（`api/wiring/embedding_model.go`）：

- `tenantEmbeddingModelResolver` 增加 lazy getter 字段 `params func() *parametersapp.Service`（llmgateway 构建早于 parameters，闭包运行时解引用，与现有 IAM lazy 模式一致）。
- `ResolveMemoryEmbeddingModel(ctx, tenantID)`：`params().Resolver().Resolve(ctx, "memory.embedding_model", nil)` → 未设置/空串 → `errMemoryEmbeddingNotConfigured`（fail-closed）；已设置则 `registry.ResolveEmbeddingExact` 校验后返回。签名保持 `(ctx, tenantID)` 不变，消费方零改动。
- 移除读 `tenants.settings` 的逻辑；`IsMemoryEmbeddingModelConfigured` 与逐租户 seed 删除。
- wiring（`api/wiring/llmgateway.go:140`）注入 `func() *parametersapp.Service { if c.Parameters == nil { return nil }; return c.Parameters.Service }`。

**启动路径（S1：未配置即失败 + 告警）**：

- **删除** `seedMemoryEmbeddingModels` 与 `embedding-seed` wiring step（不再自动回填任何默认嵌入模型）。
- 平台参数未设置 → `ResolveMemoryEmbeddingModel` fail-closed → `memory.embed.resolve_failed` WARN 日志 + `memory_embed_unavailable_total` 指标 + `StratumMemoryEmbedUnavailable` 告警；消息进 DLQ 不丢。
- 部署时由管理员在平台参数页显式配置（代码零兜底；存量两个租户当前值为 embedding-3，上线时在平台页设置一次）。

**迁移卡片下线**：

- 前端：`SettingsPage.tsx` 移除 `<MemoryMigrationCard />`；删除 `MemoryMigrationCard.tsx`、`useMemoryMigration.ts`、`memory-migration.api.ts` 及对应测试（无其它引用）。
- 后端：移除租户迁移路由注册（`/tenant/memory/migrations*`）；`MemoryMigrationService` 与 worker 保留代码但不再注册 worker/入口（无迁移记录时空转，后续如做全局迁移再恢复）。

### 3.2 extraction worker 日志补全

`internal/memory/infrastructure/workers/extraction_worker.go:115` 的 `extract_failed` 增加 `zap.Error(err)`，保留 `error_code`。

### 3.3 平台参数页加载展示

`PlatformSettingsPage` 在 `loading=true` 时直接返回页面级加载态（`<Skeleton active paragraph={{ rows: 8 }} />`，对齐 `EditMCPPage`），数据就绪后一次性渲染完整页；同步更新页面测试。

### 3.4 模型目录可删除

- `PgModelRepo.Delete`：`DELETE FROM public.models WHERE id=$1`（移除 `provider_managed=false` 守卫）；`RowsAffected==0` 仍返回"model not found"。
- `ModelMgmtService.Delete` 注释与错误文案同步更新。
- 前端确认框文案补充："该模型由厂商发现管理，删除后对厂商再次执行发现模型可能重新加入目录"。
- 测试：`TestPgModelRepo_DeleteProviderManaged` 改为"provider-managed 模型可删除 + 删除后 Get 报 not found"；`UpsertDiscovered` 覆盖"再次发现会重新插入"。

### 3.5 无写死模型/提示词与失败可观测（硬性要求）

原则（用户确认）：**代码内禁止新增写死的模型名或提示词**；模型一律来自参数/模型目录，提示词一律来自参数。若最终无兜底，必须 fail-closed，且满足：明确错误 + 结构化日志 + 记录/保留 trace + 指标与告警，禁止静默降级。

**本改动合规矩阵**：

| 路径 | 无写死 | 无兜底时的失败可观测 |
|---|---|---|
| `memory.embedding_model` 参数 | Default `""`，代码零模型字面量 | resolver 返回 `errMemoryEmbeddingNotConfigured` → `buildEmbedResolver` WARN `memory.embed.resolve_failed`（含 tenant/err）→ embedder DLQ `embed_service_unavailable` + `memory_embed_unavailable_total` → 告警 `StratumMemoryEmbedUnavailable`；日志与 DLQ 事件均携带 `trace_id` |
| `memory.*_prompt`（extraction/enrich/summary/history_summary/supersede） | 定义 Default 改为 `""`，删除 constants 兜底提示词 | 未配置 → 对应 worker 返回明确错误 + ERROR/WARN 日志（含 trace_id）+ `memory_dlq_total`/`memory_worker_messages_total` → 告警 `StratumMemoryDLQ`/`StratumMemoryWorkerErrorRate` |
| extraction 身份模板 | 删除 `extractionIdentityPrompt` 写死，`memory.extraction_prompt`（resource scope）承载完整系统提示词（支持 `{user_id}`/`{agent_id}`/`{max_facts}` 占位符） | 未配置 → extractor 返回明确错误 → 抽取任务失败（队列 error_msg 保留）+ `zap.Error(err)` 日志 + `StratumMemoryWorkerErrorRate` |
| 平台参数保存 | registry 校验（未知 key/非法值 → 400） | 失败走统一错误中间件 + 前端错误提示 |

**trace 说明**：memory 各 worker 的 LLM 调用暂无 OTEL span 导出，trace 目前以 `trace_id` 在日志与 DLQ 事件中传递（满足"记录 trace"）；span 级链路另立任务。

### 3.6 存量写死提示词兜底移除（S2）

- `internal/parameters/domain/registry.go`：`memory.extraction_prompt`、`memory.enrich_prompt`、`memory.summary_prompt`、`memory.history_summary_prompt`、`memory.supersede_prompt` 的 `Default` 全部改为 `""`。
- 删除 `pkg/constants/memory.go` 中对应五个 `Memory*DefaultPrompt` 常量及其引用（worker 兜底、`PromptDefaults()` map、前端 `PROMPT_DEFAULT_KEYS` 与默认模板展示）。
- 消费方 fail-closed：
  - enricher（`memory.enrich_prompt` 空）→ 该事件处理失败 → 重试/死信（错误码 + 日志 + 指标）。
  - session summary（`memory.summary_prompt` 空）→ 异步摘要任务记 ERROR + 指标，不阻塞 enrich 主链路。
  - superseder（`memory.supersede_prompt` 空）→ 判定失败 → 抽取任务失败（错误 + 日志 + 指标）。
  - history summarizer（`memory.history_summary_prompt` 空）→ 周期总结失败（错误 + 日志 + 指标）。
  - extractor（`memory.extraction_prompt` 空）→ 抽取任务失败（明确错误 + `zap.Error(err)` 日志 + 指标）。
- 兼容性影响：现有未显式配置提示词的租户，提取/富化/摘要将**失败并告警**（用户已确认接受）；agent 维度 `memory.extraction_prompt` 需在 Agent 配置中显式设置。
- `agent.compaction_prompt` 等非 memory.* 提示词默认不在本次范围（未列入"两处存量写死"）。

## 4. 数据与契约影响

- 无新增表、无 tenant DDL、无 proto 变更。
- 无自动写入 `platform_settings`（S1：部署时由管理员在平台页显式配置）；`tenants.settings.memory_embedding_model` 存量键保留但不再被读取（无害）。
- `PromptDefaults()` 与前端默认提示词展示同步移除五个 memory 键。
- 平台参数 schema 由 registry 动态下发，无 golden 契约变更预期（实现时验证 `api/http/contract_test.go` 不受影响）。

## 5. 测试与验收

- Go：registry 断言（scope/control/optimizable/evalKeys、`memory.*_prompt` Default 为空）、resolver 三分支（平台参数命中/未设置 fail-closed/目录不可解析）、各 worker 提示词空值 fail-closed、model repo 删除行为反转、`go vet && go test -short ./...`、`make code-quality`、`make risk-guardrails`。
- 前端：`make fe-lint && make fe-build`；PlatformSettingsPage 加载态测试、SettingsPage 无迁移卡片、删除确认文案。
- E2E（stratum-e2e-development，本地 Docker + headless）：平台参数页刷新无前置展示页；平台参数未设置时记忆链路 fail-closed 并触发指标/告警；保存 `memory.embedding_model` 与 `memory.enrich_prompt` 后记忆链路生效（outbox→embed→enrich→entity）；模型目录删除模型成功并刷新读回；租户设置页无迁移卡片；构造 extraction 未配置提示词任务观察日志含 err。
- 远端部署需用户明确许可后另行执行。

## 6. 决策记录

| # | 决策点 | 结论 |
|---|---|---|
| D1 | 嵌入模型配置来源 | 平台参数全局唯一来源（用户确认）；`tenants.settings` 键不再读取 |
| D2 | 存量租户连续性 | 不再自动 seed（S1）：未配置即失败 + 告警；部署时管理员在平台页显式配置 |
| D3 | 租户迁移卡片 | 下线（用户确认）：移除租户页卡片与租户迁移路由，切换入口收敛到平台参数页 |
| D4 | 模型删除 | 放开 provider-managed 限制（用户确认），删除为"直到下次发现前不出现"，前端提示重新发现 |
| D5 | 平台页加载 | 页面级 Skeleton 加载态，不渲染半成品展示页 |
| D6 | 日志 | extract_failed 带 err 与 error_code |
| D7 | 无写死 + 失败可观测 | 代码零模型/提示词字面量；无兜底时错误+日志+trace+指标告警（硬性要求） |
| D8 | 存量提示词兜底 | 移除（S2）：五个 `memory.*_prompt` 定义默认清空 + 删除 extraction 身份模板；未配置即失败 + 告警 |
| S1 | seed 行为 | 用户确认：纯 fail-closed——未配置即失败 + 告警，启动路径不再自动 seed 兜底；部署时管理员在平台页显式配置 |
| S2 | 存量写死提示词 | 用户确认：纳入改造——两处存量写死均移除兜底（五个 `memory.*_prompt` 定义默认清空 + 删除 extraction 身份模板），未配置即失败 + 告警 |
