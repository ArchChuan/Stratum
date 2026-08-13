# 模型管理重构实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 memory facts 提取零产出 bug（LLM 调用不传 Model），同时将 providers/models 从 per-tenant 提升为 public 平台全局目录，model_profiles 补 extraction/judge 模型成为 memory 全机制 + fact-check 唯一解析点。

**Architecture:** providers/models 去 tenant_id 迁入 public schema；ModelRegistry 去租户签名 + 单层缓存 + 全局 5 级解析链兜底；mechanism 消费方经 `model_profiles` 拿模型名 → 全局解析链 resolve；跨 schema 存量数据用一次性 Go 工具迁移；前端全局目录 UI + 档案模型字段 Select 化。

**Tech Stack:** Go 1.25 / Gin 1.9 / pgx 5.9 / golang-migrate / React 18.3 / Ant Design 5.20 / Vite 6.4。

## Global Constraints

- 编号迁移（`pkg/migration/sql/`）只操作 public schema，禁止引用 tenant-only 表；tenant DDL 唯一基线是 `pkg/storage/postgres/tenant_schema.sql`（`docs/agent/migration-tenant.md`）。
- 新表/索引用 `IF NOT EXISTS`；新列用 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`。
- 所有访问 tenant-scoped 表的 repository 走 `execTenant`；public 表直连（仿 `ProfileRepo` 的 pgxPool 模式）。
- DDD：mechanism 不得 import llmgateway → 模型存在性校验走 mechanism 自有 port `ModelExists(ctx, model, capability)`，wiring 薄适配。
- 代码质量门禁：圈复杂度 ≤10、认知 ≤15、函数 ≤120 行、嵌套 ≤4；行为数字放 `pkg/constants/` 或包内 `defaults.go`。
- API key 密文 `crypto.EncryptSecret`（同一 DataEncryptionKey）→ 迁移密文直搬，无明文中转；provider 返回路径不回传 api_key。
- HTTP JSON 契约唯一事实源是 `proto/` 下 .proto，改契约后 `make proto-gen`。
- 后端快速验证 `go vet && go test -short ./...`；PR 前 `make test-verify-before-pr`（走 `stratum-e2e-development` skill，禁止绕过）。

---

## Task 1: 035 迁移 —— public providers/models 平台目录

**Files:**

- Create: `pkg/migration/sql/035_platform_model_catalog.up.sql`
- Create: `pkg/migration/sql/035_platform_model_catalog.down.sql`

**Interfaces:**

- Produces: public schema 表 `providers`（`UNIQUE(name)`）、`models`（`UNIQUE(provider_id,name)` + 全局唯一 partial index `idx_models_default_embedding`，去 tenant 谓词）。

- [ ] **Step 1: 写 up 迁移**

`035_platform_model_catalog.up.sql`（spec 4.1）：

```sql
-- 035: 平台全局模型目录（public schema，去 tenant_id）
-- providers/models 从 tenant schema 提升为平台全局资源，账户统一走平台模型/凭据。

CREATE TABLE IF NOT EXISTS providers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    kind          TEXT NOT NULL,
    base_url      TEXT NOT NULL DEFAULT '',
    api_key       TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL DEFAULT '',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS models (
    id                TEXT PRIMARY KEY,
    provider_id       TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    name              TEXT NOT NULL,
    display_name      TEXT NOT NULL DEFAULT '',
    capabilities      TEXT[] NOT NULL DEFAULT '{}',
    context_window    INT NOT NULL DEFAULT 0,
    max_tokens        INT NOT NULL DEFAULT 0,
    input_price       DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_price      DOUBLE PRECISION NOT NULL DEFAULT 0,
    recommended       BOOLEAN NOT NULL DEFAULT false,
    enabled           BOOLEAN NOT NULL DEFAULT true,
    provider_managed  BOOLEAN NOT NULL DEFAULT false,
    default_embedding BOOLEAN NOT NULL DEFAULT false,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(provider_id, name)
);
CREATE INDEX IF NOT EXISTS idx_models_provider ON models(provider_id);
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(enabled);
CREATE UNIQUE INDEX IF NOT EXISTS idx_models_default_embedding
    ON models WHERE default_embedding AND 'embedding' = ANY(capabilities);
