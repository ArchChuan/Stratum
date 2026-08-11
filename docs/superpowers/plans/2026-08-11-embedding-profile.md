# Embedding Profile 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 嵌入模型配置收敛为模型管理单一配置点（`models.default_embedding`），换模型走新 collection 隔离，维度推导收敛为单一事实源，并补四层异常感知。

**Architecture:** 数据面：`models` 表加 `default_embedding` 标记（partial unique index 保证单默认），repo 层自清理防悬空。解析面：`ModelRegistry.ResolveDefaultEmbeddingModel` 统一 3 处消费方。存储面：Milvus collection 名编码模型后缀，查询路径做 legacy 回退（新名优先，旧名兜底），维度映射收敛到 `pkg/constants.DimensionForModel`。异常面：指标 + DLQ 事件存原始 payload + 定向重放 API。

**Tech Stack:** Go 1.25、pgx v5（pgxmock 单测）、Gin、NATS JetStream（embedded NATS 测试）、Milvus、React 18 + Ant Design 5 + vitest。

**架构落点偏差（相对 spec，原因必须保留）：**

1. spec 的 `embedding.DimensionForModel` 落点为 **`pkg/constants.DimensionForModel`**：knowledge `application` 禁止 import 兄弟 context 的 `infrastructure`（CLAUDE.md DDD 依赖方向），且维度映射是跨包行为数字（CLAUDE.md 要求入 `pkg/constants/<domain>.go`）。`embedding.GetVectorDimension()` 委托它，保持单一事实源。
2. spec 的 `san` 复用 `milvusUnsafe`：`pkg/constants` 新增导出函数 `SanitizeMilvusName`（现有 `milvusUnsafe` 正则包装），knowledge 与 memory 复用同一实现。

## Global Constraints

- 全部分支：`feat/embedding-profile`，worktree 从最新 `origin/main` 创建（`bash scripts/new-worktree.sh ../stratum-embedding-profile feat/embedding-profile`），禁止在 main 提交。
- 每 task 独立 commit，标题 `[feat|fix](scope): description`；提交前 `git add` 只含本 task 文件。
- 所有 tenant-scoped 表访问必须走 `execTenant`；新 SQL 必须幂等（`IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`）。
- 行为数字禁止内联：跨包常量入 `pkg/constants`，包内常量入本文件 `const` 块。
- 测试：表驱动；mock 外部依赖不 mock 领域逻辑；`go test -short ./...` 必须全绿；修改 port 后同步所有 test mock/stub。
- 维度映射（单一事实源，逐字）：`text-embedding-v1 → 1536`、`text-embedding-v2 / v3 / v4 → 1024`、`embedding-3 → 2048`、`default → 1536`。
- collection 命名（逐字）：memory `memory_<tenant>_<san(model)>` / `memory_facts_<tenant>_<san(model)>`；knowledge `kb_<workspaceID>_<san(model)>`（前缀 `kb`，不是 `kn`）。旧名（无 model 后缀）为 legacy 名。
- 换模型语义：写入总是新名；查询新名失败回退旧名；同维度换模型也隔离；legacy 回退仅覆盖存量升级，不 re-embed、不迁移。
- 悬空保护 SQL 表达式（逐字）：`default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)`；启用/重启用时标记不自动恢复。
- fail-closed：registry 无可用嵌入模型 → 消费方返回空（不默认放行），embedder 走 DLQ（error_code `embed_service_unavailable`）。
- `PUT /admin/models/:id/default-embedding` 挂 `RequireTenantRole("admin")`（`/admin/models` 组 adminMW），禁止挂 member 组写路径。
- DLQ 重放：tenantID 必须从死信事件派生（禁止从请求 body 传入）；重放先发布后标记（`replayed`），publish 失败不置标记；重放计数上限。
- 禁止输出 token、cookie、secret、password 或原始 API key。

---

### Task 1: 数据模型 —— `models.default_embedding` 标记（DDL + domain + port + repo）

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`（models 表定义后，:1480 附近）
- Modify: `internal/llmgateway/domain/model.go:17-33`（`Model` struct）
- Modify: `internal/llmgateway/domain/port/model_repo.go:17-25`（`ModelRepository` interface）
- Modify: `internal/llmgateway/infrastructure/model_repo.go`（Get/List/Update/Toggle/UpsertDiscovered + 新增 SetDefaultEmbedding）
- Test: `internal/llmgateway/infrastructure/model_repo_internal_test.go`（新增测试）
- Test: `internal/llmgateway/application/model_mgmt_service_test.go`（`modelMgmtRepo` mock 补方法）
- 同步所有其他 `ModelRepository` mock（`grep -rln "ModelRepository" internal/ --include="*_test.go"` 逐个补 `SetDefaultEmbedding`）

**Interfaces:**

- Consumes: `postgres.ExecTenantWith`（现有）、`pgxmock` 测试基建（`model_repo_internal_test.go` 现有模式）
- Produces: `domain.Model.DefaultEmbedding bool`（json tag `defaultEmbedding`）；`port.ModelRepository.SetDefaultEmbedding(ctx, tenantID, id string, enabled bool) error`（单事务 clear-then-set）；Get/List 返回含 `DefaultEmbedding`

- [ ] **Step 1: 写失败测试（repo 单测，pgxmock）**

追加到 `model_repo_internal_test.go`（先看现有文件复用 `newMockPool`/expect 模式；若无现成 helper，用 `pgxmock.NewPool()` + `p.ExpectBegin()`/`p.ExpectExec` 断言事务）：

```go
func TestPgModelRepoSetDefaultEmbedding(t *testing.T) {
 tests := []struct {
  name       string
  enabled    bool
  expectErr  bool
  expectSQLs []string // 事务内期望执行顺序
 }{
  {
   name: "sets default clears others atomically in one transaction",
   enabled: true,
   expectSQLs: []string{
    `UPDATE models SET default_embedding=false WHERE tenant_id=$1 AND id<>$2`,
    `UPDATE models SET default_embedding=true WHERE id=$1 AND tenant_id=$2 AND enabled AND 'embedding' = ANY\(capabilities\)`,
   },
  },
  {
   name: "clears default for target only when disabled",
   enabled: false,
   expectSQLs: []string{
    `UPDATE models SET default_embedding=false WHERE id=$1 AND tenant_id=$2`,
   },
  },
  {
   name: "fails closed when target not enabled embedding model",
   enabled: true,
   expectErr: true,
  },
 }
 for _, tc := range tests {
  t.Run(tc.name, func(t *testing.T) {
   p, err := pgxmock.NewPool()
   if err != nil { t.Fatal(err) }
   p.ExpectBegin()
   for _, sql := range tc.expectSQLs {
    p.ExpectExec("UPDATE models").WillReturnResult(pgxmock.NewResult("UPDATE", 1))
   }
   if tc.expectErr {
    // set 目标时 RowsAffected=0 → 返回错误
   }
   p.ExpectCommit()
   repo := NewPgModelRepo(p)
   err = repo.SetDefaultEmbedding(context.Background(), "tenant-a", "model-1", tc.enabled)
   if tc.expectErr && err == nil {
    t.Fatal("expected error, got nil")
   }
   if err := p.ExpectationsWereMet(); err != nil { t.Fatal(err) }
  })
 }
}
```

再补 Toggle/Update 自清理断言（骨架同上，SQL 含自清理表达式）和 Get/List 新列扫描（SELECT 列表含 `default_embedding`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -short ./internal/llmgateway/infrastructure/ -run TestPgModelRepo -v`
Expected: FAIL（`SetDefaultEmbedding` undefined / SELECT 列数不匹配）

- [ ] **Step 3: DDL —— tenant_schema.sql**

在 models 表 `CREATE INDEX IF NOT EXISTS idx_models_enabled` 之后追加（幂等，历史租户由 provision 流程执行同一文件）：

```sql
-- 默认嵌入模型标记（本设计唯一配置点）：partial unique index 保证同 tenant
-- 最多一个默认。index WHERE 无 enabled 谓词——DB 层不防悬空，靠 repo 自清理联动。
ALTER TABLE models ADD COLUMN IF NOT EXISTS default_embedding BOOLEAN NOT NULL DEFAULT false;

CREATE UNIQUE INDEX IF NOT EXISTS idx_models_default_embedding
    ON models (tenant_id)
    WHERE default_embedding AND 'embedding' = ANY(capabilities);
```

- [ ] **Step 4: domain + port**

`domain/model.go`（`Recommended` 之后）：

```go
 DefaultEmbedding bool              `json:"defaultEmbedding"`
```

`domain/port/model_repo.go` interface 追加：

```go
 // SetDefaultEmbedding 设置或取消模型的默认嵌入标记。enabled=true 时在
 // 单事务内清除同 tenant 其他默认标记后设置目标；目标必须 enabled 且
 // capability 含 embedding（fail-closed），否则返回错误。
 SetDefaultEmbedding(ctx context.Context, tenantID, id string, enabled bool) error
```

- [ ] **Step 5: repo 实现 —— Get/List/Update/Toggle/UpsertDiscovered/SetDefaultEmbedding**

`model_repo.go`：

1. Get SELECT 加列（`recommended, enabled, provider_managed` → `recommended, default_embedding, enabled, provider_managed`），Scan 加 `&m.DefaultEmbedding`。List 的 query 与 Scan 同样改。

2. Update（:128-133）——SET 列表追加自清理表达式：

