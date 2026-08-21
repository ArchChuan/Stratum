# 记忆嵌入模型平台参数化 · 运营修复（迁移卡片下线 / 模型目录可删除 / 加载展示 / 日志与 trace 补全 / 提示词兜底移除）

日期：2026-08-21 · 状态：待复核 · 关联：`2026-08-20-model-availability-fallback-memory-embed-design.md`（本设计对其 D7/D16 的修正：嵌入模型从租户级配置改为平台全局参数）

## 1. 背景与目标

生产排查发现两个管理租户的记忆实体自 08-14 起停止增长，根因是记忆嵌入模型（`embedding-3`）解析链路长时间不可用（消息全部死信到 MEMORY_DLQ）。现状中记忆嵌入模型只存在 `tenants.settings.memory_embedding_model`（租户级 JSONB），**前端没有配置入口**。

本次目标：

1. 记忆嵌入模型改为**平台参数（全局）**配置，入口落在平台参数页（`/admin/settings` 全局参数区），热更新、无需重启。
2. 租户设置页的"记忆嵌入模型迁移卡片"下线（模型已全局化，"租户级切换"语义消失；切换入口收敛到平台参数页）。
3. 平台参数页刷新后不再先出现"纯展示/骨架"前置页，改为明确加载态后一次性渲染可编辑配置页。
4. 模型目录支持删除模型（放开 `provider_managed` 限制，当前目录 25 个模型全部为 provider-managed，删除按钮形同虚设）。
5. extraction worker 失败日志补全 `err` 字段（现仅记 error_code）；抽取队列错误信息可自诊断。
6. 嵌入模型平台参数**未配置即失败 + 告警**，启动路径不再自动 seed 兜底（S1，用户确认）。
7. 存量两处写死提示词（`pkg/constants/memory.go` 的 `Memory*DefaultPrompt`、`llm_extractor.go` 的 `extractionIdentityPrompt`）**不再作为兜底**：提示词必须显式配置，未配置即失败 + 告警（S2，用户确认）。
8. 补齐记忆链路的 trace 记录：DLQ 事件 envelope 携带 `trace_id`；抽取队列新增 `trace_id` 列并透传日志（满足"记录 trace"硬性要求）。

## 2. 现状盘点（代码证据）

### 2.1 嵌入模型解析

- `tenantEmbeddingModelResolver.ResolveMemoryEmbeddingModel`（`api/wiring/embedding_model.go:48`）读 `public.tenants.settings.memory_embedding_model`，fail-closed；消费方：memory pipeline embed resolver（`api/wiring/knowledge.go:195`）、collection 维度推导（`api/wiring/memory.go:360`）、记忆页 `embedModelConfigured`（`api/http/handler/user_memory_handler.go:53`）、迁移起点（`api/http/handler/memory_migration_handler.go:161`）、**内置文档 seed（`api/wiring/knowledge.go:348 seedBuiltinDocsForTenant`）**。
- 启动 seed（`seedMemoryEmbeddingModels`，`api/wiring/embedding_model.go:107`）对未配置租户逐租户回填 `tenants.settings`，wiring step 名为 `embedding-seed`（`api/wiring/wiring.go:90`）。
- 平台参数机制已存在：注册表（`internal/parameters/domain/registry.go`）、`platform_settings` 存储、`PUT /admin/parameters` 热更新、`PlatformSettingsPage` schema 驱动渲染。现有 `memory.enrich_model` / `memory.summary_model` 即 ScopePlatform 模型参数先例。
- `ControlModel` 控件（`ProviderModelSelect`）只拉 **chat** 模型（`web/src/modules/parameters/components/ProviderModelSelect.tsx:24`，`llmApi.listModels({ capability: 'chat' })`）；`listModels` 已支持 capability 参数（`web/src/modules/llm/api/llm.api.ts:76`），迁移 hook 已用 `capability:'embedding'`。
- **写时校验现状**：模型类平台参数校验 `validateModelInDirectory` 走 `ListChatModelsByTenant`（chat-only，`api/wiring/parameters.go:170`）；`memory.embedding_model` 必须新增 embedding 能力校验（复用 `ListEmbeddingModelsByTenant`/`ResolveEmbeddingExact`），且**不得**进入 `memoryWorkerModelKeys`（会被 chat 目录拒绝）。

