# 模型管理重构设计：全局平台目录 + 行为档案分离（方案 1）

日期：2026-08-13 · 状态：待复核 · 关联：memory facts 提取零产出 bug（#模型管理碎片化根因）

## 1. 背景与根因

远端生产环境 memory facts 提取零产出。调查结论：**bug 而非未触发**——extraction 触发 2 次全部失败，根因是 memory pipeline 的 LLM 调用不传 Model：

- `llm_extractor.go` 的 `ExtractFacts` 构造 `CompletionRequest` **不设 Model** → `ModelRegistry.Resolve(tenantID, "")` 报 `model "" not found for tenant`
- `history_summarizer.go` 的 `SummarizeHistory` 同样**不设 Model** → 同错
- `enricher` 是唯一显式设 `cfg.EnrichModel` 的 working pattern
- `MEMORY_SUMMARY_MODEL` 是**死配置**——`LLMHistorySummarizer` 从未接它

深挖暴露**模型配置碎片化 4 层**，互相不一致：

| 层 | 位置 | 内容 |
|---|---|---|
| per-tenant 目录 | tenant schema `providers`/`models` | API key、能力、default_embedding |
| 行为档案 | public `model_profiles` | 按模型族的 prompt + 模型引用（enrich/summary） |
| 平台 configmap | `MemoryPipelineConfig` | EnrichModel/SummaryModel env 兜底 |
| 硬编码 | `pkg/constants`、`knowledge/domain/workspace.go` | `AgentFactCheckJudgeModel`、`AllowedEmbeddingModels`、config `RerankModel`（死配置） |

借修复 extraction model bug 的机会，整体重构模型管理。

## 2. 目标与约束

- **纯共享平台级模型目录（方案 C）**：`providers`/`models` 从 per-tenant 提升为 public schema 平台全局资源，**不保留租户自选模型池**，账户全部走平台模型/凭据
- **自洽管理**：模型/提示词/参数收敛到平台一处定义、一套规则解析，修掉各 memory 机制 model 引用不一致
- **行为档案与全局目录分离（方案 1）**：`model_profiles` 补齐后成为 memory 全部机制 + fact-check 的「模型+提示词+参数」唯一解析点；模型名是全局目录的**引用**，保存时校验存在性
- **非业务面模型选择全迁平台**：memory 4 个 LLM 消费机制（extraction/enrich/summary/supersede）、embedding 默认、fallback 候选、fact-check judge、embedding 白名单、rerank 模型全部收敛到平台（目录 or 档案）；compaction 是纯文本压缩（无 LLM 调用），仅 prompt 已接 profile
- **业务面候选收敛**：Agent 模型、知识库 embedding、rerank 策略等业务配置**保留**，但候选来源 = 全局目录，只能选不能定义
- **直接大重构**（非增量分阶段）
- **存量迁移**：现有 per-tenant 的 providers/models 迁移到平台层（如 tenant 248dfa9a 的智谱 provider + 15 模型）

## 3. 目标架构

```
public schema（平台全局，无租户维度）
├── providers       ← 去 tenant_id，UNIQUE(name)
├── models          ← 去 tenant_id，UNIQUE(provider_id, name)
├── model_profiles  ← 补 extraction_model + agent 机制键（judge_model），唯一解析点
└── platform_params（现有，不动）
```

一切 LLM 模型引用/凭据只在这三层解析。`ModelRegistry.Resolve(tenantID, model)` 的 tenantID 维度整体拆除。

## 4. 数据模型变更

### 4.1 新建 public 表：035_platform_model_catalog.up.sql

`pkg/migration/sql/035_platform_model_catalog.{up,down}.sql`（只操作 public schema）：

```sql
CREATE TABLE IF NOT EXISTS providers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,          -- 平台全局唯一
    kind TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    api_key TEXT NOT NULL DEFAULT '',   -- crypto.EncryptSecret 密文，同 DataEncryptionKey
    default_model TEXT NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    capabilities TEXT[] NOT NULL DEFAULT '{}',
    context_window INT NOT NULL DEFAULT 0,
    max_tokens INT NOT NULL DEFAULT 0,
    input_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_price DOUBLE PRECISION NOT NULL DEFAULT 0,
    recommended BOOLEAN NOT NULL DEFAULT false,
    enabled BOOLEAN NOT NULL DEFAULT true,
    provider_managed BOOLEAN NOT NULL DEFAULT false,
    default_embedding BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(provider_id, name)
);
CREATE INDEX IF NOT EXISTS idx_models_provider ON models(provider_id);
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled);
-- 常量表达式索引 (true)：满足 WHERE 的所有行取同一常量，唯一约束强制全表最多一个默认标记。
CREATE UNIQUE INDEX IF NOT EXISTS idx_models_default_embedding
    ON models ((true))
    WHERE default_embedding AND 'embedding' = ANY(capabilities);  -- 全局唯一，去 tenant 谓词
```