```

`035_platform_model_catalog.down.sql`：

```sql
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS providers;
```

- [ ] **Step 2: 验证迁移链**

Run: `make db-migrate-up` 或本地 infra 后 `golang-migrate up`。Expected: 035 applied；`\d public.providers` 无 tenant_id；`\d public.models` 唯一约束正确。

- [ ] **Step 3: Commit**

```bash
git -C ../stratum-model-mgmt add pkg/migration/sql/035_platform_model_catalog.up.sql pkg/migration/sql/035_platform_model_catalog.down.sql
git -C ../stratum-model-mgmt commit -m "feat(llmgateway): 035 public 平台模型目录迁移"
```

---

## Task 2: tenant_schema.sql 移除 per-tenant providers/models

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql:1481-1530`

**Interfaces:**

- Produces: 新租户不再建 `providers`/`models`；存量租户下次 provision 由 DROP 语句幂等清理（迁移工具已搬数据后）。

- [ ] **Step 1: 删除 DDL 段**

删除 `tenant_schema.sql` 1481-1530（Provider & Model Registry 段，含 `providers`/`models` 两张表 + 3 索引 + default_embedding 列 + partial unique index），整段替换为：

```sql
-- =============================================================================
-- Provider & Model Registry（已迁移到 public schema，见 035_platform_model_catalog）
-- 存量租户清理（迁移工具 cmd/model-migrate 跑完后幂等执行）
-- =============================================================================
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS providers;
```

- [ ] **Step 2: 验证 schema 顺序测试**

Run: `go test ./pkg/storage/postgres/... -short`（覆盖 tenant schema 应用与历史顺序）。Expected: 通过；`rg "idx_models_tenant|tenant_id" pkg/storage/postgres/tenant_schema.sql` 无 providers/models 残留。

- [ ] **Step 3: Commit**

```bash
git -C ../stratum-model-mgmt add pkg/storage/postgres/tenant_schema.sql
git -C ../stratum-model-mgmt commit -m "refactor(storage): tenant_schema 移除 per-tenant providers/models"
```

---

## Task 3: 一次性迁移工具 cmd/model-migrate

**Files:**

- Create: `cmd/model-migrate/main.go`
- Create: `cmd/model-migrate/migrate_test.go`

**Interfaces:**

- Consumes: public `providers`/`models`（Task 1）、tenant schema `providers`/`models`（迁移后仍存在，直到 Task 2 清理落地）。
- Produces: 无（用完即弃，不留启动路径）。

**归并规则（spec 10）：**

- provider 按 `name` 归并；同 name 多 key 冲突 → 取 `enabled` 且 `updated_at` 最新，冲突告警不静默。
- model 按 `(provider_id, name)` 归并；`default_embedding` 全局唯一冲突 → 保留先创建者，多余标记清理 + 告警。
- API key 密文原样读（同一 DataEncryptionKey），不解密不重加密。
- 对账：迁移后 public 行数 = 归并后预期行数，打印清单待人工核验。

- [ ] **Step 1: 写工具骨架 + dry-run 对账测试**

`cmd/model-migrate/main.go`：flag `--dry-run`（默认 true，只打印计划不改库）。核心函数：

- `discoverTenantSchemas(ctx, db) []string`：`information_schema.schemata` 排除 `public`/`pg_*`。
- `collectTenantProviders(ctx, db, schema) []tenantProvider`：读 tenant 表（`SET LOCAL search_path` 或 schema 限定 SQL）。
- `mergePlan(providers []tenantProvider) mergePlan`：归并 + 冲突告警清单。
- `apply(ctx, db, plan)`：`--dry-run=false` 时写 public 表。

`migrate_test.go`：表驱动测 `mergePlan`——「同 name provider key 冲突取最新 enabled」「default_embedding 冲突保留先创建者」「无冲突正常归并」。

- [ ] **Step 2: 本地集成验证**

Run: `go test ./cmd/model-migrate/...`；本地 infra 起 Postgres + 造两个 tenant schema 数据后 `go run ./cmd/model-migrate --dry-run`（默认）打印归并清单与冲突告警。Expected: 清单行数 = 预期；冲突有告警不静默。

- [ ] **Step 3: Commit**

```bash
git -C ../stratum-model-mgmt add cmd/model-migrate/
git -C ../stratum-model-mgmt commit -m "feat(model-migrate): 一次性存量 providers/models 迁移工具"
```