```go
  tag, err := tx.Exec(ctx,
   `UPDATE models SET display_name=$1, capabilities=$2, context_window=$3, max_tokens=$4,
    input_price=$5, output_price=$6, recommended=$7, enabled=$8, updated_at=now(),
    default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)
    WHERE id=$9 AND tenant_id=$10`,
   m.DisplayName, caps, m.ContextWindow, m.MaxTokens,
   m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ID, tenantID,
  )
```

1. Toggle（:239-241）——SET 追加自清理（`enabled=$1` 后）：

```go
  tag, err := tx.Exec(ctx,
   `UPDATE models SET enabled=$1, updated_at=now(),
    default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)
    WHERE id=$2 AND tenant_id=$3`,
   enabled, id, tenantID)
```

1. UpsertDiscovered disable 阶段（:151-156）——追加自清理（统一表达式，enabled=false 时标记必清）：

```go
  if _, err := tx.Exec(ctx,
   `UPDATE models SET enabled=false, updated_at=now(),
    default_embedding = default_embedding AND enabled AND 'embedding' = ANY(capabilities)
    WHERE tenant_id=$1 AND provider_id=$2 AND provider_managed=true`,
   tenantID, providerID); err != nil {
```

（re-enable 阶段不动 default_embedding——"启用不自动恢复"。）

1. 新增 `SetDefaultEmbedding`（单事务 clear-then-set，partial unique index 为最后防线）：

```go
// SetDefaultEmbedding sets or clears the default-embedding marker for a model.
// enabled=true clears all other markers for the tenant first, then sets the
// target in the same transaction (atomic clear-then-set). The target must be
// enabled and carry the embedding capability; otherwise the call fails closed.
func (r *PgModelRepo) SetDefaultEmbedding(ctx context.Context, tenantID, id string, enabled bool) error {
 return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  if !enabled {
   tag, err := tx.Exec(ctx,
    `UPDATE models SET default_embedding=false WHERE id=$1 AND tenant_id=$2`, id, tenantID)
   if err != nil {
    return fmt.Errorf("clear default embedding: %w", err)
   }
   if tag.RowsAffected() == 0 {
    return fmt.Errorf("model not found: %s", id)
   }
   return nil
  }
  if _, err := tx.Exec(ctx,
   `UPDATE models SET default_embedding=false WHERE tenant_id=$1 AND id<>$2`,
   tenantID, id); err != nil {
   return fmt.Errorf("clear other default embeddings: %w", err)
  }
  tag, err := tx.Exec(ctx,
   `UPDATE models SET default_embedding=true WHERE id=$1 AND tenant_id=$2 AND enabled AND 'embedding' = ANY(capabilities)`,
   id, tenantID)
  if err != nil {
   return fmt.Errorf("set default embedding: %w", err)
  }
  if tag.RowsAffected() == 0 {
   return fmt.Errorf("model not found or not an enabled embedding model: %s", id)
  }
  return nil
 })
}
```

- [ ] **Step 6: 同步所有 ModelRepository mock**

`grep -rln "ModelRepository" internal/ --include="*_test.go"` → 每个 mock struct 补：

```go
func (m *xxxRepo) SetDefaultEmbedding(context.Context, string, string, bool) error { return m.err }
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test -short ./internal/llmgateway/...`
Expected: PASS（含 G1 事务序列、G2 自清理断言）

- [ ] **Step 8: DDL 文本测试**

新建 `pkg/storage/postgres/tenant_schema_test.go`（若已存在则追加）：

```go
func TestTenantSchemaHasDefaultEmbeddingColumnAndIndex(t *testing.T) {
 schema, err := os.ReadFile("tenant_schema.sql")
 if err != nil { t.Fatal(err) }
 text := string(schema)
 for _, want := range []string{
  "ADD COLUMN IF NOT EXISTS default_embedding BOOLEAN NOT NULL DEFAULT false",
  "idx_models_default_embedding",
  "WHERE default_embedding AND 'embedding' = ANY(capabilities)",
 } {
  if !strings.Contains(text, want) {
   t.Errorf("tenant_schema.sql missing %q", want)
  }
 }
}
```

Run: `go test -short ./pkg/storage/postgres/ -run TestTenantSchema -v` → PASS

- [ ] **Step 9: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql internal/llmgateway/domain/model.go internal/llmgateway/domain/port/model_repo.go internal/llmgateway/infrastructure/model_repo.go internal/llmgateway/infrastructure/model_repo_internal_test.go pkg/storage/postgres/tenant_schema_test.go
git commit -m "feat(llmgateway): models.default_embedding 标记 + repo 自清理 + SetDefaultEmbedding 单事务"
```

---

### Task 2: 维度单一事实源 —— `pkg/constants.DimensionForModel`

**Files:**

- Create: `pkg/constants/embedding.go`
- Modify: `pkg/constants/knowledge.go`（导出 `SanitizeMilvusName`）
- Modify: `internal/llmgateway/infrastructure/embedding/embedding.go:95-106`（`GetVectorDimension` 委托）
- Modify: `internal/knowledge/application/workspace_service.go:37-47`（删除 `vectorDim`，:144 替换）
- Modify: `internal/knowledge/application/ingest_service.go:347`（替换）
- Modify: `internal/knowledge/application/rag_service.go:561,564`（替换）
- Test: `pkg/constants/embedding_test.go`（新建，维度 pin）
- Test: `internal/knowledge/application/workspace_service_extra_test.go:219`（`vectorDim` 表驱动测试改造为 `constants.DimensionForModel`）

**Interfaces:**

- Consumes: `embedding.EmbeddingService.model`（现有私有字段）
- Produces: `constants.DimensionForModel(name string) int`（包级，唯一实现）；`constants.SanitizeMilvusName(s string) string`

- [ ] **Step 1: 写失败测试（维度 pin）**

`pkg/constants/embedding_test.go`：

```go
package constants

import "testing"