### 4.2 tenant_schema.sql 移除 providers/models

- 删除 `tenant_schema.sql:1486-1530` 的 `providers`/`models` DDL + 索引
- 追加存量清理语句（`DROP TABLE IF EXISTS models; DROP TABLE IF EXISTS providers;`，靠 FK CASCADE 先 models 后 providers）
- 新租户不再建这两张表；存量租户在数据迁移完成后由 provision 幂等清理

### 4.3 model_profiles 扩展（无 DDL，纯 Go struct + seed）

`model_profiles.baseline` 是 JSONB，补字段无需表结构迁移：

```go
type BaselineModels struct {
    EnrichModel     string `json:"enrich_model,omitempty"`
    SummaryModel    string `json:"summary_model,omitempty"`
    ExtractionModel string `json:"extraction_model,omitempty"` // 新增
    JudgeModel      string `json:"judge_model,omitempty"`      // 新增：fact-check LLM-as-Judge
}
```

seed 对齐 config 现状：enrich=qwen-turbo / summary=qwen-plus / extraction=qwen-plus / judge=qwen-turbo。

## 5. 解析层改造：ModelRegistry 去租户

```
Resolve(ctx, tenantID, model)        → Resolve(ctx, model)
ResolveEmbedding(ctx, tenantID, m)   → ResolveEmbedding(ctx, m)
ResolveFallbackCandidates(ctx, tID,p)→ ResolveFallbackCandidates(ctx, p)
ResolveDefaultEmbeddingModel(ctx,tID)→ ResolveDefaultEmbeddingModel(ctx)
WarmTenant(tenantID)                 → 移除，启动时全目录预热一次
Invalidate(tenantID)                 → Invalidate()
缓存 map[tenantID]map[key]*entry     → map[key]*entry（单层）
```

**repo 接线变更**（关键）：`PgProviderRepo`/`PgModelRepo` 当前经 `execTenant`（`postgres.ExecTenantWith`）路由到 tenant schema。表移 public 后改为 **public schema 直连**（仿 `ProfileRepo` 的 pgxPool 模式），SQL 去 tenant 谓词，port 签名去 `tenantID` 参数。

已确认调用面：`internal/llmgateway/{application/model_service.go, infrastructure/gateway.go}`、`api/wiring/{tenant_resolver,knowledge,iam,agent,platform,llmgateway}.go`、`api/http/handler/user_memory_handler.go:33`、resolver 测试（`TestNewTenantCapabilityResolver...`/`TestResolverTwoLevelFallback`）。

`tenantCapabilityResolver.ResolveLLM/ResolveWorkerLLM`：删 WarmTenant 预热，直接全局 resolve。

## 6. 全局默认解析链（fallback 层级兜底）

统一 5 级解析链，把「未传 model / 未命中」从报错变成有意义兜底：

```
① 显式 model 名 → models 精确匹配（enabled + provider 可用 + 能力匹配）
② provider.default_model → 该 provider 的默认模型（HealthModel 复用）
③ models.recommended → 平台推荐的全局默认 chat/embed 模型
④ default_embedding 标记 → embedding 专用（marked → 无标记则 enabled 第一个）
⑤ fail-closed：全链空 → 明确报错，禁止默认放行
```

memory extraction/history 的 bug 根治点：profile 解析出具体模型名 → ① 精确命中；模型被删 → ②③ 兜底；全局目录真为空 → ⑤ 报错。fallback 候选语义沿用现状（排除主模型、同 provider 优先 → recommended → name、上限 `MaxModelFallbackCandidates`），仅去租户维度。

## 7. 消费方改造

### 7.1 memory 4 个 LLM 消费机制统一解析（bug 根治）

统一模式：消费方拿模型名（`profile.baseline.models.<机制>`）→ 全局解析链 resolve → `ProviderConfig` + protocol。