### 2.2 迁移卡片

- `MemoryMigrationCard`（`web/src/modules/iam/components/MemoryMigrationCard.tsx`）渲染在租户设置页（`SettingsPage.tsx:74`），读取 `tenants.settings.memory_embedding_model`（`useMemoryMigration.ts:98`）；后端 `EffectiveModelSetter` 写 `tenants.settings`（`api/wiring/memory.go:207`）。
- 路由：`api/http/router.go:284` 注册 `registerMemoryMigrationRoutes`（:610），:619 构造 `MemoryMigrationHandler`；`memory_migration_handler_test.go` 覆盖 10+ 路由用例。
- `memory_migrations` 表当前无记录；模型改为全局后该卡片语义失效。

### 2.3 模型删除

- `DELETE /admin/models/:id` 已存在（`api/http/router.go:684`、`model_mgmt_handler.go:126`）。
- `PgModelRepo.Delete`（`internal/llmgateway/infrastructure/model_repo.go:414`）带 `provider_managed=false` 守卫；`TestPgModelRepo_DeleteProviderManaged`（`model_repo_test.go:264`）固化该行为。
- `models` 表无入站外键（仅 `provider_id → providers ON DELETE CASCADE`，迁移 035）；`UpsertDiscovered`/`upsertSyncModel`（`model_repo.go:294-360`）下次发现会重新插入被删模型（删除语义="直到下次发现前不出现"）。
- `Delete` 不处理 `default_embedding` 标记：删除默认嵌入模型后，重新发现会以无标记状态重插。

### 2.4 平台参数页加载

- `PlatformSettingsPage` 初始 `loading=true` 时仍渲染标题、分区标题、Card 骨架与保存按钮（`web/src/modules/parameters/pages/PlatformSettingsPage.tsx`），表现为"纯展示页"→ 可编辑表单的两段式。
- 站内先例：`EditMCPPage.tsx:24` `if (loading) return <Skeleton active />`。

### 2.5 提示词与 trace 现状

- 五个 `memory.*_prompt` 的 registry `Default` **已经是 `""`**（`registry.go:520-621`）；真正兜底在消费方常量：`enricher_prompt.go:11/:18`、`llm_extractor.go:80`、`llm_superseder.go:62`、`history_summarizer.go:59` 引用 `constants.Memory*DefaultPrompt`，以及 `parameters` service `PromptDefaults()`（`service.go:108`）与前端 `PROMPT_DEFAULT_KEYS`/`PromptDefaultViewer`。
- **enricher 空模板风险**：`formatEnrichmentPrompt` 在模板为空时 `fmt.Sprintf("", role, content)` 返回空串继续调 LLM（`enricher.go:386`）——删常量不补空值检查会变成"空 system prompt 静默降级"。
- `memory.extraction_prompt` 现为"规则增量"（`registry.go:520` 注释：拼接在系统渲染的身份/上限/协议之后），由 `extractionIdentityPrompt`（`llm_extractor.go:16`）承载身份与 JSON 契约。
- trace：`MemoryRawEvent.TraceID` 存在（`events.go:18`），embedder/enricher 各失败分支日志带 `trace_id`；但 **DLQ envelope（`DeadLetterEvent`/`deadLetterDetails`，`dead_letter.go:18`）无 TraceID 字段**；**`ExtractionTask` 无 TraceID**（`extraction_queue.go:9`），抽取 worker 日志只有 task_id/error_code（`extraction_worker.go:115`）。

## 3. 设计

### 3.1 记忆嵌入模型平台参数（全局）

**参数定义**（`internal/parameters/domain/registry.go` 的 `registerMemoryWorkerParams`）：

```go
{
    Key: "memory.embedding_model", Scope: ScopePlatform, Category: "memory",
    DisplayName: "记忆嵌入模型",
    Description: "全局记忆嵌入模型（模型管理目录选择）；未设置时记忆写入 fail-closed 并告警",
    ValueType: TypeString, Default: "",
    VisualHint: VisualHint{Control: ControlEmbeddingModel},
    Optimizable: false,
},
```

**控件**：