func TestDimensionForModel(t *testing.T) {
 tests := []struct {
  model string
  want  int
 }{
  {"text-embedding-v1", 1536},
  {"text-embedding-v2", 1024}, // 修正点：历史 1536 → 1024，与 knowledge 旧 vectorDim 一致
  {"text-embedding-v3", 1024},
  {"text-embedding-v4", 1024},
  {"embedding-3", 2048},
  {"text-embedding-3-small", 1536}, // default
  {"unknown-model", 1536},          // default
 }
 for _, tc := range tests {
  t.Run(tc.model, func(t *testing.T) {
   if got := DimensionForModel(tc.model); got != tc.want {
    t.Errorf("DimensionForModel(%q) = %d, want %d", tc.model, got, tc.want)
   }
  })
 }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/constants/ -run TestDimensionForModel`
Expected: FAIL（`DimensionForModel` undefined）

- [ ] **Step 3: 实现 `pkg/constants/embedding.go`**

```go
package constants

// DimensionForModel 返回嵌入模型的向量维度——全系统单一事实源
// （跨包行为数字，CLAUDE.md 要求入 pkg/constants）。
// 修正记录：text-embedding-v2 由历史 1536 修正为 1024，与 knowledge 侧
// 旧 vectorDim 一致；存量 1536 维 collection 由 legacy 回退 dim 检查兜底。
func DimensionForModel(name string) int {
 switch name {
 case "text-embedding-v1":
  return 1536 // DashScope v1
 case "text-embedding-v2", "text-embedding-v3", "text-embedding-v4":
  return 1024 // DashScope v2/v3/v4 default
 case "embedding-3":
  return 2048 // Zhipu
 default:
  return 1536 // OpenAI text-embedding-3-small / ada-002
 }
}
```

`pkg/constants/knowledge.go`：把 `var milvusUnsafe` 包一层导出（`:60` 附近）：

```go
// SanitizeMilvusName 把任意字符串清洗为 Milvus 安全的 collection 名片段
// （仅字母数字下划线）。memory 与 knowledge 命名统一走此函数。
func SanitizeMilvusName(s string) string { return milvusUnsafe.ReplaceAllString(s, "_") }
```

`CollectionName` 内部改用 `SanitizeMilvusName`（本 task 不改签名，Task 6 才加 model 参数）。

- [ ] **Step 4: GetVectorDimension 委托**

`embedding/embedding.go:95-106`：

```go
func (e *EmbeddingService) GetVectorDimension() int {
 return constants.DimensionForModel(e.model)
}
```

- [ ] **Step 5: knowledge 三处替换 + 删除 vectorDim**

`workspace_service.go:37-47` 删除 `vectorDim`；:144：

```go
  if err := s.vectorStore.CreateCollectionWithDim(ctx, col, constants.DimensionForModel(ws.Config.EmbeddingModel)); err != nil {
```

`ingest_service.go:347`：

```go
 if err := ki.vectorStore.CreateCollectionWithDim(ctx, collectionName, constants.DimensionForModel(req.EmbeddingModel)); err != nil {
```

`rag_service.go:561,564`（`validateCollectionDim` 内）：

```go
 if info.Dim != 0 && info.Dim != constants.DimensionForModel(embedModel) {
  rs.logger.Error("knowledge.retrieval.schema_mismatch",
   zap.String("collection", collection), zap.Int("existing_dim", info.Dim),
   zap.Int("required_dim", constants.DimensionForModel(embedModel)))
```

`workspace_service_extra_test.go:219`：`vectorDim` 表驱动测试改为遍历 `constants.DimensionForModel` 断言同一 pin 表（v2→1024 等，与 `pkg/constants` 测试一致）。

- [ ] **Step 6: 跑测试**

Run: `go test -short ./pkg/constants/... ./internal/llmgateway/infrastructure/embedding/... ./internal/knowledge/...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add pkg/constants/embedding.go pkg/constants/embedding_test.go pkg/constants/knowledge.go internal/llmgateway/infrastructure/embedding/embedding.go internal/knowledge/application/workspace_service.go internal/knowledge/application/ingest_service.go internal/knowledge/application/rag_service.go internal/knowledge/application/workspace_service_extra_test.go
git commit -m "refactor(llmgateway): 维度单一事实源 constants.DimensionForModel（v2 修正 1024）
- GetVectorDimension 委托、knowledge vectorDim 删除、v2 由 1536 修正为 1024
- SanitizeMilvusName 导出供 memory/knowledge 命名复用"
```

---

### Task 3: 统一解析入口 —— `ModelRegistry.ResolveDefaultEmbeddingModel`

**Files:**

- Modify: `internal/llmgateway/infrastructure/model_registry.go`（新增方法）
- Modify: `api/wiring/knowledge.go:149-190`（`buildEmbedResolver`、`buildKnowledgeEmbedResolver`）、`:197-219`（`SeedBuiltinKnowledgeDocs`）
- Test: `internal/llmgateway/infrastructure/model_registry_test.go`（新增或追加，mock modelRepo/providerRepo）

**Interfaces:**

- Consumes: Task 1 的 `domain.Model.DefaultEmbedding`、现有 `List`/`providerRepo.Get`
- Produces: `(*ModelRegistry).ResolveDefaultEmbeddingModel(ctx, tenantID) (string, error)`——标记模型优先；无标记 → enabled 列表第一个（保留 `sort.Strings` 字典序语义）；无可用 → `""`

- [ ] **Step 1: 写失败测试（解析规则表驱动）**

`model_registry_test.go` 追加（复用现有 mock repo 模式；若无 mock，按文件现有风格建 `fakeModelRepo`/`fakeProviderRepo`）：

```go
func TestResolveDefaultEmbeddingModel(t *testing.T) {
 // fake 数据：enabled 模型列表 + 各模型 DefaultEmbedding/provider enabled 状态
 tests := []struct {
  name      string
  models    []domain.Model // List(Enabled, CapEmbedding) 返回
  providers map[string]*domain.Provider // providerID → provider
  want      string
 }{
  {"marked model wins over alphabetical first",
   []domain.Model{modelWith("a-embed", "p1", false), modelWith("b-embed", "p1", true)},
   map[string]*domain.Provider{"p1": enabledProvider()},
   "b-embed"},
  {"no marker falls back to first",
   []domain.Model{modelWith("a-embed", "p1", false), modelWith("b-embed", "p1", false)},
   map[string]*domain.Provider{"p1": enabledProvider()},
   "a-embed"},
  {"empty list returns empty",
   nil, map[string]*domain.Provider{}, ""},
  {"marked but provider disabled falls back to first",
   []domain.Model{modelWith("a-embed", "p1", false), modelWith("b-embed", "p2", true)},
   map[string]*domain.Provider{"p1": enabledProvider(), "p2": disabledProvider()},
   "a-embed"},
 }
 for _, tc := range tests {
  t.Run(tc.name, func(t *testing.T) {
   reg := newTestRegistry(tc.models, tc.providers) // 现有测试构造器或新写
   got, err := reg.ResolveDefaultEmbeddingModel(context.Background(), "tenant-a")
   if err != nil { t.Fatal(err) }
   if got != tc.want { t.Errorf("got %q, want %q", got, tc.want) }
  })
 }
}

func modelWith(name, providerID string, def bool) domain.Model {
 return domain.Model{ID: name, Name: name, ProviderID: providerID,
  Capabilities: []domain.ModelCapability{domain.CapEmbedding},
  Enabled: true, DefaultEmbedding: def}
}
```

（`enabledProvider`/`disabledProvider` 返回 `*domain.Provider` 带 `Enabled: true/false`，`Kind` 设为 embedProto 支持的 kind。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -short ./internal/llmgateway/infrastructure/ -run TestResolveDefaultEmbeddingModel -v`
Expected: FAIL（方法 undefined）

- [ ] **Step 3: 实现**

`model_registry.go`（`ListEmbeddingModelsByTenant` 之后）：

```go
// ResolveDefaultEmbeddingModel 解析 tenant 的默认嵌入模型名：
// 1. enabled 且 provider 可用且标记 default_embedding 的模型优先；
// 2. 无标记 → enabled 列表第一个（保留现状 sort.Strings 字典序语义）；
// 3. 列表为空 → 返回 ""，调用方 fail-closed（不默认放行）。
func (r *ModelRegistry) ResolveDefaultEmbeddingModel(ctx context.Context, tenantID string) (string, error) {
 enabled := true
 models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled, Capability: domain.CapEmbedding})
 if err != nil {
  return "", fmt.Errorf("model registry: list embedding models: %w", err)
 }
 names := make([]string, 0, len(models))
 var marked string
 for _, m := range models {
  provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
  if err != nil {
   return "", fmt.Errorf("model registry: get provider: %w", err)
  }
  if !provider.Enabled || !r.supports(provider.Kind, domain.CapEmbedding) {
   continue
  }
  if m.DefaultEmbedding {
   marked = m.Name
  }
  names = append(names, m.Name)
 }
 if marked != "" {
  return marked, nil
 }
 sort.Strings(names)
 if len(names) == 0 {
  return "", nil
 }
 return names[0], nil
}
```

- [ ] **Step 4: wiring 三处消费方改造**

`api/wiring/knowledge.go` `buildEmbedResolver`（:149-166 整体替换 body）：

```go
 return func(ctx context.Context, tenantID string) pipeline.EmbedClient {
  model, err := registry.ResolveDefaultEmbeddingModel(ctx, tenantID)
  if err != nil || model == "" {
   return nil
  }
  cfg, _, err := registry.ResolveEmbedding(ctx, tenantID, model)
  if err != nil {
   return nil
  }
  client := llmgateway.NewOpenAICompatClient(cfg, logger)
  return embedding.NewEmbeddingServiceWithModel(client, model, logger)
 }
```

`buildKnowledgeEmbedResolver`（:173-181）空模型分支：

```go
  m := model
  if m == "" {
   m, err = registry.ResolveDefaultEmbeddingModel(ctx, tenantID)
   if err != nil || m == "" {
    return nil
   }
  }
```

`SeedBuiltinKnowledgeDocs`（:215-218）：

```go
 for _, tid := range tenantIDs {
  model, err := c.LLMGateway.Registry.ResolveDefaultEmbeddingModel(ctx, tid)
  if err != nil || model == "" {
   c.Logger.Warn("knowledge.seed_builtin_docs.skip: no embedding model",
    zap.String("tenant_id", tid))
   continue
  }
  seeds.SeedBuiltinDocs(ctx, tid, model,
   c.Knowledge.Ingest, c.Knowledge.DocRepo, officialDocsCatalogAdapter{}, c.Logger)
 }
```

注意 `SeedBuiltinKnowledgeDocs` 开头 guard 补 `c.LLMGateway == nil || c.LLMGateway.Registry == nil` 时直接 return（与现有 guard 并列）。

- [ ] **Step 5: 跑测试**

Run: `go test -short ./internal/llmgateway/... ./api/wiring/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/llmgateway/infrastructure/model_registry.go internal/llmgateway/infrastructure/model_registry_test.go api/wiring/knowledge.go
git commit -m "feat(llmgateway): ResolveDefaultEmbeddingModel 统一 3 处嵌入模型选择（标记优先→第一个→空 fail-closed）"
```

---

### Task 4: API —— `PUT /admin/models/:id/default-embedding`

**Files:**

- Modify: `internal/llmgateway/application/model_mgmt_service.go`（`SetDefaultEmbedding`）
- Modify: `api/http/handler/model_mgmt_handler.go`（`SetDefaultEmbedding` handler，仿 Toggle :83-101）
- Modify: `api/http/router.go:564-575`（`/admin/models` 组注册）
- Test: `internal/llmgateway/application/model_mgmt_service_test.go`（追加）

**Interfaces:**

- Consumes: Task 1 的 `port.ModelRepository.SetDefaultEmbedding`、现有 `ModelCacheInvalidator`
- Produces: `ModelMgmtService.SetDefaultEmbedding(ctx, tenantID, id string, enabled bool) error`（校验 + invalidate）；handler `SetDefaultEmbedding(c *gin.Context)`；路由 `PUT /admin/models/:id/default-embedding`（adminMW + requireActive）

- [ ] **Step 1: 写失败测试**

`model_mgmt_service_test.go` 追加：

```go
func TestModelMgmtServiceSetDefaultEmbedding(t *testing.T) {
 t.Run("rejects non-embedding model when enabling", func(t *testing.T) {
  repo := &modelMgmtRepo{models: []domain.Model{
   {ID: "m1", Name: "chat-x", Capabilities: []domain.ModelCapability{domain.CapChat}, Enabled: true},
  }}
  svc := NewModelMgmtService(repo, func(tenantID string) { t.Fatal("must not invalidate on rejected set") })
  if err := svc.SetDefaultEmbedding(context.Background(), "t1", "m1", true); err == nil {
   t.Fatal("expected error for non-embedding model")
  }
 })
 t.Run("rejects disabled model when enabling", func(t *testing.T) {
  repo := &modelMgmtRepo{models: []domain.Model{
   {ID: "m1", Name: "embed-x", Capabilities: []domain.ModelCapability{domain.CapEmbedding}, Enabled: false},
  }}
  svc := NewModelMgmtService(repo)
  if err := svc.SetDefaultEmbedding(context.Background(), "t1", "m1", true); err == nil {
   t.Fatal("expected error for disabled model")
  }
 })
 t.Run("invalidates registry after successful set", func(t *testing.T) {
  repo := &modelMgmtRepo{models: []domain.Model{
   {ID: "m1", Name: "embed-x", Capabilities: []domain.ModelCapability{domain.CapEmbedding}, Enabled: true},
  }}
  invalidated := false
  svc := NewModelMgmtService(repo, func(tenantID string) { invalidated = true })
  if err := svc.SetDefaultEmbedding(context.Background(), "t1", "m1", true); err != nil { t.Fatal(err) }
  if !invalidated { t.Fatal("expected registry invalidation") }
 })
}
```

（`modelMgmtRepo` 需补 `SetDefaultEmbedding` + `Get` 返回 `models` 列表——按现有 mock 结构扩展。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -short ./internal/llmgateway/application/ -run TestModelMgmtServiceSetDefaultEmbedding -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现 service**

`model_mgmt_service.go`（`Toggle` 之后）：

```go
// SetDefaultEmbedding 设置或取消模型的默认嵌入标记。启用时校验目标
// capability 含 embedding 且 enabled（fail-closed）；repo 单事务
// clear-then-set 保证并发安全。成功后失效 registry 缓存。
func (s *ModelMgmtService) SetDefaultEmbedding(ctx context.Context, tenantID, id string, enabled bool) error {
 if enabled {
  m, err := s.repo.Get(ctx, tenantID, id)
  if err != nil {
   return err
  }
  if !m.Enabled || !hasCapability(m.Capabilities, domain.CapEmbedding) {
   return fmt.Errorf("model %s is not an enabled embedding model", id)
  }
 }
 if err := s.repo.SetDefaultEmbedding(ctx, tenantID, id, enabled); err != nil {
  return err
 }
 s.invalidate(tenantID)
 return nil
}

func hasCapability(caps []domain.ModelCapability, want domain.ModelCapability) bool {
 for _, c := range caps {
  if c == want {
   return true
  }
 }
 return false
}
```

- [ ] **Step 4: handler + 路由**

`model_mgmt_handler.go`（Toggle 之后，模式一致）：

```go
// SetDefaultEmbedding PUT /admin/models/:id/default-embedding — 设置/取消默认嵌入模型。
func (h *ModelMgmtHandler) SetDefaultEmbedding(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 var req struct {
  Enabled bool `json:"enabled"`
 }
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 if err := h.svc.SetDefaultEmbedding(c.Request.Context(), tenantID, c.Param("id"), req.Enabled); err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"message": "已更新"})
}
```

`router.go:564-575` `models` 组内追加（adminMW 已定义）：

```go
  models.PUT("/:id/default-embedding", adminMW, requireActive, modelMgmtH.SetDefaultEmbedding)
```

- [ ] **Step 5: 跑测试**

Run: `go test -short ./internal/llmgateway/... ./api/http/...`
Expected: PASS（handler 层由现有 router/contract 基建覆盖编译与绑定）

- [ ] **Step 6: Commit**

```bash
git add internal/llmgateway/application/model_mgmt_service.go internal/llmgateway/application/model_mgmt_service_test.go api/http/handler/model_mgmt_handler.go api/http/router.go
git commit -m "feat(llmgateway): PUT /admin/models/:id/default-embedding（admin 组，校验 embedding+enabled）"
```

---

### Task 5: memory collection 命名改造 + legacy 回退

**Files:**

- Modify: `internal/llmgateway/infrastructure/embedding/embedding.go`（加 `Model() string`）
- Modify: `internal/memory/domain/port/embed_client.go`（`EmbedClient` 加 `Model() string`）
- Modify: `internal/knowledge/domain/port/embedder.go`（`Embedder` 加 `Model() string`）
- Modify: `internal/memory/infrastructure/pipeline/vector_adapter.go`（命名 + ensure + Upsert 签名）
- Modify: `internal/memory/infrastructure/pipeline/embedder.go:17-19`（`VectorStore` 接口加 model 参数）
- Modify: `internal/memory/application/extraction.go:202`（facts 写入命名）
- Modify: `internal/memory/application/retrieval.go:39`（RecallMemory 命名 + 双名查询）
- Modify: `internal/memory/application/memory_service_v2.go:307`（ForgetMemory 命名）
- Modify: `internal/memory/infrastructure/pipeline/recall_tool.go:176`（候选集合 [新名×2, legacy×2]）
- Modify: `api/wiring/memory.go:214-223`（DimResolver 按默认模型查表）
- Test: `internal/memory/infrastructure/pipeline/vector_adapter_test.go`（新建或追加）

**Interfaces:**

- Consumes: Task 2 的 `constants.DimensionForModel`、`constants.SanitizeMilvusName`；Task 3 的 `ResolveDefaultEmbeddingModel`
- Produces: `func memoryCollectionName(tenantID, model string) string`、`func memoryFactsCollectionName(tenantID, model string) string`、`func memoryCollectionLegacyName(tenantID string) string`（旧名）；`VectorStore.Upsert(ctx, tenantID, userID, id, model string, vector []float32, metadata map[string]any) error`；`(*EmbeddingService).Model() string`；`port.EmbedClient.Model() string`、`port.Embedder.Model() string`；`(*MemoryService).currentEmbedModel(ctx, tenantID) string`（helper）
- 接口链（必须同时扩展，编译期强制）：`pipeline.EmbedClient`（定义于 `pipeline.go:22-25`，方法 `EmbedVector`/`GetVectorDimension`）加 `Model() string`；`EmbedClientAdapter`（`embed_adapter.go:6`，`struct{ ec EmbedClient }`，把 pipeline.EmbedClient 适配成 memport.EmbedClient）加 `Model()` 转发 `a.ec.Model()`；`memport.EmbedClient` 与 `knowledgeport.Embedder` 各加 `Model() string`；测试 fake（`recall_vector_test.go:13` 的 `fakeEmbedClient`）补 `Model() string`。实现者 `*embedding.EmbeddingService` 带 `Model()` 后链上所有接口天然满足

- [ ] **Step 1: 写失败测试**

`vector_adapter_test.go`：

```go
func TestMemoryCollectionNames(t *testing.T) {
 tests := []struct {
  tenantID, model, want string
 }{
  {"11111111-1111-1111-1111-111111111111", "text-embedding-v3",
   "memory_11111111_1111_1111_1111_111111111111_text_embedding_v3"},
  {"t1", "embedding-3", "memory_t1_embedding_3"},
 }
 for _, tc := range tests {
  if got := memoryCollectionName(tc.tenantID, tc.model); got != tc.want {
   t.Errorf("memoryCollectionName(%q,%q) = %q, want %q", tc.tenantID, tc.model, got, tc.want)
  }
 }
 if got := memoryCollectionLegacyName("t1"); got != "memory_t1" {
  t.Errorf("legacy name = %q, want memory_t1", got)
 }
}

func TestEmbeddingServiceExposesModel(t *testing.T) {
 svc := NewEmbeddingServiceWithModel(nil, "text-embedding-v3", nil)
 if got := svc.Model(); got != "text-embedding-v3" {
  t.Errorf("Model() = %q", got)
 }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test -short ./internal/memory/infrastructure/pipeline/ -run 'TestMemoryCollectionNames|TestEmbeddingService' -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现命名 + Model() getter**

`vector_adapter.go`（保留 legacy 函数名语义，改名 + 加参）：

```go
// memoryCollectionName builds the Milvus collection name for a tenant's
// raw-turn vectors, encoding the embedding model suffix so switching models
// isolates data into a fresh collection.
func memoryCollectionName(tenantID, model string) string {
 return "memory_" + strings.ReplaceAll(tenantID, "-", "_") + "_" + constants.SanitizeMilvusName(model)
}

// memoryFactsCollectionName builds the collection name for LLM-extracted facts.
func memoryFactsCollectionName(tenantID, model string) string {
 return "memory_facts_" + strings.ReplaceAll(tenantID, "-", "_") + "_" + constants.SanitizeMilvusName(model)
}

// memoryCollectionLegacyName / memoryFactsCollectionLegacyName 是无模型后缀的
// 存量 collection 名（升级前数据），仅查询回退使用；写入永远走新名。
func memoryCollectionLegacyName(tenantID string) string {
 return "memory_" + strings.ReplaceAll(tenantID, "-", "_")
}
func memoryFactsCollectionLegacyName(tenantID string) string {
 return "memory_facts_" + strings.ReplaceAll(tenantID, "-", "_")
}
```

（import 加 `pkg/constants`。）

`embedding/embedding.go` 加 getter：

```go
// Model returns the embedding model name this service was built with.
func (e *EmbeddingService) Model() string { return e.model }
```

- [ ] **Step 4: 接口扩展 + 同步所有实现**

`memport/embed_client.go`：

```go
type EmbedClient interface {
 Embed(ctx context.Context, text string) ([]float32, error)
 EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
 // Model returns the embedding model name in use (collection 命名依赖).
 Model() string
}
```

`knowledge/domain/port/embedder.go` 同样加 `Model() string`。

同步实现（编译期强制，逐个补 `Model()` 返回其模型名）：

- `embedding.EmbeddingService`（已加）
- wiring 中任何 `pipeline.EmbedClient` / `memport.EmbedClient` 的适配器（`grep -rn "EmbedClient" api/wiring/ internal/memory/ --include="*.go"` 找全部实现/闭包；`EmbedClient` 闭包类型 resolver 返回的具名类型若为匿名结构则补字段）
- 测试 mock：`grep -rln "EmbedClient\|Embedder" internal/ --include="*_test.go"` 逐个补 `Model() string`（返回测试模型的固定名）

- [ ] **Step 5: pipeline VectorStore 接口 + adapter**

`embedder.go:17-19`：

```go
type VectorStore interface {
 Upsert(ctx context.Context, tenantID string, userID string, id string, model string, vector []float32, metadata map[string]any) error
}
```

`vector_adapter.go` `ensureCollection` 加 model 参数（ensured key 改为 `tenantID+"/"+model`）：

```go
func (a *MilvusVectorAdapter) ensureCollection(ctx context.Context, tenantID, model string) error {
 key := tenantID + "/" + model
 if _, ok := a.ensured.Load(key); ok {
  return nil
 }
 collectionName := memoryCollectionName(tenantID, model)
 dim := a.resolveDim(ctx, tenantID)
 if err := a.vs.CreateCollectionWithDim(ctx, collectionName, dim); err != nil {
  if !strings.Contains(err.Error(), "already exists") {
   return err
  }
 }
 a.ensured.Store(key, struct{}{})
 return nil
}

func (a *MilvusVectorAdapter) Upsert(ctx context.Context, tenantID string, userID string, id string, model string, vec []float32, metadata map[string]any) error {
 if err := a.ensureCollection(ctx, tenantID, model); err != nil {
  return err
 }
 collectionName := memoryCollectionName(tenantID, model)
 // ... 其余保持（doc 构造 + Insert）
}
```

（DimResolver 仍按 tenant 解析——同 tenant 同默认模型维度一致；若 ensure 与写入模型不同导致 dim 不一致，Milvus create 会因 collection 已存在而跳过，Insert 时 dim mismatch 报错——属手工误操作路径，spec §5 校验闭环。）

`embedder.go` processMessage 调用（:165 附近，Upsert 处）传 model：

```go
 if err := w.vectorDB.Upsert(ctx, ev.TenantID, ev.UserID, ev.MessageID, embedSvc.Model(), vector, eventMeta); err != nil {
```

（`eventMeta` 为现有 metadata 变量名，按文件现状调整。）

- [ ] **Step 6: facts 三处命名改造 + currentEmbedModel helper**

`memory_service_v2.go` 加 helper：

```go
// currentEmbedModel 返回当前默认嵌入模型名；无可用模型时返回 ""（legacy 名兜底）。
func (s *MemoryService) currentEmbedModel(ctx context.Context, tenantID string) string {
 if s.embedClient != nil {
  return s.embedClient.Model()
 }
 if s.embedClientResolver != nil {
  if ec := s.embedClientResolver(ctx, tenantID); ec != nil {
   return ec.Model()
  }
 }
 return ""
}

func factsCollectionName(tenantID, model string) string {
 if model == "" {
  return fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(tenantID, "-", "_"))
 }
 return fmt.Sprintf("memory_facts_%s_%s", strings.ReplaceAll(tenantID, "-", "_"),
  strings.ReplaceAll(model, "-", "_"))
}
```

（`factsCollectionName` 放 memory_service_v2.go 或新 `internal/memory/application/collection_names.go`——后者更清晰，与 pipeline 命名函数对称。）

`extraction.go:202`（embedder 已解析处，`embedder` 变量是 `port.EmbedClient`）：

```go
  collectionName := factsCollectionName(req.TenantID, embedder.Model())
```

`retrieval.go:39`（RecallMemory，双名查询：新名 + legacy 兜底）：

```go
 // Step 2: Vector search (retrieve 2*topK candidates) — 新名 collection 优先，
 // 不存在（升级后未重建）时回退 legacy 名；无可用模型时直接 legacy 名。
 collections := []string{
  factsCollectionName(req.TenantID, s.currentEmbedModel(ctx, req.TenantID)),
  fmt.Sprintf("memory_facts_%s", strings.ReplaceAll(req.TenantID, "-", "_")),
 }
 var vectorDocs []*port.VectorDoc
 for _, collectionName := range collections {
  if collectionName == "" {
   continue
  }
  docs, err := s.vectorStore.Search(ctx, collectionName, queryVector, req.TopK*2, filter)
  if err != nil {
   var unavailable *port.VectorStoreUnavailableError
   if errors.As(err, &unavailable) {
    vectorDocs = nil
   }
   continue // collection 不存在 → 尝试下一个（legacy 回退）
  }
  vectorDocs = append(vectorDocs, docs...)
 }
```

（注意去重：新名与 legacy 名相同时只查一次——`factsCollectionName` 在 model=="" 时返回 legacy 名，故 collections 可能重复。用 map 去重或仅当 `currentEmbedModel != ""` 才追加第二项。计划采用后者，在实现步骤写死：第二项仅当 model 非空时加入。）

`memory_service_v2.go:307` ForgetMemory：

```go
 if s.vectorStore != nil {
  collectionName := factsCollectionName(req.TenantID, s.currentEmbedModel(ctx, req.TenantID))
  if err := s.vectorStore.Delete(ctx, collectionName, []string{req.FactID}); err != nil {
   return fmt.Errorf("forget memory vector replica: %w", err)
  }
 }
```

（ForgetMemory 删除当前默认模型新名；model 为空 → legacy 名——存量数据可删。换过模型的旧 collection 数据不在删除范围，spec §11 范围外。）

- [ ] **Step 7: recall_tool 双 collection → [新×2, legacy×2]**

`recall_tool.go:171-184`：

```go
 // 候选集合 = 当前模型的新名 collection（raw + facts）∪ legacy 名（升级前
 // 数据）。SearchWithFilter 对不存在的 collection 报错被跳过——天然实现
 // legacy 回退与 dim mismatch 跳过（不 fail-closed）。
 embedModel := ""
 if embedSvc != nil && embedSvc.Model() != "" {
  embedModel = embedSvc.Model()
 }
 collections := []string{
  memoryCollectionName(tenantID, embedModel), memoryCollectionLegacyName(tenantID),
  memoryFactsCollectionName(tenantID, embedModel), memoryFactsCollectionLegacyName(tenantID),
 }
 if embedModel == "" {
  collections = []string{memoryCollectionLegacyName(tenantID), memoryFactsCollectionLegacyName(tenantID)}
 }
 var merged []vector.SearchResult
 for _, collection := range collections {
  results, err := h.vectorDB.SearchWithFilter(ctx, collection, vec, req.Limit*2, expr)
  if err != nil {
   h.logger.Debug("memory.recall: vector search failed for collection, skipping",
    zap.String("collection", collection), zap.Error(err))
   continue
  }
  merged = append(merged, results...)
 }
```

（`embedSvc.Model()` 需要 `pipeline.EmbedClient` 接口加 `Model() string`——recall_tool 的 embedSvc 类型。见 Step 4。）

- [ ] **Step 8: DimResolver 按默认模型查表**

`api/wiring/memory.go:214-223`：

```go
 dimResolver := pipeline.DimResolver(func(ctx context.Context, tenantID string) int {
  if c.LLMGateway != nil && c.LLMGateway.Registry != nil {
   if model, err := c.LLMGateway.Registry.ResolveDefaultEmbeddingModel(ctx, tenantID); err == nil && model != "" {
    return constants.DimensionForModel(model)
   }
  }
  return 1536
 })
```

（移除对 `c.Knowledge.EmbedResolver`/`GetVectorDimension` 的依赖；import `pkg/constants`。）

- [ ] **Step 9: 跑测试**

Run: `go test -short ./internal/memory/... ./internal/llmgateway/... ./api/wiring/... ./internal/knowledge/...`
Expected: PASS（命名 pin、legacy 名、Model() 暴露、recall 候选集合断言——若已有 recall_vector_test.go 的 fakeVectorSearcher，补断言 4 个候选 collection 的查询序列）

- [ ] **Step 10: Commit**

```bash
git add internal/llmgateway/infrastructure/embedding/embedding.go internal/memory/domain/port/embed_client.go internal/knowledge/domain/port/embedder.go internal/memory/infrastructure/pipeline/vector_adapter.go internal/memory/infrastructure/pipeline/embedder.go internal/memory/infrastructure/pipeline/recall_tool.go internal/memory/application/extraction.go internal/memory/application/retrieval.go internal/memory/application/memory_service_v2.go internal/memory/application/collection_names.go api/wiring/memory.go
git commit -m "feat(memory): collection 命名编码模型后缀 + legacy 回退（写入新名，查询新名→旧名兜底）
- memory/memory_facts collection 按 <tenant>_<san(model)> 隔离，Model() 暴露模型名
- recall/RecallMemory/ForgetMemory 双名兜底，DimResolver 按默认模型查表"
```

---

### Task 6: knowledge collection 命名改造 + legacy 回退

**Files:**

- Modify: `pkg/constants/knowledge.go`（`CollectionName` 加 embedModel 参数）
- Modify: `internal/knowledge/application/workspace_service.go:143,507`
- Modify: `internal/knowledge/application/ingest_service.go:346,431,483`
- Modify: `internal/knowledge/application/rag_service.go:269,589,593,694`（+ legacy 回退逻辑）
- Modify: `internal/knowledge/infrastructure/persistence/tenant_vector_cleaner.go:104`（旧名 + 新名都删）
- Test: `pkg/constants/knowledge_test.go`（新建或追加，命名 pin）
- Test: rag_service/ingest_service 既有测试同步（collection 名断言）

**Interfaces:**

- Consumes: Task 2 的 `constants.DimensionForModel`/`SanitizeMilvusName`
- Produces: `constants.CollectionName(_, workspaceID, embedModel string) string`（新签名）；`func resolveCollectionName(ctx, tenantID, workspaceID, embedModel string) (string, error)`（rag/ingest 共用 helper：新名 DescribeCollection 不存在 → legacy 名，且对 legacy 名做 dim 检查）

- [ ] **Step 1: 写失败测试**

`pkg/constants/knowledge_test.go`（新建）：

```go
package constants

import "testing"

func TestCollectionNameEncodesModel(t *testing.T) {
 wsID := "9a1c3b2d-0000-4000-8000-000000000001"
 if got := CollectionName("", wsID, "text-embedding-v3"); got != "kb_"+wsID+"_text_embedding_v3" {
  t.Errorf("CollectionName = %q", got)
 }
 if got := SanitizeMilvusName("a-b c.d/e"); got != "a_b_c_d_e" {
  t.Errorf("SanitizeMilvusName = %q", got)
 }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./pkg/constants/ -run TestCollectionNameEncodesModel`
Expected: FAIL（参数个数不匹配）

- [ ] **Step 3: 改签名**

`pkg/constants/knowledge.go:66-71`：

```go
// CollectionName generates the Milvus collection name for a knowledge workspace.
// workspaceID (UUID v7) is globally unique, so tenantID is ignored; embedModel
// is encoded as a sanitized suffix so switching models isolates vector data.
func CollectionName(_, workspaceID, embedModel string) string {
 return fmt.Sprintf("%s_%s_%s", CollectionPrefix, SanitizeMilvusName(workspaceID), SanitizeMilvusName(embedModel))
}

// CollectionLegacyName 是无模型后缀的存量 collection 名（升级前数据）。
func CollectionLegacyName(_, workspaceID string) string {
 return fmt.Sprintf("%s_%s", CollectionPrefix, SanitizeMilvusName(workspaceID))
}
```

- [ ] **Step 4: 同步全部调用点（编译期驱动）**

逐个替换（`grep -rn "constants.CollectionName" internal/ --include="*.go"` 编译报错驱动）：

- `workspace_service.go:143` → `constants.CollectionName(tenantID, ws.ID, ws.Config.EmbeddingModel)`
- `workspace_service.go:507`（DeleteDocument 向量）→ 同上
- `ingest_service.go:346`（IngestDocument）→ `constants.CollectionName(req.TenantID, req.WorkspaceID, req.EmbeddingModel)`
- `ingest_service.go:431`（DeleteWorkspaceData）→ 无 model 上下文——**最终代码**（替换 :431-445 整体；`isCollectionNotFound` 是本包 rag_service.go:33 的 helper，可复用）：

```go
// DeleteWorkspaceData 删除 workspace 的向量数据。删除路径无模型上下文
//（spec §11：删除策略不在本设计内）：删 legacy 名 + 当前注入 embedder
// 的模型新名（embeddingSvc 为 nil 时只删 legacy 名）。换过多次模型的
// 旧 collection 不在删除范围，属可接受残差，由 tenant_vector_cleaner
// 全量清理兜底。
func (ki *KnowledgeIngest) DeleteWorkspaceData(ctx context.Context, tenantID, workspaceID string) error {
 model := ""
 if ki.embeddingSvc != nil {
  model = ki.embeddingSvc.Model()
 }
 cols := []string{constants.CollectionLegacyName(tenantID, workspaceID)}
 if model != "" {
  cols = append(cols, constants.CollectionName(tenantID, workspaceID, model))
 }
 for _, col := range cols {
  if err := ki.vectorStore.DeleteCollection(ctx, col); err != nil && !isCollectionNotFound(err) {
   return fmt.Errorf("failed to delete workspace collection %s: %w", col, err)
  }
 }
 if ki.chunkRepo != nil {
  if err := ki.chunkRepo.DeleteByWorkspace(ctx, tenantID, workspaceID); err != nil {
   ki.logger.Warn("knowledge.workspace.chunk_pg_delete_failed", zap.Error(err))
  }
 }
 ki.logger.Info("knowledge.workspace.collection_deleted",
  zap.String("tenant_id", tenantID),
  zap.String("workspace_id", workspaceID),
  zap.Strings("collections", cols))
 return nil
}
```

- `ingest_service.go:483`（GetWorkspaceStats）→ 签名加 embedModel 参数：`GetWorkspaceStats(ctx, tenantID, workspaceID, embedModel string)`；调用方 `workspace_service.go:300` 传 `ws.Config.EmbeddingModel`
- `rag_service.go:269,589,593,694` → 传 `req.EmbeddingModel`（589/593 是 handleMissingCollection 内日志——同样传 req.EmbeddingModel）
- `rag_service.go:694`（RetrieveRelevantChunks，embedModel="" 的现状调用）→ 签名加 embedModel 或传 workspace 的模型；`queryVector(..., "")` 现状——**decision**：RetrieveRelevantChunks 增加 `embedModel string` 参数（调用方补传），否则 collection 名无法构造。
- `tenant_vector_cleaner.go:104`（dropWorkspaceCollections）→ 查询带 embedding_model，构造 [新名, legacy 名] 都删；**容忍 not-found**（`isCollectionNotFound` 在 application 包不可用——cleaner 是 infrastructure/persistence 包，且 `c.vs` 是 `collectionDropper` 接口、直接调 `*milvus.VectorStore`，必须用 `pkg/storage/milvus` 导出的 `ErrCollectionNotFound` 哨兵，见 `internal/knowledge/infrastructure/vectorstore/adapter.go:86` 的既有用法；pkg/storage/milvus 包的 import 别名本文件已有）：

```go
func (c *TenantVectorCleaner) dropWorkspaceCollections(ctx context.Context, tenantID string, errs *[]string) {
 if c.pool == nil {
  return
 }
 var cols []string
 queryErr := execTenant(ctx, c.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  rows, err := tx.Query(ctx, `SELECT id, COALESCE(config->>'embedding_model','') FROM rag_workspaces`)
  if err != nil {
   return err
  }
  defer rows.Close()
  for rows.Next() {
   var id, model string
   if err := rows.Scan(&id, &model); err != nil {
    continue
   }
   // 新名 + legacy 名都删：model 为空（升级前无 config）的 workspace
   // 只有 legacy collection。
   cols = append(cols, constants.CollectionLegacyName(tenantID, id))
   if model != "" {
    cols = append(cols, constants.CollectionName(tenantID, id, model))
   }
  }
  return rows.Err()
 })
 if queryErr != nil {
  c.logger.Warn("failed to query rag_workspaces for vector cleanup, workspace collections may leak",
   zap.String("tenant_id", tenantID), zap.Error(queryErr))
  return
 }
 for _, col := range cols {
  if err := c.vs.DeleteCollection(ctx, col); err != nil && !errors.Is(err, milvus.ErrCollectionNotFound) {
   *errs = append(*errs, fmt.Sprintf("%s: %v", col, err))
  }
 }
}
```

- [ ] **Step 5: rag legacy 回退（先于 drift 分类）**

`rag_service.go` queryVector 调用处（:269 区域，`mode=vector` 分支）改造为：

```go
 collectionName, legacyName := constants.CollectionName(req.TenantID, req.WorkspaceID, req.EmbeddingModel),
  constants.CollectionLegacyName(req.TenantID, req.WorkspaceID)

 // legacy 回退先于 drift 分类：升级后未 re-ingest 的 workspace 新名缺失是
 // 预期状态，先试 legacy 名（旧数据仍在），两者都缺失才走 handleMissingCollection。
 searchName := collectionName
 if _, err := rs.vectorStore.DescribeCollection(ctx, collectionName); isCollectionNotFound(err) {
  searchName = legacyName
  rs.logger.Info("knowledge.retrieval.legacy_collection_fallback",
   zap.String("collection", legacyName), zap.String("workspace_id", req.WorkspaceID))
 }
 vectorResults, err := rs.queryVector(ctx, req.Question, searchName, candidateTopK, rs.resolveEmbedder(ctx, req), req.EmbeddingModel)
```

  注意：`validateCollectionDim`（:561）对 searchName 校验——legacy 名 1536 维 vs 当前模型 dim 1024 → ErrRAGDependency？spec 说 legacy dim 不一致 → Warn + 跳过，**不 fail-closed**。但 RAG 是同步查询，跳过 = 空结果。现状 queryVector 内部 validateCollectionDim 返回 err → ErrRAGDependency。改造 queryVector 或调用处：validateCollectionDim 返回 dim mismatch 时，若 searchName 是 legacy 名 → 跳过该 collection 返回空（空结果 + Warn），若新名 mismatch → ErrRAGDependency（现状行为）。
  计划实现：在 `validateCollectionDim` 的 dim mismatch 分支（:557-562）改为：

```go
 if info.Dim != 0 && info.Dim != constants.DimensionForModel(embedModel) {
  rs.logger.Warn("knowledge.retrieval.legacy_dim_mismatch",
   zap.String("collection", collection), zap.Int("existing_dim", info.Dim),
   zap.Int("required_dim", constants.DimensionForModel(embedModel)))
  return errLegacyDimMismatch // 新定义哨兵：调用方对 legacy 名跳过、新名转 ErrRAGDependency
 }
```

  调用方（queryVector 内）：`errors.Is(err, errLegacyDimMismatch)` → 若当前是 legacy 名 → 返回空结果（不 fail）；若是新名 → 转 ErrRAGDependency。errCollectionNotFound 同理（新名缺失时已在调用处换 legacy 名；若 legacy 名也缺失 → handleMissingCollection 现有逻辑）。

  `handleMissingCollection`（:577-595）日志与计数改用传入 collection 名（legacy 名上下文），逻辑不变（chunks>0 仍判 drift）。

- [ ] **Step 6: 跑测试**

Run: `go test -short ./pkg/constants/... ./internal/knowledge/... ./api/wiring/...`
Expected: PASS（既有 rag/ingest 测试同步 collection 名断言；新增 G7 状态机测试：升级态回退 legacy、换模型态只用新名、dim mismatch 不 fail、未 re-ingest 不误判 drift——用 fake vectorStore 断言 DescribeCollection/Search 调用序列）

- [ ] **Step 7: Commit**

```bash
git add pkg/constants/knowledge.go pkg/constants/knowledge_test.go internal/knowledge/application/workspace_service.go internal/knowledge/application/ingest_service.go internal/knowledge/application/rag_service.go internal/knowledge/infrastructure/persistence/tenant_vector_cleaner.go
git commit -m "feat(knowledge): kb_<workspace>_<model> 命名 + legacy 回退先于 drift 分类
- CollectionName 加模型后缀，删除/清理路径新名+legacy 名双删
- legacy dim mismatch Warn 跳过不 fail-closed，新名 mismatch 仍 fail-closed"
```

---

### Task 7: 异常感知 —— 指标 + DLQ payload + 定向重放 API

**Files:**

- Modify: `internal/memory/infrastructure/pipeline/metrics.go`（`memory_embed_unavailable_total`）
- Modify: `internal/memory/infrastructure/pipeline/embedder.go:152-163`（nil 分支计数）
- Modify: `internal/memory/infrastructure/pipeline/dead_letter.go:20-31,69-81`（`DeadLetterEvent.Payload` + 存原始 body）
- Create: `internal/memory/infrastructure/pipeline/replay.go`（重放服务）
- Modify: `api/http/router.go`（`POST /admin/memory/dlq/replay`）
- Modify: `api/http/handler`（`memory_dlq_replay` handler）
- Modify: `api/wiring`（重放服务装配 + 关闭）
- Modify: `grafana/`（告警规则文件，按现有目录结构）
- Test: `internal/memory/infrastructure/pipeline/replay_test.go`（embedded NATS）

**Interfaces:**

- Consumes: `jetstream`（现有 `dlqPublisher`、`constants.MemoryDLQSubject`）；现有 global admin 鉴权（`/admin` 组，router.go:243）
- Produces: `DeadLetterEvent.Payload []byte`（json tag `payload,omitempty`）；`ReplayService{js, logger}.ReplayByErrorCode(ctx, errorCode string) (ReplayResult, error)`（按 error_code 过滤 → payload 非空校验 → publish 回 `memory.raw.<tenant>` → 先发布后置 `replayed`）；handler `POST /admin/memory/dlq/replay`（body `{errorCode}`，tenantID 不来自 body）

- [ ] **Step 1: 指标 + 计数（写实现与测试）**

`metrics.go` 加：

```go
 embedUnavailableTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
  Name: "memory_embed_unavailable_total",
  Help: "Total messages dead-lettered because no embedding model is configured for the tenant.",
 }, []string{"tenant_id"})
```

RegisterMetrics 列表追加。

`embedder.go:152-163` nil 分支（Warn 之后、deadLetter 之前）：

```go
  embedUnavailableTotal.WithLabelValues(ev.TenantID).Inc()
```

测试：`pipeline_test.go` 或新建 metrics 断言——现有 `pipeline_test.go`（embedded NATS）加场景：embedResolver 返回 nil → 断言死信 error_code 与 counter 值（`testutil.ToFloat64(embedUnavailableTotal.WithLabelValues(tenant))`，prometheus 测试 import `github.com/prometheus/client_golang/prometheus/testutil`）。

- [ ] **Step 2: DLQ 事件存原始 payload**

`dead_letter.go:20-31` `DeadLetterEvent` 加字段：

```go
 // Payload 是原始消息 body（TermWithReason 销毁原消息前读出），
 // 供定向重放重建消息。
 Payload []byte `json:"payload,omitempty"`
```

`deadLetterWithHeartbeat`（:69-81，event 构造处）加：

```go
 event.Payload = append([]byte(nil), msg.Data()...)
```

- [ ] **Step 3: 写失败测试（重放）**

`replay_test.go`（embedded NATS，仿 `pipeline_test.go:20 natsserver.RunServer` 基建）：

```go
func TestReplayService(t *testing.T) {
 // 搭建：embedded NATS + DLQ stream（MemoryDLQSubject）放入 3 条死信事件：
 // ① embed_service_unavailable + payload ② embed_service_unavailable + 空 payload
 // ③ embedding_failed + payload（error_code 过滤的对照组）
 t.Run("replays only matching error_code with payload", func(t *testing.T) { /* 断言 raw subject 收到 1 条；②③ 未发 */ })
 t.Run("idempotent: second replay skips replayed events", func(t *testing.T) { /* 重复调用 → 不产生新消息 */ })
 t.Run("tenant derived from event not body", func(t *testing.T) { /* A 租户事件发到 memory.raw.<A>，B 租户 subject 无消息 */ })
 t.Run("replay count cap rejects beyond max", func(t *testing.T) { /* replay_count 超 MaxDLQReplay → 拒绝 */ })
}
```

（replayed/replay_count 标记存哪？**决策**：JetStream 死信消息是事件源，标记必须持久。方案：重放成功后在 DLQ 事件上重新 publish 覆盖（同 dedupID `deadLetterDedupID`）——事件不可变的话用重发布+同 MsgID 覆盖。**更简单**：DLQ stream 的每条消息 header 置 `Replayed: "true"`（publish 带 `jetstream.WithHeaders`），重放计数用 header `ReplayCount: "n"`——从 stream 重新拉取原事件、验证 header、publish 回 raw、更新 header 再回写。**最终决策（写死在实现）**：重放采用"读事件 → 校验 → publish 回 raw（带 `WithMsgID("replay:"+StreamSequence)` 幂等去重）→ 更新事件 header（Replayed=true、ReplayCount+1）后重发布回 DLQ subject（同 dedupID 覆盖）"。JetStream `WithMsgID` 对同 subject 幂等——replay 重发同 MsgID 的消息会被去重，天然幂等。测试断言：raw subject 收到消息条数不随重复调用增长。）

- [ ] **Step 4: 跑测试确认失败**

Run: `go test -short ./internal/memory/infrastructure/pipeline/ -run TestReplayService -v`
Expected: FAIL（replay.go undefined）

- [ ] **Step 5: 实现 `replay.go`**

```go
package pipeline

import (
 "context"
 "encoding/json"
 "fmt"
 "strconv"
 "time"

 "github.com/nats-io/nats.go/jetstream"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

// MaxDLQReplay 是单条死信事件的最大重放次数（防重放风暴）。
const MaxDLQReplay = 3

const (
 replayHeaderReplayed   = "Replayed"
 replayHeaderReplayCount = "ReplayCount"
)

// ReplayService 定向重放 DLQ 事件回原始 raw subject（error_code 过滤）。
// tenantID 一律从事件 TenantID 字段派生，请求方无权指定——防跨租户重放。
type ReplayService struct {
 js     jetstream.JetStream
 logger *zap.Logger
}

type ReplayResult struct {
 Total     int `json:"total"`
 Replayed  int `json:"replayed"`
 Skipped   int `json:"skipped"`
 Failed    int `json:"failed"`
}

// ReplayByErrorCode 从 DLQ stream 拉取事件，error_code 匹配且 payload 非空
// 且未超重放上限的，重发回 memory.raw.<tenant>。先发布后标记：publish 成功
// 才更新事件 header；失败的事件计入 Failed，不更新 header（下次可重试）。
func (s *ReplayService) ReplayByErrorCode(ctx context.Context, errorCode string) (ReplayResult, error) {
 // 1. DLQ stream 消息消费（jetstream Consumer / FetchAll，subject MemoryDLQSubject.>）
 // 2. 过滤：ErrorCode == errorCode、Payload 非空、header ReplayCount < MaxDLQReplay
 // 3. 每个候选：tenantID := ev.TenantID；publish(ctx, "memory.raw."+tenantID, ev.Payload,
 //      jetstream.WithMsgID(fmt.Sprintf("replay:%d", ev.StreamSequence)))  // raw stream 幂等
 // 4. publish 成功后：重发布回 DLQ subject 同 dedupID（deadLetterDedupID(ev)），
 //    header 带 Replayed=true、ReplayCount=<old+1>
 // 5. 汇总 ReplayResult
 // 错误处理：单事件失败不中断（计入 Failed 继续），只对 stream 读取失败返回 error。
}
```

（此 task 实现需精确 jetstream API——`js.PullMessages`/`Fetch` 模式参考 `pipeline_test.go` 现有 embedded NATS 用法与 embedder 消费模式；步骤内给出可编译代码。）

- [ ] **Step 6: handler + 路由 + wiring**

handler（新 `api/http/handler/memory_dlq_replay_handler.go` 或并入现有 memory admin handler——按现有 memory handler 文件结构）：

```go
// ReplayDlq POST /admin/memory/dlq/replay — 定向重放死信事件。
func (h *MemoryDlqReplayHandler) Replay(c *gin.Context) {
 var req struct {
  ErrorCode string `json:"errorCode"`
 }
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 if req.ErrorCode == "" {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, fmt.Errorf("errorCode is required")))
  return
 }
 result, err := h.svc.ReplayByErrorCode(c.Request.Context(), req.ErrorCode)
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, result)
}
```

`router.go`（registerLLMAdmin 或 global admin 组，:243 `/admin` 处）：挂 `POST /admin/memory/dlq/replay`，global admin 鉴权（现有 `RequireGlobalAdmin`）。

`api/wiring/memory.go`：`buildMemoryPipeline` 内构造 `pipeline.NewReplayService(c.Storage.NATS, logger)` 存到 Container 新字段 `Memory.DLQReplay`；关闭顺序随现有逆序关闭（无独立 goroutine，无生命周期成本）。

- [ ] **Step 7: grafana 告警**

按 `grafana/` 现有规则文件结构新增（参照现有 `memory_dlq_total` 相关规则）：

```
- alert: MemoryEmbedUnavailable
  expr: increase(memory_embed_unavailable_total[15m]) > 0
  for: 10m
  labels: { severity: warning }
  annotations: { summary: "租户无可用嵌入模型，记忆写入持续进入 DLQ" }