| 机制 | 现状 | 改动 |
|---|---|---|
| extraction | `llm_extractor.go` 不设 Model → bug | 设 `Model = profile.ExtractionModel` |
| enrich | 设 `cfg.EnrichModel`（working） | 切到 profile 统一解析 |
| summary | `history_summarizer.go` 不设 Model → bug；`MEMORY_SUMMARY_MODEL` 死配置 | 设 `Model = profile.SummaryModel`，接入死配置 |
| supersede | `llm_superseder.go` 同 enrich | 同 enrich |
| compaction | prompt 已在 profile；**无 LLM 调用（纯文本压缩）** | 不动（仅确认 prompt 已接） |

每个机制同时从 profile 拿 prompt（`BaselinePrompts` 6 键已存在）+ 参数（`BaselineRecall` 接线）。

### 7.2 fact-check judge 进 model_profiles（已确认）

- `BaselineModels` 新增 `JudgeModel`；`factcheck` 从 `cfg.AgentFactCheck.JudgeModel`（env）切到 profile 解析
- judge 走 `Judge` port（`JudgeClaims(ctx, claims, evidence)`），LLM judge 实现在 llmgateway/agent infra，prompt 在 judge 实现内——实现时提取到 `BaselinePrompts` 独立键 `agent_factcheck`
- 与 memory 机制同模式：profile 拿 judge 模型名 → 全局解析链 resolve

### 7.3 embedding / rerank / 白名单收敛（非业务面全迁平台）

- **embedding 默认**：`ResolveDefaultEmbeddingModel` 全局化（④ 级），`default_embedding` 全局唯一
- **embedding 白名单**：删除 `knowledge/domain/workspace.go` 硬编码 `AllowedEmbeddingModels`，`WorkspaceConfig.Validate()` 改为「模型存在于全局 models 且 enabled + embedding 能力」
- **rerank 模型**：全局目录新增 rerank 能力模型（cohere kind 进全局 providers）；`AllowedRerankIdentities` 的 `cohere` 校验改为目录存在性
- **死配置清理**：`config.go` `RerankModel`（无消费点）与 `MEMORY_SUMMARY_MODEL` 一并清理或接入

### 7.4 业务面边界（候选收敛，不定义）

Agent 模型、知识库 embedding、rerank 策略、SystemAssistant、evaluation 模型的业务配置保留，但：

- 业务表单模型下拉数据源统一指向全局目录列表
- `WorkspaceConfig.Validate()` embedding/rerank 校验改目录存在性
- 业务侧禁止自定义模型名（只能选全局目录内）

## 8. 前端改造

- **`web/src/modules/llm/`**（ModelListPage / ModelManagementPage / ProviderListPage / ModelEditDrawer / ProviderForm / DiscoverResultModal + hooks）：租户自管模型 → 平台全局模型目录；API 去 tenant 维度；入口收进平台管理（对齐 #361 仅 global admin 可见可调用）
- **`ProfileEditDrawer.tsx`**：`enrich_model`/`summary_model`（Input:170-175）改 `Select`（数据源 = 全局 chat 模型列表），新增 `extraction_model`、`judge_model` 下拉；mechanism Profile schema 同步
- **业务面表单**（AgentFormSections / WorkspaceCreateModal / SystemAssistantSettingsForm 等）：模型下拉数据源切换全局目录列表
- mechanism/llm 的 API 层：新增「拉全局模型列表」依赖（或复用全局目录接口）

## 9. 凭据安全

- 全局 `providers.api_key` 沿用 `crypto.EncryptSecret(r.key, ...)`，同一 `DataEncryptionKey`（`provider_repo.go:51`）
- 迁移密文直接搬，无明文中转
- provider CRUD API 返回时不回传 api_key（沿用 `ListModelsByTenantDetails`「凭据不出边界」先例，全局化泛化到所有 provider 返回路径）
- 校验：`crypto.IsEncrypted` 前缀判定不变

## 10. 存量迁移（一次性，时序关键）

**约束**：编号迁移（`pkg/migration/sql/`）只操作 public schema，禁止引用 tenant-only 表（`docs/agent/migration-tenant.md`）。跨 schema 迁移**不能**是 SQL 迁移，必须是一次性 Go 工具。

**时序**（顺序不可颠倒）：