- `internal/parameters/domain/parameter.go` 新增 `ControlEmbeddingModel Control = "embedding_model"`；`web/src/modules/parameters/model/parameters.ts` zod enum 增加 `"embedding_model"`。
- `ProviderModelSelect` 增加 `capability?: 'chat' | 'embedding'`（默认 `'chat'`，现有行为不变）；`ParameterControl` 新增 `case 'embedding_model'` 以 `capability="embedding"` 渲染。
- `PlatformFieldItem` hint 语义：embedding_model 未设置时显示"未设置（记忆写入将失败并告警）"，不再显示"使用定义默认"（M3）。

**写时校验**：`api/wiring/parameters.go` 新增 embedding 能力 ValidateFn（`ListEmbeddingModelsByTenant` 或 `ResolveEmbeddingExact`），`memory.embedding_model` 写入时校验模型在嵌入目录且 enabled；**不进 `memoryWorkerModelKeys`**。

**解析链路**（`api/wiring/embedding_model.go`）：

- `tenantEmbeddingModelResolver` 增加 lazy getter 字段 `params func() *parametersapp.Service`（llmgateway 构建早于 parameters，闭包运行时解引用，与 IAM lazy 模式一致）。
- `ResolveMemoryEmbeddingModel(ctx, tenantID)`：`params().Resolver().Resolve(ctx, "memory.embedding_model", nil)` → 未设置/空串 → `errMemoryEmbeddingNotConfigured`（fail-closed）；已设置则 `registry.ResolveEmbeddingExact` 校验后返回。签名保持 `(ctx, tenantID)` 不变。
- 移除读 `tenants.settings` 的逻辑；删除 `IsMemoryEmbeddingModelConfigured` 与 `seedMemoryEmbeddingModels`。
- 错误文案与注释同步：`errMemoryEmbeddingNotConfigured`（`embedding_model.go:18`）、`llmgateway.go:42`、`user_memory_handler.go:30`、`memory_migration_service.go:29`、`web/src/modules/iam/model/auth.ts:52`。

**启动路径（S1：未配置即失败 + 告警）**：

- **删除** `seedMemoryEmbeddingModels` 与 `embedding-seed` wiring step（不再自动回填任何默认嵌入模型）。
- 平台参数未设置 → `ResolveMemoryEmbeddingModel` fail-closed → `memory.embed.resolve_failed` WARN 日志 + `memory_embed_unavailable_total` 指标 + `StratumMemoryEmbedUnavailable` 告警；消息进 DLQ 不丢。
- 部署顺序：先在平台参数页配置 `memory.embedding_model`（存量两租户当前值 embedding-3）再上线；未配置期间记忆写入 fail-closed（DLQ 不丢，配置后可重放）。
- 说明：内置文档 seed（`knowledge.go:348`）在启动时解析该参数；若管理员在启动后才配置，内置文档 seed 需下次重启或后续任务补齐（不阻塞本设计）。

### 3.2 迁移卡片与后端迁移入口下线

- 前端：`SettingsPage.tsx` 移除 `<MemoryMigrationCard />` 及 `SettingsPage.test.tsx:15-16` 的 mock；删除 `MemoryMigrationCard.tsx`、`useMemoryMigration.ts`、`memory-migration.api.ts` 及对应测试；删除 `web/src/constants/index.ts:99` 的 `MEMORY_MIGRATION_POLL_MS`。
- 后端：**删除** `registerMemoryMigrationRoutes` 调用与定义、`MemoryMigrationHandler` 及其测试、`buildMemoryMigration` wiring 构造、`appendMigrationWorker` 注册；`MemoryMigrationService`、`MigrationRepo`、`memory_migrations` 表保留为休眠代码/数据（无入口、无 worker，后续做全局迁移再恢复）。
- 监控：移除 `StratumMemoryMigrationStalled` 告警（`monitoring/local/rules/stratum-ai.yml`）与迁移指标（随 service 移除）。
- 契约：删除迁移路由后核对 `api/http/contract_test.go` golden 是否含 `/tenant/memory/migrations*`（含则同步更新）。

### 3.3 extraction worker 日志与错误自诊断

- `extraction_worker.go:115` 的 `extract_failed` 增加 `zap.Error(err)`，保留 `error_code`。
- `MarkFailed` 的 error_msg 由固定 `"extraction_failed"` 改为底层错误截断文本（≤200 字符，不含 PII/完整提示词），队列侧可自诊断。