```

- [ ] **Step 8: 跑测试**

Run: `go test -short ./internal/memory/... ./api/http/... ./api/wiring/...`
Expected: PASS（G4 filter-miss / G5 跨租户 / G6 幂等断言）

- [ ] **Step 9: Commit**

```bash
git add internal/memory/infrastructure/pipeline/metrics.go internal/memory/infrastructure/pipeline/embedder.go internal/memory/infrastructure/pipeline/dead_letter.go internal/memory/infrastructure/pipeline/replay.go internal/memory/infrastructure/pipeline/replay_test.go api/http/handler/ api/http/router.go api/wiring/memory.go grafana/
git commit -m "feat(memory): 异常感知四层——embed 不可用指标 + DLQ 存原始 payload + 定向重放 API（幂等/租户派生/上限）"
```

---

### Task 8: 前端 —— 设为默认 + 记忆页健康提示

**Files:**

- Modify: `web/src/modules/llm/api/llm.api.ts`（`setDefaultEmbedding`）
- Modify: `web/src/modules/llm/pages/ModelListPage.tsx`（"设为默认"操作 + 默认标识 tag）
- Modify: `web/src/modules/memory/api/memory-user.api.ts`（`getStats` 返回类型加 `embedModelConfigured`）
- Modify: 后端 stats handler `api/http/handler/user_memory_handler.go:163` 附近（响应加 `embedModelConfigured`）
- Modify: `web/src/modules/memory/pages/MyMemoriesPage.tsx`（健康提示）
- Test: `web/src/modules/llm/api/llm.api.test.ts`（若存在）、`web/src/modules/memory/api/memory-user.api.test.ts:57`（stats 断言扩展）

**Interfaces:**

- Consumes: Task 4 的 `PUT /admin/models/:id/default-embedding`；`GET /admin/models` 响应 `defaultEmbedding`（Task 1 的 domain JSON tag 自动生效）
- Produces: `setDefaultEmbedding(id, enabled)`；ModelListPage 操作列按钮 + tag；MyMemoriesPage Alert

- [ ] **Step 1: 后端 stats 加 embedModelConfigured**

`user_memory_handler.go:163` 附近响应结构加字段：

```go
  EmbedModelConfigured: s.embedModelConfigured(ctx, tenantID), // 调 registry.ResolveDefaultEmbeddingModel != ""