> 注：远端执行需用户明确许可；E2E 优先本地 Docker。

---

## Task 4: provider/model repo 改 public 直连

**Files:**

- Modify: `internal/llmgateway/infrastructure/provider_repo.go`
- Modify: `internal/llmgateway/infrastructure/model_repo.go`

**Interfaces:**

- Consumes: 035 public 表（Task 1）。
- Produces: 两 repo 去 `tenantID` 参数、去 `execTenant`，改 pgxPool 直连（仿 `internal/mechanism/infrastructure/persistence/profile_repo.go` 的 `pgxPool` 接口）；SQL 去 tenant 谓词、唯一键改 `(name)` / `(provider_id,name)`。

- [ ] **Step 1: provider_repo 去租户**

删除 `execTenant` 与 `postgres` import；struct 改 `{db pgxPool}`；所有方法去 `tenantID` 参数，SQL：

- `SELECT ... FROM providers WHERE name=$1`（原 `tenant_id=$1 AND name=$2`）
- `INSERT INTO providers (id, name, ...) VALUES ($1,$2,...)`（去 tenant_id）
- `UPDATE providers ... WHERE id=$1`；`UPDATE ... SET default_model=$1 WHERE id=$2 AND enabled`
- Delete/List 同理；`api_key` 加密 `crypto.EncryptSecret(r.key, ...)` 保留。

- [ ] **Step 2: model_repo 去租户**

同法：去 `tenantID`/`execTenant`，`SELECT ... FROM models`（去 `tenant_id` 谓词），唯一查询 `WHERE provider_id=$1 AND name=$2`；`SetDefaultEmbedding` 的 clear-then-set 去 tenant 谓词（`WHERE id<>$1` 全局）；`idx_models_default_embedding` 已全局唯一。

- [ ] **Step 3: 编译 + 单测**

Run: `go build ./... && go test -short ./internal/llmgateway/infrastructure/...`. Expected: 编译通过；repo 单测（pgxmock）同步去 `tenantID`。⚠️ 修改 port 后立即同步所有 test mock/stub。

- [ ] **Step 4: Commit**

```bash
git -C ../stratum-model-mgmt add internal/llmgateway/infrastructure/provider_repo.go internal/llmgateway/infrastructure/model_repo.go
git -C ../stratum-model-mgmt commit -m "refactor(llmgateway): provider/model repo public 直连去租户"
```

---

## Task 5: ModelRegistry 去租户 + 全局 5 级解析链

**Files:**

- Modify: `internal/llmgateway/infrastructure/model_registry.go`
- Modify: `internal/llmgateway/infrastructure/model_registry_test.go`

**Interfaces:**

- Produces: 全局签名——`Resolve(ctx, model)`、`ResolveEmbedding(ctx, m)`、`ResolveFallbackCandidates(ctx, p)`、`ResolveDefaultEmbeddingModel(ctx)`、`Invalidate()`；`WarmTenant(tenantID)` 删除 → 启动全目录预热一次。缓存 `map[string]*resolvedEntry` 单层。
- 解析链（spec 6）：① 显式 model 精确匹配（enabled + provider enabled + 能力）→ ② `provider.default_model` → ③ `models.recommended`（全局 chat/embed）→ ④ `default_embedding` 标记 → ⑤ fail-closed 报错。空 model 不再走「not found」而是走 ②③ 兜底，全链空才 fail-closed。

- [ ] **Step 1: 重构签名与缓存**

方法签名去 `tenantID`；缓存 `map[tenantID]map[...]` → `map[...]` 单层；`WarmTenant` 改 `Warm(ctx)` 全目录预热；`Invalidate` 去 tenantID。`cacheGet`/`cacheSet` 同步。

- [ ] **Step 2: 实现 5 级解析链**

`Resolve(ctx, model)`：`model==""` 时跳过 ①，直接 ②③④ 兜底；非空时 ① 精确命中失败 → ② provider.default_model（若 provider enabled 且能力匹配）→ ③ recommended → ④ default_embedding → ⑤ `fmt.Errorf("model %q not resolved: no default model in global catalog", model)`。`ResolveEmbedding`/`ResolveDefaultEmbeddingModel` 同理（④ 专用）。

- [ ] **Step 3: 测试**