### 3.4 平台参数页加载展示

`PlatformSettingsPage` 在 `loading=true` 时直接返回页面级加载态（`<Skeleton active paragraph={{ rows: 8 }} />`，对齐 `EditMCPPage`），数据就绪后一次性渲染完整页；同步更新页面测试。

### 3.5 平台参数页保存/清空语义

- `onFinish` 当前对 `model === def.default` 跳过提交，导致已设置值无法清空。改为：`embedding_model` 控件显式清空（空串）时**提交空串**（= 主动未配置 → fail-closed + 告警）；其它 model 参数保持跳过语义。
- `memory.embedding_model` 的 hint 与 placeholder 文案按 §3.1 调整。

### 3.6 模型目录可删除

- `PgModelRepo.Delete`：`DELETE FROM public.models WHERE id=$1`（移除 `provider_managed=false` 守卫）；`RowsAffected==0` 仍返回"model not found"；错误文案从"model not found or is provider-managed"更新。
- `ModelMgmtService.Delete` 注释同步。
- 前端确认框文案："该模型由厂商发现管理，删除后对厂商再次执行发现模型可能重新加入目录；若为默认嵌入模型或被平台参数/降级链引用，相关解析将 fail-closed，请先调整配置。"
- 测试：`TestPgModelRepo_DeleteProviderManaged` 改为"provider-managed 模型可删除 + 删除后 Get 报 not found"；`UpsertDiscovered` 覆盖"再次发现会重新插入"。

### 3.7 无写死 + 失败可观测（硬性要求）

原则（用户确认）：**代码内禁止新增写死的模型名或提示词**；模型一律来自参数/模型目录，提示词一律来自参数。若最终无兜底，必须 fail-closed，且满足：明确错误 + 结构化日志 + 记录/保留 trace + 指标与告警，禁止静默降级。

**合规矩阵（已按并行 review 修正）**：

| 路径 | 无写死 | 无兜底时的失败可观测 |
|---|---|---|
| `memory.embedding_model` 参数 | Default `""`，代码零模型字面量 | resolver 返回 `errMemoryEmbeddingNotConfigured` → `memory.embed.resolve_failed` WARN（含 tenant/err）→ DLQ `embed_service_unavailable` + `memory_embed_unavailable_total` → `StratumMemoryEmbedUnavailable`；日志含 `trace_id`，DLQ envelope 新增 `trace_id`（§3.8） |
| `memory.enrich_prompt` 空 | 删除 constants 兜底 | 显式返回错误（**禁止空模板继续调 LLM**）→ 事件重试/死信 + `memory_dlq_total` → `StratumMemoryDLQ`；日志含 `trace_id` |
| `memory.summary_prompt` 空 | 删除 constants 兜底 | 异步摘要任务记 ERROR + 指标，不阻塞 enrich 主链路（现有 WARN 升 ERROR） |
| `memory.history_summary_prompt` 空 | 删除 constants 兜底 | 周期总结返回错误 + ERROR 日志 + `memory_worker_messages_total{status=error}` → `StratumMemoryWorkerErrorRate` |
| `memory.supersede_prompt` 空 | 删除 constants 兜底 | 判定失败 → 抽取任务失败 + `zap.Error(err)` + 队列 error_msg 自诊断 + `StratumMemoryWorkerErrorRate` |
| `memory.extraction_prompt` 空 | 删除 `extractionIdentityPrompt`，完整提示词 + `{user_id}/{agent_id}/{max_facts}` 占位符 | 抽取任务失败 + `zap.Error(err)` + 队列 error_msg + `StratumMemoryWorkerErrorRate` |
| 平台参数保存 | registry + embedding 目录校验 | 非法值 → 400 统一错误中间件 + 前端提示 |

### 3.8 trace 补全