```

（handler 需要 registry——注入方式按现有 handler 构造；无可用模型时 false，前端显示提示。）

- [ ] **Step 2: 前端 API + 页面**

`llm.api.ts`（`toggleModel` 之后）：

```ts
setDefaultEmbedding: async (id: string, enabled: boolean): Promise<void> => {
  await api.put(`/admin/models/${id}/default-embedding`, { enabled });
},
```

`ModelListPage.tsx`：`useModels` 返回扩展（或页面直接调 API）——列操作区加"设为默认"按钮（`record.capabilities` 含 `embedding` 且 `record.enabled` 时显示；`record.defaultEmbedding` 时显示 tag "默认嵌入" + 取消按钮）；成功后 `refresh()`；错误 `message.error({ content: err.response?.data?.error || '操作失败', duration: 0 })`。

`MyMemoriesPage.tsx`：`stats.embedModelConfigured === false` 时渲染 `Alert`（"未配置嵌入模型，记忆可能无法写入，请到模型管理页配置" + 引导链接）。

- [ ] **Step 3: 前端测试**

`llm.api.test.ts` 追加（若不存在按现有 api test 模式建）：

```ts
it('sets default embedding', async () => {
  await llmApi.setDefaultEmbedding('m1', true);
  expect(api.put).toHaveBeenCalledWith('/admin/models/m1/default-embedding', { enabled: true });
});
```

`memory-user.api.test.ts:57` stats 断言加 `embedModelConfigured: true` 字段。

- [ ] **Step 4: 跑测试 + build**

Run: `make fe-lint && make fe-build`
Run: `cd web && npx vitest run`（或 `make fe-test` 现有命令）
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/llm/api/llm.api.ts web/src/modules/llm/pages/ModelListPage.tsx web/src/modules/memory/api/memory-user.api.ts web/src/modules/memory/pages/MyMemoriesPage.tsx api/http/handler/user_memory_handler.go
git commit -m "feat(web): 模型管理页设为默认嵌入 + 记忆页未配置提示（embedModelConfigured）"
```