`model_registry_test.go`：现有 `TestResolverTwoLevelFallback` 扩展为全局解析链 5 级用例 + 新增「目录为空 fail-closed」「②③ 兜底」「空 model 命中 provider.default_model」用例。

- [ ] **Step 4: Commit**

```bash
git -C ../stratum-model-mgmt add internal/llmgateway/infrastructure/model_registry.go internal/llmgateway/infrastructure/model_registry_test.go
git -C ../stratum-model-mgmt commit -m "refactor(llmgateway): ModelRegistry 去租户 + 全局 5 级解析链"
```

---

## Task 6: 调用方签名同步（编译绿）

**Files:**

- Modify: `api/wiring/{tenant_resolver,knowledge,iam,agent,platform,llmgateway,memory}.go`
- Modify: `internal/llmgateway/application/model_service.go`
- Modify: `internal/llmgateway/infrastructure/gateway.go`
- Modify: `api/http/handler/user_memory_handler.go:33`
- Modify: resolver 测试（`TestNewTenantCapabilityResolver...`/`TestResolverTwoLevelFallback`）

**Interfaces:**

- Consumes: Task 5 的新签名。
- Produces: 全仓编译绿；`tenantCapabilityResolver.ResolveLLM/ResolveWorkerLLM` 删 WarmTenant 预热，直接全局 resolve。

- [ ] **Step 1: 同步调用点**

全局 `rg "\.Resolve\(|WarmTenant|ResolveFallbackCandidates|ResolveDefaultEmbeddingModel|\.Invalidate\("` 找出所有调用点，逐个去 `tenantID` 参数；删 `WarmTenant` 调用改启动预热。

- [ ] **Step 2: 编译 + 测试**

Run: `go build ./... && go vet ./... && go test -short ./...`. Expected: 全绿；无残留 tenantID 签名调用。

- [ ] **Step 3: Commit**

```bash
git -C ../stratum-model-mgmt commit -am "refactor(llmgateway): 调用方同步去租户签名"
```

---

## Task 7: BaselineModels 补 extraction/judge + seed

**Files:**

- Modify: `internal/mechanism/domain/profile.go`
- Modify: `internal/mechanism/application/baseline_service.go`（seed `DefaultBaseline`）
- Modify: `internal/mechanism/infrastructure/persistence/profile_repo.go`（如需 seed 落库）

**Interfaces:**

- Produces: `BaselineModels{EnrichModel,SummaryModel,ExtractionModel,JudgeModel}`；`BaselinePrompts` 补 `AgentFactCheck` 键（json `agent_factcheck`）。seed：enrich=qwen-turbo / summary=qwen-plus / extraction=qwen-plus / judge=qwen-turbo（对齐 config 现状）。

- [ ] **Step 1: profile.go 补字段**

`BaselineModels` 加 `ExtractionModel string \`json:"extraction_model,omitempty"\``、`JudgeModel string \`json:"judge_model,omitempty"\``；`BaselinePrompts` 加 `AgentFactCheck string \`json:"agent_factcheck,omitempty"\``。

- [ ] **Step 2: seed 补值**

`DefaultBaseline()`（baseline_service.go 内）models 补 `ExtractionModel: "qwen-plus", JudgeModel: "qwen-turbo"`；prompts 补 `AgentFactCheck` 默认（从 `internal/agent/application/factcheck/` LLM judge 实现提取现有 prompt，见 Task 10）。

- [ ] **Step 3: 测试 + Commit**

Run: `go test -short ./internal/mechanism/...`. Expected: 通过（含 seed 断言）。Commit：`git -C ../stratum-model-mgmt commit -am "feat(mechanism): BaselineModels 补 extraction/judge + agent_factcheck 键"`。

---

## Task 8: mechanism ModelExists port + 校验注入

**Files:**

- Create: `internal/mechanism/domain/port/model_exists.go`
- Modify: `internal/mechanism/application/baseline_service.go`
- Modify: `api/wiring/mechanism.go`

**Interfaces:**

- Produces: port `type ModelExists interface { Exists(ctx context.Context, model string, capability domain.ModelCapability) (bool, error) }`（capability 用 llmgateway domain 值语义，mechanism 侧定义等价 string 常量避免 import llmgateway）。
- `Service` 构造加可选 `modelExists`；`UpsertProfile` 在 `p.Validate()` 后校验 `EnrichModel/SummaryModel/ExtractionModel/JudgeModel` 非空字段存在于全局目录（capability=chat），不存在返回 `ErrInvalidProfile`。
- wiring 用全局 `ModelRegistry` 适配 `Exists`。