- **DLQ envelope**：`deadLetterDetails`/`DeadLetterEvent` 增加 `TraceID` 字段，从事件透传；重放对账无需反解 payload。
- **抽取队列**：`tenant_schema.sql` 的 `memory_extraction_queue` 增加 `trace_id TEXT`（`ADD COLUMN IF NOT EXISTS`，符合租户 DDL 规则）；`ExtractionTask` port 增加 `TraceID`；enqueue 路径（Redis buffer scanner）透传 trace_id（无则生成）；worker 失败日志带 `trace_id`。
- supersede/history 路径为事实级后台任务，无独立请求 trace：日志保留租户/上下文字段，不承诺 span 级 trace（矩阵已如实标注）。
- OTEL span 导出（memory worker LLM 调用）另立任务，不随本次。

### 3.9 存量写死提示词兜底移除（S2）

- 删除 `pkg/constants/memory.go` 五个 `Memory*DefaultPrompt` 常量及全部引用：`enricher_prompt.go`、`llm_extractor.go`、`llm_superseder.go`、`history_summarizer.go`、`parameters` service `PromptDefaults()`、前端 `PROMPT_DEFAULT_KEYS`。
- 五个 `memory.*_prompt` registry `Default` 保持 `""`（已是空串，无需改动；`registerMemoryWorkerParams` 注释"Defaults stay at the current const fallback"更新）。
- 消费方 fail-closed：
  - enricher：`formatEnrichmentPrompt` 前校验空模板 → 返回明确错误 → 重试/死信。
  - session summary：空模板 → ERROR + 指标，不阻塞 enrich 主链路。
  - superseder：空模板 → 判定失败。
  - history summarizer：空模板 → 总结失败。
  - extractor：`memory.extraction_prompt`（resource scope）改为**完整系统提示词**，代码渲染 `{user_id}`/`{agent_id}`/`{max_facts}` 占位符；空 → 失败。JSON 输出契约仍由 `parseExtractedFacts`/`Validate` 强制（不依赖提示词文本）。
- **语义变更声明**：存量已配置的 per-agent `memory.extraction_prompt` 语义从"规则增量"变为"完整提示词"，前端 Agent 编辑页文案/占位符提示（`AgentMemoryConfig.tsx:60-67`）、`PromptDefaultViewer`（缺失 key 会 throw，`PromptDefaultViewer.tsx:41`）与相关测试（`AgentMemoryConfig.test.tsx`、`AgentFormSections.test.tsx`）同步重写/移除。
- 兼容性影响：现有未显式配置提示词的租户，提取/富化/摘要将**失败并告警**（用户已确认接受）。
- `agent.compaction_prompt` 等非 memory.* 提示词默认不在本次范围；`registerPromptEvaluationKeys` 的下划线裸键（`memory_extraction_prompt` 等，`registry.go:657-666`）与 S2 无关，**不得误删**。

## 4. 数据与契约影响

- 无新增 public 表；`tenant_schema.sql` 的 `memory_extraction_queue` 增加 `trace_id TEXT`（幂等 `ADD COLUMN IF NOT EXISTS`）。
- 无自动写入 `platform_settings`（S1）；`tenants.settings.memory_embedding_model` 存量键保留但不再被读取（无害）。
- `PromptDefaults()` 与前端默认提示词展示移除五个 memory 键；`memory_extraction_queue.error_msg` 从固定值改为截断错误文本。
- 删除 `/tenant/memory/migrations*` 路由后核对 `api/http/contract_test.go` golden；`/admin/parameters*` 不在 contract dddPrefixes（无 golden），"无 golden 契约变更预期"仅对参数路由成立。

## 5. 测试与验收

**Go 单测**：

- registry：`memory.embedding_model` 定义断言（scope platform / control embedding_model / optimizable false / 无 evalKeys）；五个 `memory.*_prompt` Default 为空。
- resolver：平台参数命中 / 未设置 fail-closed / 目录不可解析三态；`embedding_model_test.go` 的 seed 与 `IsMemoryEmbeddingModelConfigured` 用例删除，settings fixture 重写为参数注入（`knowledge_embed_resolver_test.go:106` 同步）。
- 参数写时校验：`memory.embedding_model` 走 embedding 目录校验（不在 chat 校验链）。
- 各 worker 提示词空值 fail-closed：enricher / superseder / history / extractor / summary。
- trace：DLQ envelope 含 trace_id；抽取队列 trace_id 落库与透传。
- 模型删除：`model_repo_test.go:264` 断言反转；删除后 Get not found。
- 契约：`parameter_handler_test.go` 的 `TestPromptDefaults_returnsAllWhitelistedTemplates` 收敛为仅 `agent.compaction_prompt`，移除常量 import；迁移路由 golden 核对。
- 全量：`go vet && go test -short ./...`、`make code-quality`、`make risk-guardrails`。