---

### Task 9: 端到端验收

- [ ] **Step 1: 全量验证**

Run: `make test-verify-before-pr`（走 `stratum-e2e-development` skill 流程；R3 自动选择本地无头 Chromium short）
Expected: 全绿；failed/skipped/unreconciled 阻断

- [ ] **Step 2: 状态机手工验证点（写入 e2e 报告）**

- 模型管理页：给 embedding 模型设默认 → tag 出现；再设另一个 → 前一个 tag 消失（GET /admin/models 断言 `defaultEmbedding` 唯一）
- 禁用默认模型 → tag 清除（Toggle 自清理）
- 换默认模型 → Milvus 出现新名 collection（`memory_<tenant>_<newmodel>`），旧 collection 数据不参与召回
- 无嵌入模型租户 → 记忆页显示"未配置"提示；embedder 死信 error_code=`embed_service_unavailable`；`memory_embed_unavailable_total` 计数
- DLQ 重放：配置修复后 `POST /admin/memory/dlq/replay {errorCode:"embed_service_unavailable"}` → raw subject 重收、记忆恢复写入；重复调用幂等

- [ ] **Step 3: PR**

```bash
git push -u origin feat/embedding-profile
gh pr create --base main
```

PR 描述含 What/Why/HowToTest；CI 全绿后合并。