- [ ] **Step 1: 定义 port + 注入**

新建 `port/model_exists.go`；`NewService(repo, modelExists)`；`UpsertProfile` 校验逻辑（空字段跳过，非空必须存在）。

- [ ] **Step 2: wiring 适配**

`wiring/mechanism.go`：`NewService(profileRepo, &modelExistsAdapter{registry})`。⚠️ mechanism 不 import llmgateway——adapter 放 wiring。

- [ ] **Step 3: 测试 + Commit**

Run: `go build ./... && go test -short ./internal/mechanism/...`. Expected: 通过；新增「档案引用不存在模型被拒」用例。Commit：`git -C ../stratum-model-mgmt commit -am "feat(mechanism): UpsertProfile 模型存在性校验"`。

---

## Task 9: memory 4 机制统一 profile 解析（bug 根治）

**Files:**

- Modify: `internal/memory/infrastructure/pipeline/llm_extractor.go`
- Modify: `internal/memory/infrastructure/workers/history_summarizer.go`
- Modify: `internal/memory/infrastructure/enricher.go`（切 profile 统一解析）
- Modify: `internal/memory/infrastructure/pipeline/llm_superseder.go`
- Modify: 对应测试

**Interfaces:**

- Consumes: `GetEffective(ctx, model).Models.{ExtractionModel,SummaryModel,EnrichModel}`（Task 7）。
- Produces: 四机制 `CompletionRequest` 均显式设 `Model`；解析链兜底生效。compaction 不动（无 LLM）。

- [ ] **Step 1: llm_extractor 设 Model**

`ExtractFacts` 构造请求时 `Model: baseline.Models.ExtractionModel`（先经全局 `Resolve` 兜底，模型被删走 ②③；解析失败 → 透传错误，禁止默认放行）。回归测试：断言请求 `Model == profile.ExtractionModel`。

- [ ] **Step 2: history_summarizer 设 Model + 接死配置**

`SummarizeHistory` 设 `Model = baseline.Models.SummaryModel`；删/接 `MEMORY_SUMMARY_MODEL` 死配置（spec 7.1）。

- [ ] **Step 3: enricher/superseder 切 profile**

enrich/supersede 从 `cfg.EnrichModel` 切到 `baseline.Models.EnrichModel` 统一解析。

- [ ] **Step 4: 测试 + Commit**

Run: `go test -short ./internal/memory/...`. Expected: 补「不设 Model → 解析出模型名」回归用例全过。Commit：`git -C ../stratum-model-mgmt commit -am "fix(memory): 四机制设 Model 统一从 profile 解析"`。

---

## Task 10: fact-check judge 切 profile

**Files:**

- Modify: `internal/agent/application/factcheck/checker.go`（judge 模型来源）
- Modify: `internal/agent/application/factcheck/judge_llm.go`（prompt 提取到 `BaselinePrompts.AgentFactCheck`）
- Modify: 相关测试

**Interfaces:**

- Consumes: `GetEffective(ctx, model).Models.JudgeModel`（Task 7）。
- Produces: judge 模型从 `cfg.AgentFactCheck.JudgeModel`（env）切到 profile；prompt 从 judge 实现提取到 `agent_factcheck` 键。

- [ ] **Step 1: judge 切 profile**

`checker.go`：judge 模型解析改走 `GetEffective(...).Models.JudgeModel` + 全局解析链。回归测试断言 judge 请求模型名。

- [ ] **Step 2: 提取 prompt**

LLM judge 实现内现有 prompt 提取为 `BaselinePrompts.AgentFactCheck` 默认值（seed 补，Task 7 落点）。

- [ ] **Step 3: 测试 + Commit**

Run: `go test -short ./internal/agent/application/factcheck/...`. Commit：`git -C ../stratum-model-mgmt commit -am "feat(agent): fact-check judge 模型切 model_profiles"`。

---

## Task 11: embedding 白名单 / rerank / 死配置收敛

**Files:**