1. **部署迁移 035**：建 public `providers`/`models`（不删 tenant 数据）
2. **运行一次性 Go 工具** `cmd/model-migrate/`（用完即弃，不留启动路径）：
   - 遍历 `information_schema` 找全部 tenant schema
   - 读各 schema `providers`/`models`，**API key 密文原样读**（同一 DataEncryptionKey，无需解密）
   - **归并**到 public 表：provider 按 `name`、模型按 `(provider,name)`；`default_embedding` 冲突全局唯一 → 保留先创建者，迁移工具清理多余标记并打印告警
   - 同 name 多 provider key 冲突：取 `enabled` 且 `updated_at` 最新，冲突打印告警、不静默选 key
   - **对账校验**：迁移后行数 = 归并后预期行数，打印清单待人工核验
3. **更新 tenant_schema.sql**：移除 DDL + 追加 `DROP IF EXISTS`（存量租户下次 provision 幂等清理，新租户不建）
4. **代码全量切换**到 public 表（ModelRegistry 去租户、repo 直连）

**E2E 优先本地 Docker 验证**，明确确认后才远端执行。

## 11. 测试策略

- **迁移**：一次性工具 dry-run 对账测试（源行数 = 归并后行数；default_embedding/key 冲突告警用例）
- **解析链**：`TestResolverTwoLevelFallback` 扩成全局解析链 5 级单测；新增「目录为空 fail-closed」「②③ 兜底」用例
- **memory 回归**：`llm_extractor`/`history_summarizer` 补「不设 Model → 解析出模型名」回归用例；四机制 resolve 正常
- **fact-check**：judge 模型从 profile 解析路径单测
- **前端**：ProfileEditDrawer 模型字段 Select 化后 upsert 提交全局目录内模型；业务面表单下拉数据源断言
- **仓库级**：`rg` 全量扫描确认无残留 per-tenant model 引用、无硬编码 embedding 白名单、无 `execTenant` 残留
- 契约测试：`api/http/contract_test.go` + golden 守护 provider/model DTO 变更

## 12. 影响面清单

| 文件/目录 | 改动 |
|---|---|
| `pkg/migration/sql/035_platform_model_catalog.{up,down}.sql` | 新建 public 表 |
| `pkg/storage/postgres/tenant_schema.sql` | 移除 providers/models DDL + 追加清理 |
| `internal/mechanism/domain/profile.go` | `BaselineModels` 补 `ExtractionModel`/`JudgeModel`；`BaselinePrompts` 补 agent 键 |
| `internal/mechanism/application/baseline_service.go` | `UpsertProfile` 注入模型存在性校验（新增 mechanism 自有 port `ModelExists(ctx, model, capability)`，由 wiring 用全局目录适配，**禁止 mechanism import llmgateway**） |
| `internal/mechanism/infrastructure/persistence/profile_repo.go` | 无表变更；seed 补字段 |
| `internal/llmgateway/infrastructure/{model_registry,model_repo,provider_repo,gateway,tenant_resolver 依赖}.go` | 去 tenantID、缓存单层、repo public 直连 |
| `api/wiring/{tenant_resolver,knowledge,iam,agent,platform,llmgateway,memory}.go` | 签名同步、删 WarmTenant |
| `internal/memory/infrastructure/{pipeline/llm_extractor, workers/history_summarizer, ...}` | 设 Model + profile 解析 |
| `internal/agent/application/factcheck/` | judge 模型切 profile |
| `internal/knowledge/domain/workspace.go` | 删 `AllowedEmbeddingModels`，校验改目录存在性 |
| `config/config.go` + `pkg/constants` | 清理死配置；`MemoryPipelineConfig` 补 extraction model |
| `web/src/modules/llm/`、`mechanism/`、业务面表单 | 全局目录 UI + Select 化 |
| `cmd/model-migrate/` | 一次性迁移工具 |

## 13. 风险与开放问题

- **多租户 provider key 冲突**：同名 provider 不同 key 归并只留一条 → 迁移工具告警 + 人工确认，不静默选
- **`default_embedding` 全局唯一**：迁移时多租户标记冲突 → 保留先创建者，其余落库后清理
- **rerank 能力模型**：cohere 进全局 providers 需新增 kind 支持，`rerank/cohere.go` 走独立 HTTP 服务（非 ModelRegistry），目录化只约束「模型选择」，调用链路不变
- **业务面 embedding 不可变**：workspace 创建后 `embedding_model` 不可变（`ErrEmbeddingModelImmutable`），白名单收敛不影响存量 workspace
- **API 兼容**：provider/model 的 HTTP DTO 契约变更由 contract_test + golden 守护，破坏性变更单独评审