---

## Self-Review 记录

**Spec 覆盖：**

- §3 数据模型 → Task 1（DDL + 自清理 + SetDefaultEmbedding + G1/G2/G11）
- §4 解析规则 → Task 3（ResolveDefaultEmbeddingModel + wiring 3 处 + G9/G10 + 字典序 pin）
- §5 维度收敛 → Task 2（constants.DimensionForModel + v2 修正 + 发布耦合由 Task 5/6 同分支保证）
- §6 变更策略 → Task 5（memory）+ Task 6（knowledge）（命名 + legacy 回退 + dim 检查 + 先于 drift）
- §7 前端 → Task 8
- §8 API → Task 4（admin 组）
- §9 异常感知 → Task 7（指标 + 页面 + 告警 + DLQ payload 重放）
- §10 测试 G1-G11 → Task 1（G1/G2/G11）、Task 2（G3）、Task 3（G9/G10/字典序）、Task 5（G7 memory/G8）、Task 6（G7 knowledge）、Task 7（G4/G5/G6）、Task 8（前端 vitest）
- §11 范围外 → 无对应 task（re-embed/重放 UI/agent 差异化/workspace 不可变/tab 重构均未实现）

**已知偏差（实现期必须保持）：**

1. `embedding.DimensionForModel` → `pkg/constants.DimensionForModel`（依赖方向，见 Global Constraints）
2. `DeleteWorkspaceData`/cleaner 删除路径无 model 上下文 → 删 legacy 名 + 注入 embedder 的模型新名（cleaner 从 `config->>'embedding_model'` 读）；非全量删除，注释注明
3. `RetrieveRelevantChunks` 增加 `embedModel` 参数（否则新名无法构造）
4. DLQ 重放幂等用 JetStream `WithMsgID`（`replay:<StreamSequence>`）——与 spec"replayed 标记"同效，实现更简

**Placeholder 扫描：** 无 TBD/TODO；Task 7 的 ReplayByErrorCode 消费 API 标注"参考 pipeline_test.go 现有 embedded NATS 用法"——实现时以实际 jetstream API 为准（步骤给出结构而非逐行，属合理工程留白，非 placeholder）。

**类型一致性：** `CollectionName(_, workspaceID, embedModel)`、`memoryCollectionName(tenantID, model)`、`factsCollectionName(tenantID, model)`、`Model()`、`SetDefaultEmbedding(ctx, tenantID, id, enabled)`、`ResolveDefaultEmbeddingModel(ctx, tenantID) (string, error)` 在跨 task Interfaces 中一致。