- Modify: `internal/knowledge/domain/workspace.go`（删 `AllowedEmbeddingModels`，`WorkspaceConfig.Validate()` 改目录存在性；`AllowedRerankIdentities` 校验改目录）
- Modify: `config/config.go`（删死配置 `RerankModel`；`MemoryPipelineConfig` 补 extraction）
- Modify: `pkg/constants`（`AgentFactCheckJudgeModel` 收敛后清理）
- Modify: `internal/llmgateway/...` rerank kind 支持（cohere 进全局 providers）

**Interfaces:**

- Produces: 硬编码 embedding/rerank 白名单删除；校验经 `ModelExists(ctx, model, capability)`（embedding/rerank 能力）；死配置清理。

- [ ] **Step 1: 删白名单改目录校验**

`WorkspaceConfig.Validate()`：embedding 模型 → 全局目录存在 + enabled + embedding 能力；rerank identity → 目录存在 + rerank 能力。注入方式：wiring 传 `ModelExists` port（knowledge 侧自有 port 或复用 mechanism 风格）。

- [ ] **Step 2: 死配置清理**

`config.go` 删 `RerankModel`；`AgentFactCheckJudgeModel` 若被 Task 10 完全替代则删；`MemoryPipelineConfig` 补 extraction model 字段（若仍留 config 兜底）或删（已由 profile 兜底）。

- [ ] **Step 3: 测试 + Commit**

Run: `go build ./... && go test -short ./internal/knowledge/...`. Commit：`git -C ../stratum-model-mgmt commit -am "refactor(knowledge): embedding/rerank 白名单收敛到全局目录"`。

---

## Task 12: 前端全局目录 UI + 档案 Select 化

**Files:**

- Modify: `web/src/modules/llm/`（ModelListPage/ModelManagementPage/ProviderListPage/ModelEditDrawer/ProviderForm/DiscoverResultModal + `hooks/useModels.ts`/`useProviders.ts`）
- Modify: `web/src/modules/mechanism/components/ProfileEditDrawer.tsx`（enrich/summary Input→Select，新增 extraction/judge Select）
- Modify: 业务面表单（AgentFormSections / WorkspaceCreateModal / SystemAssistantSettingsForm 等模型下拉数据源）
- Modify: `proto/` 契约 + `make proto-gen`

**Interfaces:**

- Consumes: 全局模型列表 API（`GET /api/v1/models` 去 tenant 维度或新增全局目录端点）；`ProfileBaseline` 类型补 `extraction_model`/`judge_model`。
- Produces: llm 模块租户自管 → 平台全局目录（对齐 #361 仅 global admin 可见）；档案模型字段 Select 化数据源 = 全局 chat 模型；业务面表单下拉数据源切全局目录。

- [ ] **Step 1: mechanism API/类型补字段**

`web/src/modules/mechanism/model/mechanism.ts`：`ProfileBaseline` 加 `extraction_model?`/`judge_model?`；`.api.ts` 对齐。

- [ ] **Step 2: ProfileEditDrawer Select 化**

enrich/summary `Input`（:170-175）→ `Select`（options = 全局 chat 模型列表）；新增 extraction_model/judge_model 两个 `Select`。数据源 hook：拉全局目录 chat 模型。

- [ ] **Step 3: llm 模块全局化**

ModelManagementPage/ProviderListPage 去 tenant 维度、入口收进平台管理（global admin）。业务面表单下拉数据源统一指向全局目录列表。

- [ ] **Step 4: 前端验证 + Commit**

Run: `make fe-lint && make fe-build`. Commit：`git -C ../stratum-model-mgmt commit -am "feat(web): 全局模型目录 + 档案模型 Select 化"`。

---

## Task 13: 全量验收（测试 / 契约 / E2E）

**Files:**

- 全仓回归

- [ ] **Step 1: 契约与仓库级扫描**

Run: `make proto-gen && go vet ./... && go test -short ./...`；`make fe-build`。仓库级 `rg` 扫描：无残留 per-tenant model 引用、无硬编码 embedding 白名单、无 `execTenant` 残留（provider/model repo）。

- [ ] **Step 2: 系统验收**

在 clean commit 上走 `stratum-e2e-development` skill → `make test-verify-before-pr`；本地 Docker 验证迁移工具 dry-run 对账。远端迁移执行需用户明确许可。

- [ ] **Step 3: PR 更新**

`git -C ../stratum-model-mgmt push`；更新 PR #363 描述（spec → 实现）或合并后新建实现 PR。