**前端**：

- `make fe-lint && make fe-build`。
- PlatformSettingsPage 加载态（`PlatformSettingsPage.test.tsx`）、清空 embedding_model 提交语义、hint 文案。
- SettingsPage 无迁移卡片（`SettingsPage.test.tsx` mock 移除）、AgentMemoryConfig/AgentFormSections 文案与测试、PromptDefaultViewer 相关测试、删除确认文案。

**E2E（stratum-e2e-development，本地 Docker + headless）**：

- 平台参数页刷新无前置展示页；未配置 `memory.embedding_model` 时记忆链路 fail-closed 并触发指标/告警（`memory_embed_unavailable_total`）。
- 配置 `memory.embedding_model` 与 `memory.enrich_prompt` 后记忆链路生效（outbox→embed→enrich→entity）。
- 模型目录删除模型成功并刷新读回；租户设置页无迁移卡片。
- 构造 extraction 未配置提示词任务，日志含 err 与 trace_id，队列 error_msg 含截断错误。

**远端部署**：需用户明确许可后另行执行；部署前先在平台页配置 `memory.embedding_model`。

## 6. 决策记录

| # | 决策点 | 结论 |
|---|---|---|
| D1 | 嵌入模型配置来源 | 平台参数全局唯一来源（用户确认）；`tenants.settings` 键不再读取 |
| D2 | 存量租户连续性 | 不再自动 seed（S1，用户确认）：未配置即失败 + 告警；部署时管理员在平台页显式配置 |
| D3 | 租户迁移卡片 | 下线（用户确认）：移除租户页卡片/前端 hook/api、后端 handler/路由/wiring/worker 注册；service/repo/表休眠保留 |
| D4 | 模型删除 | 放开 provider-managed 限制（用户确认），删除为"直到下次发现前不出现"，前端提示重新发现与引用风险 |
| D5 | 平台页加载 | 页面级 Skeleton 加载态，不渲染半成品展示页 |
| D6 | 日志 | extract_failed 带 err 与 error_code；队列 error_msg 截断错误文本 |
| D7 | 无写死 + 失败可观测 | 代码零模型/提示词字面量；无兜底时错误+日志+trace+指标告警（硬性要求） |
| D8 | 存量提示词兜底 | 移除（S2，用户确认）：删五个常量与 PromptDefaults 条目、extraction_prompt 改完整提示词；未配置即失败 + 告警 |
| D9 | embedding_model 写时校验 | embedding 目录校验（新增 ValidateFn），不进 chat-only 校验链 |
| D10 | trace 补全 | DLQ envelope 与抽取队列新增 trace_id（tenant DDL + port 透传）；span 级 OTEL 另立任务 |
| D11 | 平台页清空语义 | embedding_model 允许显式清空提交（主动未配置 → fail-closed + 告警） |
| D12 | 内置文档 seed | 启动时解析平台参数；启动后配置需重启或后续任务补齐（不阻塞） |

## 7. 并行 review 结论与采纳

三路并行 review（代码正确性 / 风险回归 / 范围质量）已完成，本设计已吸收其全部 blocker/major：

- `parameter_handler_test.go` 编译与断言波及面（已入 §3.9/§5）。
- `memory.embedding_model` 写时校验（chat-only 陷阱，已入 §3.1/D9）。
- DLQ envelope 与抽取队列 trace_id（已入 §3.8/D10）。
- Agent 编辑页 extraction_prompt 文案/测试与 PromptDefaultViewer throw（已入 §3.9）。
- enricher 空模板静默降级风险（已入 §3.7/§3.9）。
- 迁移链路四件套（handler/路由/wiring/worker）处置与 MigrationStalled 告警（已入 §3.2）。
- 平台页清空语义与 hint 文案（已入 §3.5/D11）。
- seed 相关测试重写、`knowledge.go:348` 消费方、`registerPromptEvaluationKeys` 裸键防误删（已入 §2.1/§3.9/§5）。
