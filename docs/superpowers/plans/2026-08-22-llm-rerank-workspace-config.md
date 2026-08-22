# LLM 语义重排与证据判断 — workspace 显式模型配置 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 judge + rerank 两个 LLM 模型从平台级 env 配置迁移为 **workspace 显式配置**（`rerank_model` / `judge_model` 落入 workspace config JSONB），无 env、无 Recommended 兜底；空值 = 关闭对应能力，builtin 无模型保存显式拒绝。

**Architecture:** 模型从 `WorkspaceConfig` 解析 → 经 `RerankRequest.Model` / `SetSufficiencyJudgeResolver` 传到 wiring 组合根装配的 LLM 组件；目录校验走 `port.ModelExists` 新增 `CapChat` 能力。rerank 运行期失败 fail-open 降级为召回分数排序，judge 运行期失败 fail-closed 放行。PATCH 字符串字段清空用 NUL 前缀 sentinel 编码（与 `ScoreThresholdResetSentinel` 同构）。

**Tech Stack:** Go 1.25（gin / pgx / Milvus SDK）、proto3 + protoc-gen-ginstruct、React 18 + antd 5 + zod。

## Global Constraints

（来自 spec §1，逐字保留用户裁决）

1. **配置层级**：两个模型（judge + rerank）都必须显式配置在 workspace 配置中（`rerank_model` / `judge_model`），无 env、无 Recommended 兜底。空 = 关闭对应能力（当 `reranking` 不是 builtin 时 `rerank_model` 不适用；`judge_model` 空 = judge 门关闭）。
2. **显式拒绝**：当 `reranking=builtin-score-v1` 但 workspace 没有 `rerank_model` 时，保存/更新必须返回验证错误（类似 `ErrEmbeddingModelRequired`），不自动降级、不静默兜底。
3. **失败语义**：rerank **fail-open**（LLM 调用失败/超时/解析失败 → WARN + degraded 指标 → 降级为按召回分数排序，检索永不因重排失败）；judge **fail-closed**（judge 未装配/调用失败/超时 → WARN + 放行，行为与不配置时一致，绝不误杀检索）。
4. **重排范围**：仅 top-N 精排 —— 先按召回分数取前 N 条（`RerankLLMTopN=10`，与池大小取 min），再做一次 listwise 打分。
5. **模型来源唯一**：模型从 workspace 配置解析，经 `port.ModelExists`（capability `CapChat`）校验在 enabled chat 目录；不存在 → 保存被拒（遵循 embedding_model 先例）。

（补充：目录查询失败 → 传播包装错误（5xx）；仅 `!ok` → `ErrInvalidRerankModel`/`ErrInvalidJudgeModel`（400）。`AgentFactCheckConfig` 是死配置，本次不动。`RerankConfigured()`（外部 Cohere 门）保留，仅删 `RerankLLMConfigured()`。evaluation 快照语义重排集成留作独立任务（快照无 RerankModel → 空模型守卫 fail-open 降级，接受）。）

---

## File Structure

| 文件 | 任务 | 职责变化 |
|---|---|---|
| `proto/knowledge/rag.proto` | T1 | WorkspaceConfig 加 `rerank_model=10` / `judge_model=11` |
| `internal/knowledge/domain/workspace.go` | T2 | 2 字段 + 3 errors + 3 sentinels + Validate + applyRerankSettings |
| `internal/knowledge/domain/workspace_test.go` | T2 | Validate/MergeUpdate 用例 |
| `internal/knowledge/infrastructure/persistence/workspace_repo.go` | T3 | JSONB 2 字段（不带 omitempty） |
| `internal/knowledge/infrastructure/persistence/workspace_repo_test.go` | T3 | round-trip 用例 |
| `internal/knowledge/application/workspace_service.go` | T4 | 3 aliases + validateModelsInCatalogue builtin-first + CapChat |
| `internal/knowledge/application/workspace_service_extra_test.go` | T4 | fakeModelExists.chat + 用例 |
| `internal/knowledge/domain/port/model_exists.go` | T4 | `CapChat ModelCapability = "chat"` |
| `api/wiring/knowledge.go` | T4/T5/T6 | adapter CapChat case / wireKnowledgeJudge resolver / wireSemanticReranker 简化 |
| `internal/knowledge/application/rag_service.go` | T5 | RAGQueryRequest 2 字段 + resolvedWorkspace struct + SetSufficiencyJudgeResolver + rerankSemantic 模型传递 |
| `internal/knowledge/application/evidence_gate.go` | T5 | judgeSufficiencyGate 带 model 参数 |
| `internal/knowledge/application/evidence_gate_test.go` | T5 | resolver 适配 |
| `internal/knowledge/application/rag_service_rerank_test.go` | T5 | 6 处 RerankModel 适配 |
| `api/wiring/llm_reranker.go` | T6 | 移除 model + 空模型守卫 + 签名 |
| `api/wiring/llm_reranker_test.go` | T6 | 重写 stub/签名/目录测试 |
| `api/http/handler/rag_handler.go` | T7 | DTO 双向映射 + sentinel 编码 |
| `api/http/handler/rag_handler_test.go` | T7 | PATCH 拒绝 + DTO/sentinel 单测 |
| `api/middleware/error_mapping.go` | T8 | 3 条 400 映射 |
| `config/config.go` | T8 | 删 KnowledgeJudge/KnowledgeRerank 结构体 + bindings + RerankLLMConfigured |
| `config/config_test.go` | T8 | 删 TestLoadKnowledgeRerankConfig |
| `scripts/knowledge-rerank-workspaces-migration.sh` | T9 | 逐 tenant schema 幂等迁移 |
| `docs/agent/migration-tenant.md` / spec | T9 | 部署时序说明 |
| `web/src/modules/knowledge/model/knowledge.ts` | T10 | schema 2 字段 |
| `web/src/modules/knowledge/components/WorkspaceConfigForm.tsx` | T10 | chatModels prop + 条件 rerank_model + judge_model + tooltip |
| `web/src/modules/knowledge/hooks/useKnowledgeDetailPage.ts` | T10 | chatModels + 回填 + payload |
| `web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx` | T10 | 传 chatModels |
| `web/src/modules/knowledge/hooks/useKnowledgeDetailPage.test.tsx` | T10 | mock llm.api |
| `web/src/modules/knowledge/components/__tests__/WorkspaceConfigForm.test.tsx` | T10 | chatModels prop |
| `docs/superpowers/plans/2026-08-22-llm-rerank.md` | T11 | 同步过期的 env 引用 |

---

### Task 1: proto 字段 + `make proto-gen`

**Files:**

- Modify: `proto/knowledge/rag.proto:8-18`（`WorkspaceConfig` message）
- 生成物：`api/http/dto/gen/rag.go`、`web/src/services/gen/`（不入 git，运行 `make proto-gen` 重新生成）

**Interfaces:**

- Produces: proto 契约新增 `rerank_model`（tag 10）/ `judge_model`（tag 11）；`make proto-gen` 后 `gen.WorkspaceConfig` 自动带 `RerankModel string json:"rerank_model"` / `JudgeModel string json:"judge_model"`（生成 DTO 无 omitempty），`web/src/services/gen/` 类型同步。

- [ ] **Step 1: 修改 proto**

`proto/knowledge/rag.proto` 的 `WorkspaceConfig` message 追加两字段（紧跟 `rerank_top_k = 9`）：

```proto
message WorkspaceConfig {
  string embedding_model = 1;
  string chunking_strategy = 2;
  int32 chunk_size = 3;
  int32 chunk_overlap = 4;
  string query_mode = 5;
  int32 top_k = 6;
  string reranking = 7; // "" / "builtin-score-v1" / "provider:model"
  float score_threshold = 8; // float32:映射表 float → float32
  int32 rerank_top_k = 9; // 0 = use TopK
  // rerank_model: builtin-score-v1 的 LLM 语义重排模型（workspace 显式配置）。
  // judge_model: 证据充分性判断模型；空 = judge 门关闭。
  string rerank_model = 10;
  string judge_model = 11;
}
```

- [ ] **Step 2: 重新生成契约代码**

Run: `make proto-gen`
Expected: 无报错；`api/http/dto/gen/rag.go` 中 `gen.WorkspaceConfig` 新增 `RerankModel`/`JudgeModel` 两字段。生成物不入 git，`git status` 不显示该文件变更。

- [ ] **Step 3: Commit**

```bash
git add proto/knowledge/rag.proto
git commit -m "feat(knowledge): add rerank_model/judge_model to WorkspaceConfig proto contract"
```

---

### Task 2: domain — `WorkspaceConfig` 字段、sentinel errors、显式拒绝校验

**Files:**

- Modify: `internal/knowledge/domain/workspace.go`（sentinel errors 11-22、struct 83-98、ScoreThresholdResetSentinel 105、Validate 129-146、applyRerankSettings 222-244）
- Test: `internal/knowledge/domain/workspace_test.go`

**Interfaces:**

- Produces: 3 个新 sentinel errors `ErrRerankModelRequired`/`ErrInvalidRerankModel`/`ErrInvalidJudgeModel`；3 个字符串 sentinel `RerankingResetSentinel = "\x00rerank_reset"` / `RerankModelResetSentinel = "\x00rerank_model_reset"` / `JudgeModelResetSentinel = "\x00judge_model_reset"`；`WorkspaceConfig` 新字段 `RerankModel string` / `JudgeModel string`。Task 3/4/7 依赖这些。

- [ ] **Step 1: 写失败测试**

在 `internal/knowledge/domain/workspace_test.go` 追加两个测试函数（放在文件尾部）：

```go
func TestWorkspaceConfigValidateRerankModel(t *testing.T) {
 base := WorkspaceConfig{
  EmbeddingModel:   "text-embedding-v3",
  QueryMode:        "hybrid",
  ChunkingStrategy: "recursive",
 }
 cases := []struct {
  name    string
  cfg     WorkspaceConfig
  wantErr error
 }{
  {
   name: "builtin rerank 无模型拒绝",
   cfg: func() WorkspaceConfig {
    c := base
    c.Reranking = "builtin-score-v1"
    return c
   }(),
   wantErr: ErrRerankModelRequired,
  },
  {
   name: "builtin rerank 有模型通过",
   cfg: func() WorkspaceConfig {
    c := base
    c.Reranking = "builtin-score-v1"
    c.RerankModel = "qwen-turbo"
    return c
   }(),
  },
  {
   name: "外部 rerank 不需要 rerank_model",
   cfg: func() WorkspaceConfig {
    c := base
    c.Reranking = "cohere:rerank-multilingual-v3.0"
    return c
   }(),
  },
  {
   name:    "judge_model 空可通过（门关闭）",
   cfg:     base,
  },
  {
   name: "embedding 缺失优先于 rerank 缺失",
   cfg: func() WorkspaceConfig {
    c := base
    c.EmbeddingModel = ""
    c.Reranking = "builtin-score-v1"
    return c
   }(),
   wantErr: ErrEmbeddingModelRequired,
  },
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   err := tc.cfg.Validate()
   if tc.wantErr == nil {
    if err != nil {
     t.Fatalf("Validate() = %v, want nil", err)
    }
    return
   }
   if !errors.Is(err, tc.wantErr) {
    t.Fatalf("Validate() = %v, want %v", err, tc.wantErr)
   }
  })
 }
}

func TestMergeUpdateRerankModelSentinels(t *testing.T) {
 base := WorkspaceConfig{
  EmbeddingModel:   "text-embedding-v3",
  QueryMode:        "hybrid",
  ChunkingStrategy: "recursive",
  Reranking:        "builtin-score-v1",
  RerankModel:      "qwen-turbo",
  JudgeModel:       "qwen-plus",
 }
 t.Run("显式清空 judge_model（sentinel）", func(t *testing.T) {
  got, err := base.MergeUpdate(WorkspaceConfig{JudgeModel: JudgeModelResetSentinel})
  if err != nil {
   t.Fatalf("MergeUpdate() error = %v", err)
  }
  if got.JudgeModel != "" {
   t.Fatalf("JudgeModel = %q, want cleared", got.JudgeModel)
  }
  if got.RerankModel != "qwen-turbo" {
   t.Fatalf("RerankModel must be untouched, got %q", got.RerankModel)
  }
 })
 t.Run("零值 judge_model 不覆盖（partial 语义）", func(t *testing.T) {
  got, err := base.MergeUpdate(WorkspaceConfig{})
  if err != nil {
   t.Fatalf("MergeUpdate() error = %v", err)
  }
  if got.JudgeModel != "qwen-plus" {
   t.Fatalf("JudgeModel = %q, want preserved", got.JudgeModel)
  }
 })
 t.Run("显式设置 rerank_model 覆盖", func(t *testing.T) {
  got, err := base.MergeUpdate(WorkspaceConfig{RerankModel: "qwen-max"})
  if err != nil {
   t.Fatalf("MergeUpdate() error = %v", err)
  }
  if got.RerankModel != "qwen-max" {
   t.Fatalf("RerankModel = %q, want qwen-max", got.RerankModel)
  }
 })
 t.Run("显式清空 reranking（sentinel）", func(t *testing.T) {
  got, err := base.MergeUpdate(WorkspaceConfig{Reranking: RerankingResetSentinel})
  if err != nil {
   t.Fatalf("MergeUpdate() error = %v", err)
  }
  if got.Reranking != "" {
   t.Fatalf("Reranking = %q, want cleared", got.Reranking)
  }
 })
}
```

`workspace_test.go` 已 import `errors`（现 TestSentinelErrorsNonNil 用 `errors.Is`），无需改 import。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/knowledge/domain/ -run 'TestWorkspaceConfigValidateRerankModel|TestMergeUpdateRerankModelSentinels'`
Expected: FAIL —— 编译错误：`ErrRerankModelRequired` / `RerankingResetSentinel` 等未定义、`RerankModel` 字段不存在。

- [ ] **Step 3: 实现 domain 改动**

`internal/knowledge/domain/workspace.go`：

(a) sentinel errors 块（11-22 行）末尾追加：

```go
 ErrRerankModelRequired = errors.New("rerank model is required")
 ErrInvalidRerankModel  = errors.New("unsupported rerank model")
 ErrInvalidJudgeModel   = errors.New("unsupported judge model")
```

(b) `ScoreThresholdResetSentinel`（105 行）之后追加字符串 sentinels：

```go
// RerankingResetSentinel / RerankModelResetSentinel / JudgeModelResetSentinel 是
// MergeUpdate 对字符串字段「显式清空」的编码：partial 合并以零值表示"未提供"，
// 但 reranking/rerank_model/judge_model 的 "" 是合法关闭值。handler PATCH（整体
// 替换契约）把用户显式传的 "" 转成这些 NUL 前缀哨兵，domain 侧转回 ""；proposal
// 等 partial 调用方保持零值=未传语义，互不干扰。哨兵仅存在于内存转换瞬间，绝不
// 落库（NUL 字节也保证不会与任何真实 JSONB 值碰撞）。
const (
 RerankingResetSentinel   = "\x00rerank_reset"
 RerankModelResetSentinel = "\x00rerank_model_reset"
 JudgeModelResetSentinel  = "\x00judge_model_reset"
)
```

(c) `WorkspaceConfig` struct（83-98）末尾（`RerankTopK int` 之后）追加：

```go
 // RerankModel 是 builtin-score-v1 的 LLM 语义重排模型（workspace 显式配置）；
 // 空 = builtin 未装配（保存被拒，见 Validate）。JudgeModel 是证据充分性 judge
 // 模型；空 = judge 门关闭（fail-closed 放行）。
 RerankModel string
 JudgeModel  string
```

(d) `Validate`（129-146）—— embedding 检查**之后**追加（保持 embedding 缺失优先，review M1）：

```go
 if c.EmbeddingModel == "" {
  return ErrEmbeddingModelRequired
 }
 if c.Reranking == "builtin-score-v1" && c.RerankModel == "" {
  return ErrRerankModelRequired
 }
 return nil
```

(e) `applyRerankSettings`（222-244）整体替换为（sentinel 检查**必须优先**于 `!= ""`，否则 NUL 前缀 sentinel 会被 `ValidRerankIdentity` 拒绝）：

```go
func (c WorkspaceConfig) applyRerankSettings(partial WorkspaceConfig) (WorkspaceConfig, error) {
 out := c
 if partial.Reranking == RerankingResetSentinel {
  out.Reranking = ""
 } else if partial.Reranking != "" {
  if !ValidRerankIdentity(partial.Reranking) {
   return c, ErrInvalidRerankIdentity
  }
  out.Reranking = partial.Reranking
 }
 if partial.ScoreThreshold > 0 {
  if partial.ScoreThreshold > 1 {
   return c, ErrInvalidScoreThreshold
  }
  out.ScoreThreshold = partial.ScoreThreshold
 } else if partial.ScoreThreshold == ScoreThresholdResetSentinel {
  // 显式 0 重置：handler PATCH 整体替换契约把 0 编码为哨兵，partial
  // 语义下零值=未传，0 只能经哨兵显式写入，防止"设了关不掉"。
  out.ScoreThreshold = 0
 }
 if partial.RerankTopK > 0 {
  out.RerankTopK = partial.RerankTopK
 }
 if partial.RerankModel == RerankModelResetSentinel {
  out.RerankModel = ""
 } else if partial.RerankModel != "" {
  out.RerankModel = partial.RerankModel
 }
 if partial.JudgeModel == JudgeModelResetSentinel {
  out.JudgeModel = ""
 } else if partial.JudgeModel != "" {
  out.JudgeModel = partial.JudgeModel
 }
 return out, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/knowledge/domain/`
Expected: PASS（含既有 `TestWorkspaceConfigValidate`/`TestMergeUpdate`/`TestSentinelErrorsNonNil`，它们不引用新字段/哨兵，不受影响）。

- [ ] **Step 5: Commit**

```bash
git add internal/knowledge/domain/workspace.go internal/knowledge/domain/workspace_test.go
git commit -m "feat(knowledge): workspace-explicit rerank/judge models with reset sentinels"
```

---

### Task 3: persistence — JSONB 字段（不带 omitempty）

**Files:**

- Modify: `internal/knowledge/infrastructure/persistence/workspace_repo.go`（jsonbConfig 29-39、toJSONB 41-54、fromJSONB 56-68）
- Test: `internal/knowledge/infrastructure/persistence/workspace_repo_test.go`

**Interfaces:**

- Consumes: Task 2 的 `domain.WorkspaceConfig.RerankModel/JudgeModel`。
- Produces: JSONB 键 `rerank_model`/`judge_model` **不带 omitempty**（与 embedding_model 一致）——使迁移谓词 `config->>'rerank_model' IS NULL` 能可靠区分"部署前旧行（无 key）"与"部署后新行（有 key）"。

- [ ] **Step 1: 写失败测试**

在 `workspace_repo_test.go` 追加（若文件无既有 round-trip 用例则新建此函数；参照该文件既有 `toJSONB`/`fromJSONB` 测试风格）：

```go
func TestJSONBRoundTripRerankAndJudgeModels(t *testing.T) {
 in := domain.WorkspaceConfig{
  EmbeddingModel:   "text-embedding-v3",
  ChunkSize:        512,
  ChunkOverlap:     64,
  QueryMode:        "hybrid",
  TopK:             5,
  ChunkingStrategy: "recursive",
  Reranking:        "builtin-score-v1",
  RerankModel:      "qwen-turbo",
  JudgeModel:       "qwen-plus",
 }
 out := fromJSONB(jsonbConfig{})
 _ = json.Unmarshal([]byte(toJSONB(in)), &out)
 if out.RerankModel != "qwen-turbo" || out.JudgeModel != "qwen-plus" {
  t.Fatalf("round-trip lost models: RerankModel=%q JudgeModel=%q", out.RerankModel, out.JudgeModel)
 }
 if !strings.Contains(toJSONB(in), `"rerank_model":"qwen-turbo"`) {
  t.Fatalf("rerank_model key missing/omitempty in JSON: %s", toJSONB(in))
 }
}

func TestJSONBEmptyModelsWriteEmptyKeys(t *testing.T) {
 // 空模型也写入显式键（不带 omitempty）：部署后新行必有键，迁移谓词
 // config->>'rerank_model' IS NULL 只命中部署前旧行。
 cfg := domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}
 if got := toJSONB(cfg); !strings.Contains(got, `"rerank_model":""`) || !strings.Contains(got, `"judge_model":""`) {
  t.Fatalf("empty models must serialize explicit empty keys, got: %s", got)
 }
}
```

若文件缺 `encoding/json`/`strings` import 则补上。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/knowledge/infrastructure/persistence/ -run 'TestJSONB'`
Expected: FAIL —— `jsonbConfig` 无 `RerankModel`/`JudgeModel` 字段。

- [ ] **Step 3: 实现 persistence 改动**

`workspace_repo.go` 三处同步追加：

```go
type jsonbConfig struct {
 EmbeddingModel   string  `json:"embedding_model"`
 ChunkSize        int     `json:"chunk_size"`
 ChunkOverlap     int     `json:"chunk_overlap"`
 QueryMode        string  `json:"query_mode"`
 TopK             int     `json:"top_k"`
 ChunkingStrategy string  `json:"chunking_strategy"`
 Reranking        string  `json:"reranking,omitempty"`
 ScoreThreshold   float32 `json:"score_threshold,omitempty"`
 RerankTopK       int     `json:"rerank_top_k,omitempty"`
 // 不带 omitempty：空模型也显式落键，迁移谓词依赖（见 TestJSONBEmptyModelsWriteEmptyKeys）。
 RerankModel string `json:"rerank_model"`
 JudgeModel  string `json:"judge_model"`
}
```

`toJSONB` 的 struct 字面量追加：

```go
  RerankTopK:       c.RerankTopK,
  RerankModel:      c.RerankModel,
  JudgeModel:       c.JudgeModel,
```

`fromJSONB` 的 struct 字面量追加：

```go
  RerankTopK:       c.RerankTopK,
  RerankModel:      c.RerankModel,
  JudgeModel:       c.JudgeModel,
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/knowledge/infrastructure/persistence/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/knowledge/infrastructure/persistence/workspace_repo.go internal/knowledge/infrastructure/persistence/workspace_repo_test.go
git commit -m "feat(knowledge): persist rerank/judge models in workspace JSONB"
```

---

### Task 4: application — 目录校验（builtin-first + `CapChat`）

**Files:**

- Modify: `internal/knowledge/domain/port/model_exists.go`（CapChat）
- Modify: `internal/knowledge/application/workspace_service.go`（aliases 24-31、validateModelsInCatalogue 118-135）
- Modify: `api/wiring/knowledge.go`（knowledgeModelExistsAdapter.Exists switch，338-356）
- Test: `internal/knowledge/application/workspace_service_extra_test.go`

**Interfaces:**

- Consumes: Task 2 的 3 个 domain errors。
- Produces: `port.CapChat ModelCapability = "chat"`；`validateModelsInCatalogue` 对非空 `RerankModel`/`JudgeModel` 做 CapChat 存在性校验（目录查询失败传播、`!ok` → 对应 400 error）；builtin 空模型检查在 `modelExists == nil` 判断**之前**（覆盖 PATCH 路径，因 `MergeUpdate` 不调 `Validate`）。

- [ ] **Step 1: 写失败测试**

`internal/knowledge/domain/port/model_exists.go` 加 `CapChat` 后（见 Step 3），在 `workspace_service_extra_test.go`：

(a) `fakeModelExists`（186-202 行）加 `chat` 字段 + case：

```go
type fakeModelExists struct {
 embedding map[string]bool
 rerank    map[string]bool
 chat      map[string]bool
 err       error
}

func (f *fakeModelExists) Exists(_ context.Context, model string, capability port.ModelCapability) (bool, error) {
 if f.err != nil {
  return false, f.err
 }
 switch capability {
 case port.CapRerank:
  return f.rerank[model], nil
 case port.CapChat:
  return f.chat[model], nil
 default:
  return f.embedding[model], nil
 }
}
```

(b) 追加测试函数：

```go
func TestValidateModelsInCatalogueChatModels(t *testing.T) {
 base := domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}
 catalogue := &fakeModelExists{
  embedding: map[string]bool{"text-embedding-v3": true},
  chat:      map[string]bool{"qwen-turbo": true},
 }
 svc := &WorkspaceService{modelExists: catalogue, logger: zap.NewNop()}

 t.Run("rerank_model 不在 chat 目录拒绝", func(t *testing.T) {
  cfg := base
  cfg.RerankModel = "qwen-max"
  if err := svc.validateModelsInCatalogue(context.Background(), cfg); !errors.Is(err, domain.ErrInvalidRerankModel) {
   t.Fatalf("err = %v, want ErrInvalidRerankModel", err)
  }
 })
 t.Run("judge_model 不在 chat 目录拒绝", func(t *testing.T) {
  cfg := base
  cfg.JudgeModel = "qwen-max"
  if err := svc.validateModelsInCatalogue(context.Background(), cfg); !errors.Is(err, domain.ErrInvalidJudgeModel) {
   t.Fatalf("err = %v, want ErrInvalidJudgeModel", err)
  }
 })
 t.Run("chat 目录模型通过", func(t *testing.T) {
  cfg := base
  cfg.RerankModel = "qwen-turbo"
  cfg.JudgeModel = "qwen-turbo"
  if err := svc.validateModelsInCatalogue(context.Background(), cfg); err != nil {
   t.Fatalf("err = %v, want nil", err)
  }
 })
 t.Run("目录查询失败传播（5xx 而非 400）", func(t *testing.T) {
  svc.modelExists = &fakeModelExists{embedding: map[string]bool{"text-embedding-v3": true}, err: errors.New("db down")}
  cfg := base
  cfg.JudgeModel = "qwen-turbo"
  err := svc.validateModelsInCatalogue(context.Background(), cfg)
  if err == nil || errors.Is(err, domain.ErrInvalidJudgeModel) {
   t.Fatalf("err = %v, want wrapped db error, not ErrInvalidJudgeModel", err)
  }
 })
 t.Run("builtin 空 rerank_model 在 modelExists 为 nil 时也拒绝", func(t *testing.T) {
  svc.modelExists = nil
  cfg := base
  cfg.Reranking = "builtin-score-v1"
  if err := svc.validateModelsInCatalogue(context.Background(), cfg); !errors.Is(err, domain.ErrRerankModelRequired) {
   t.Fatalf("err = %v, want ErrRerankModelRequired", err)
  }
 })
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/knowledge/application/ -run 'TestValidateModelsInCatalogueChatModels'`
Expected: FAIL —— `port.CapChat` 未定义。

- [ ] **Step 3: 实现**

(a) `internal/knowledge/domain/port/model_exists.go` 常量块追加：

```go
const (
 CapEmbedding ModelCapability = "embedding"
 CapRerank    ModelCapability = "rerank"
 CapChat      ModelCapability = "chat"
)
```

(b) `internal/knowledge/application/workspace_service.go` aliases 块（24-31）追加：

```go
 ErrInvalidRerankModel      = domain.ErrInvalidRerankModel
 ErrRerankModelRequired     = domain.ErrRerankModelRequired
 ErrInvalidJudgeModel       = domain.ErrInvalidJudgeModel
```

(c) `validateModelsInCatalogue`（118-135）整体替换：

```go
func (s *WorkspaceService) validateModelsInCatalogue(ctx context.Context, cfg domain.WorkspaceConfig) error {
 // builtin 空模型检查放在 modelExists==nil 判断之前：PATCH 更新不调 Validate
 // （MergeUpdate 只做 partial 合并），必须在这里兜住显式拒绝（Global Constraint 2）。
 if cfg.Reranking == "builtin-score-v1" && cfg.RerankModel == "" {
  return domain.ErrRerankModelRequired
 }
 if s.modelExists == nil {
  return nil
 }
 if ok, err := s.modelExists.Exists(ctx, cfg.EmbeddingModel, port.CapEmbedding); err != nil {
  return fmt.Errorf("knowledge workspace: check embedding model %q: %w", cfg.EmbeddingModel, err)
 } else if !ok {
  return domain.ErrInvalidEmbeddingModel
 }
 // rerank/judge 模型必须是 enabled chat 目录中的模型（Global Constraint 5）。
 // 目录查询失败传播包装错误（5xx），仅 !ok 返回 400 配置错误。
 if cfg.RerankModel != "" {
  if ok, err := s.modelExists.Exists(ctx, cfg.RerankModel, port.CapChat); err != nil {
   return fmt.Errorf("knowledge workspace: check rerank model %q: %w", cfg.RerankModel, err)
  } else if !ok {
   return domain.ErrInvalidRerankModel
  }
 }
 if cfg.JudgeModel != "" {
  if ok, err := s.modelExists.Exists(ctx, cfg.JudgeModel, port.CapChat); err != nil {
   return fmt.Errorf("knowledge workspace: check judge model %q: %w", cfg.JudgeModel, err)
  } else if !ok {
   return domain.ErrInvalidJudgeModel
  }
 }
 if provider, model := domain.SplitRerankIdentity(cfg.Reranking); !domain.AllowedRerankIdentities[provider] {
  if ok, err := s.modelExists.Exists(ctx, model, port.CapRerank); err != nil {
   return fmt.Errorf("knowledge workspace: check rerank model %q: %w", model, err)
  } else if !ok {
   return domain.ErrInvalidRerankIdentity
  }
 }
 return nil
}
```

(d) `api/wiring/knowledge.go` 的 `knowledgeModelExistsAdapter.Exists`（338-356）switch 追加 case：

```go
 switch capability {
 case knowledgeport.CapRerank:
  names, err = a.registry.ListRerankModelsByTenant(ctx)
 case knowledgeport.CapChat:
  names, err = a.registry.ListChatModelsByTenant(ctx)
 default: // CapEmbedding
  names, err = a.registry.ListEmbeddingModelsByTenant(ctx)
 }
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/knowledge/application/ -run 'TestValidateModelsInCatalogue'` 然后 `go build ./api/... ./internal/knowledge/...`
Expected: PASS + 编译通过。

- [ ] **Step 5: Commit**

```bash
git add internal/knowledge/domain/port/model_exists.go internal/knowledge/application/workspace_service.go internal/knowledge/application/workspace_service_extra_test.go api/wiring/knowledge.go
git commit -m "feat(knowledge): validate rerank/judge models against chat catalogue"
```

---

### Task 5: application — 模型从请求传递（RAGQueryRequest / resolvedWorkspace / judge resolver）

**Files:**

- Modify: `internal/knowledge/application/rag_service.go`（searchWorkspace 86-111、resolveWorkspaceConfig 113-139、RAGService struct 188-210、SetSufficiencyJudge 233-235、RAGQueryRequest 253-275、rerankSemantic 1105-1136、searchWorkspaceWithEvidence 1299-1340）
- Modify: `internal/knowledge/application/evidence_gate.go`（17-38）
- Modify: `api/wiring/knowledge.go`（wireKnowledgeJudge 131-144 最小 seam）
- Test: `internal/knowledge/application/evidence_gate_test.go`、`internal/knowledge/application/rag_service_rerank_test.go`

**Interfaces:**

- Consumes: Task 2 的 `RerankModel`/`JudgeModel` 字段；`port.RerankRequest`（已含 `Model string`）；`port.SufficiencyJudge`。
- Produces: `RAGQueryRequest` 新字段 `RerankModel`/`JudgeModel`；`resolvedWorkspace` struct；`SufficiencyJudgeResolver` 类型 + `SetSufficiencyJudgeResolver`；`judgeSufficiencyGate(ctx, tenantID, workspace, query, model, result)`；`rerankSemantic` 用 `req.RerankModel`。

- [ ] **Step 1: 写失败测试**

`internal/knowledge/application/evidence_gate_test.go`：

(a) `judge` helper（30-34）与 `TestJudgeSufficiencyGatePreservesStats`（104-114）改用 resolver：

```go
 judge := func(j knowledgeport.SufficiencyJudge) *RAGService {
  rs := NewRAGService(nil, nil, zap.NewNop())
  rs.SetSufficiencyJudgeResolver(func(_ context.Context, _ string) (knowledgeport.SufficiencyJudge, error) { return j, nil })
  return rs
 }
```

```go
 rs := NewRAGService(nil, nil, zap.NewNop())
 rs.SetSufficiencyJudgeResolver(func(_ context.Context, _ string) (knowledgeport.SufficiencyJudge, error) {
  return stubSufficiencyJudge{verdict: knowledgeport.SufficiencyInsufficient}, nil
 })
```

(b) 所有 `rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", ...)` 调用点（81、107）追加 `"qwen-turbo"` model 参数 → `rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "qwen-turbo", ...)`。

(c) 追加新用例覆盖 model/resolver 语义：

```go
func TestJudgeSufficiencyGateModelAndResolverPaths(t *testing.T) {
 rs := NewRAGService(nil, nil, zap.NewNop())
 rs.SetSufficiencyJudgeResolver(func(_ context.Context, model string) (knowledgeport.SufficiencyJudge, error) {
  if model != "qwen-turbo" {
   return nil, errors.New("model not in chat catalogue")
  }
  return stubSufficiencyJudge{verdict: knowledgeport.SufficiencyInsufficient}, nil
 })

 t.Run("空 model 短路放行（judge 门关闭）", func(t *testing.T) {
  got := rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "", gateResult())
  if len(got.Sources) == 0 || got.NoAnswer != nil {
   t.Fatalf("empty model must pass through, got NoAnswer=%v", got.NoAnswer)
  }
 })
 t.Run("resolver 失败 fail-closed 放行", func(t *testing.T) {
  got := rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "qwen-max", gateResult())
  if len(got.Sources) == 0 || got.NoAnswer != nil {
   t.Fatalf("resolver failure must pass through, got NoAnswer=%v", got.NoAnswer)
  }
 })
 t.Run("insufficient 升级 insufficient_evidence", func(t *testing.T) {
  got := rs.judgeSufficiencyGate(context.Background(), "tenant-1", "kb", "q", "qwen-turbo", gateResult())
  if len(got.Sources) != 0 || got.NoAnswer == nil || got.NoAnswer.Reason != NoAnswerInsufficientEvidence {
   t.Fatalf("want insufficient_evidence, got sources=%d NoAnswer=%+v", len(got.Sources), got.NoAnswer)
  }
 })
}
```

`internal/knowledge/application/rag_service_rerank_test.go` 6 处请求构造点补 `RerankModel: "qwen-turbo"`，并反转一处模型断言：

- `TestRAGQueryBuiltinSemanticRerankRescores`（请求 350-353 加字段；断言 361 `reranker.lastReq.Model != ""` → `== "qwen-turbo"`）
- `FailsOpenOnError`（请求 389-392）、`SkipsTinyPool`（请求 415-418）、`PartialTailFill`（请求 474-477）、`EmptyLLMResultsFillsTail`（请求 506-510）各加 `RerankModel: "qwen-turbo"`
- `TestRerankSemanticNarrowsToTopN`（:445 直接调 `rerankSemantic`，请求需 `RerankModel: "qwen-turbo"`）

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/knowledge/application/ -run 'TestJudgeSufficiencyGate|TestRAGQueryBuiltinSemanticRerank|TestRerankSemanticNarrowsToTopN|TestRerankSemanticFailsOpen|TestRerankSemanticSkipsTiny|TestRerankSemanticPartialTail|TestRerankSemanticEmptyLLM'`
Expected: FAIL —— 编译错误：`SetSufficiencyJudge` 不存在、`judgeSufficiencyGate` 参数数量不符、`RerankModel` 字段不存在。

- [ ] **Step 3: 实现 rag_service.go**

(a) `RAGService` struct（202）字段替换：

```go
 // judgeResolver 按请求中的 judge 模型解析证据充分性 judge（仅 evidence 路径
 // 消费，Plain Query/API 面板零接触）；nil/解析失败 = fail-closed 放行。
 judgeResolver SufficiencyJudgeResolver
```

(b) struct 之前定义 resolver 类型（`RAGQueryRequest` 附近）：

```go
// SufficiencyJudgeResolver 按请求中的 judge 模型解析证据充分性 judge；模型未知/
// 目录校验失败返回 error（fail-closed 放行）。wiring 注入闭包，application 不
// import llmgateway（跨 context 接口定义在消费方）。
type SufficiencyJudgeResolver func(ctx context.Context, model string) (knowledgeport.SufficiencyJudge, error)
```

(c) Setter 替换（233-235）：

```go
func (rs *RAGService) SetSufficiencyJudgeResolver(r SufficiencyJudgeResolver) {
 rs.judgeResolver = r
}
```

(d) `RAGQueryRequest`（253-275）末尾（`SkipAccessCheck bool` 之后）追加：

```go
 // RerankModel 是 builtin-score-v1 的 LLM 语义重排模型（workspace 显式配置）；
 // 仅 Reranking=builtin-score-v1 时消费，空模型由重排器 fail-open 拒绝。
 RerankModel string
 // JudgeModel 是证据充分性 judge 模型（workspace 显式配置）；空 = 关闭 judge 门。
 JudgeModel string
```

(e) `resolvedWorkspace` struct + `resolveWorkspaceConfig`（113-139）整体替换：

```go
// resolvedWorkspace 收敛 resolveWorkspaceConfig 的多返回值，避免 6 值元组
// 解构位错（review I2）。
type resolvedWorkspace struct {
 mode          string
 effectiveTopK int
 embedModel    string
 workspaceID   string
 threshold     float32
 rerankModel   string
 judgeModel    string
}

func resolveWorkspaceConfig(ctx context.Context, rs *RAGService, tenantID, ws string, topK int) (resolvedWorkspace, error) {
 rw := resolvedWorkspace{mode: domain.DefaultQueryMode, effectiveTopK: topK}
 if rs.wsRepo == nil {
  return rw, nil
 }
 w, getErr := rs.wsRepo.GetByName(ctx, tenantID, ws)
 if getErr != nil {
  return rw, ErrRAGDependency
 }
 if w == nil {
  return rw, nil
 }
 rw.workspaceID = w.ID
 if w.Config.TopK > 0 {
  rw.effectiveTopK = w.Config.TopK
 }
 rw.embedModel = w.Config.EmbeddingModel
 rw.threshold = w.Config.ScoreThreshold
 if w.Config.QueryMode != "" {
  rw.mode = w.Config.QueryMode
 }
 // 模型来自 workspace 显式配置（Global Constraint 1/5）：RerankModel 供 builtin
 // 重排消费（当前触发面是 snapshot/evaluation 路径，Plain Query/API 面板
 // Reranking 恒空，字段暂为潜在）；JudgeModel 由证据充分性门消费。
 rw.rerankModel = w.Config.RerankModel
 rw.judgeModel = w.Config.JudgeModel
 return rw, nil
}
```

(f) `searchWorkspace`（86-111）改用 rw + 传模型：

```go
func searchWorkspace(ctx context.Context, rs *RAGService, tenantID, viewerID, ws, query string, topK int) wsResult {
 rw, err := resolveWorkspaceConfig(ctx, rs, tenantID, ws, topK)
 if err != nil {
  return wsResult{err: err}
 }
 out, err := rs.Query(ctx, RAGQueryRequest{
  WorkspaceID:    rw.workspaceID,
  Workspace:      ws,
  Question:       query,
  TenantID:       tenantID,
  Mode:           rw.mode,
  TopK:           rw.effectiveTopK,
  EmbeddingModel: rw.embedModel,
  // workspace config 单一事实源：阈值缺省兜底（0=不过滤），避免
  // 配置存库但不生效的装配断点。
  ScoreThreshold: rw.threshold,
  ViewerID:       viewerID,
  // System-actor contexts (privileged wiring paths such as eval workers)
  // carry the same trust as an admin owner and bypass the D2 gate.
  SkipAccessCheck: reqctx.SystemActorFromContext(ctx) != "",
  RerankModel:     rw.rerankModel,
  JudgeModel:      rw.judgeModel,
 })
 if err != nil {
  return wsResult{err: err}
 }
 return wsResult{content: formatSources(out.Sources), noAnswer: out.NoAnswer}
}
```

(g) `searchWorkspaceWithEvidence`（1299-1340）：同 f 的解构改造，构造请求加 `RerankModel: rw.rerankModel, JudgeModel: rw.judgeModel`，且 1324 行调用带 model：

```go
 out = rs.judgeSufficiencyGate(ctx, tenantID, ws, query, rw.judgeModel, out)
```

(h) `rerankSemantic`（1105-1136）—— 删除 1114 行注释、1116 行 `Model: ""` 改为 `req.RerankModel`：

```go
 results, err := rs.semanticReranker.Rerank(ctx, knowledgeport.RerankRequest{
  Query: req.Question, Documents: docs, Model: req.RerankModel, TopN: topN,
 })
```

- [ ] **Step 4: 实现 evidence_gate.go**

`judgeSufficiencyGate`（17-38）整体替换为带 model + resolver 版本（保留 fail-closed 语义）：

```go
// judgeSufficiencyGate 是生成前证据充分性门（仅 evidence 路径挂载，由
// searchWorkspaceWithEvidence 调用）：相似度阈值回答"像不像"，judge 回答
// "能不能推出结论"。判 INSUFFICIENT 时该 workspace 按无内容处理（Sources
// 置空 + NoAnswer=insufficient_evidence，维持 content=="" ⇒ NoAnswer!=nil
// 不变量），经聚合严重度排序上报。fail-closed：model 为空（judge 门关闭）/
// resolver 未装配/解析失败/调用失败/超时 → 原样放行（WARN 留痕），行为与不
// 配置时完全一致，绝不误杀检索。
func (rs *RAGService) judgeSufficiencyGate(ctx context.Context, tenantID, workspace, query, model string, result *RAGQueryResult) *RAGQueryResult {
 if model == "" || rs.judgeResolver == nil || len(result.Sources) == 0 {
  return result
 }
 judge, err := rs.judgeResolver(ctx, model)
 if err != nil {
  rs.logger.Warn("knowledge.judge.sufficiency_degraded",
   zap.String("tenant_id", tenantID), zap.String("workspace", workspace),
   zap.String("model", model), zap.Error(err))
  return result
 }
 if judge == nil {
  rs.logger.Warn("knowledge.judge.sufficiency_unavailable",
   zap.String("tenant_id", tenantID), zap.String("workspace", workspace),
   zap.String("model", model))
  return result
 }
 verdict, err := judge.JudgeSufficiency(ctx, query, formatSources(result.Sources))
 if err != nil {
  rs.logger.Warn("knowledge.judge.sufficiency_degraded",
   zap.String("tenant_id", tenantID), zap.String("workspace", workspace),
   zap.String("model", model), zap.Error(err))
  return result
 }
 if verdict != port.SufficiencyInsufficient {
  return result
 }
 if rs.metrics != nil {
  rs.metrics.IncNoAnswer(tenantID, constants.NoAnswerReasonInsufficientEvidence)
 }
 return &RAGQueryResult{
  NoAnswer:       buildNoAnswer(NoAnswerInsufficientEvidence, result.CandidateCount, 0, result.BestScore),
  BestScore:      result.BestScore,
  CandidateCount: result.CandidateCount,
 }
}
```

- [ ] **Step 5: 实现 wiring 最小 seam（knowledge.go wireKnowledgeJudge）**

`api/wiring/knowledge.go` 131-144 整体替换（保留 Enabled gate + 平台 Timeout，仅把 setter 改为 resolver 以保持编译；模型目录校验 Task 6 补）：

```go
// wireKnowledgeJudge 在 LLM gateway 可用且 KNOWLEDGE_JUDGE_ENABLED 时注入证据
// 充分性 judge 解析器；任一条件不满足保持未装配（judgeResolver nil，fail-closed
// 放行）。模型在运行期从请求解析（workspace 显式 JudgeModel），resolver 闭包
// 每次查询按需构造实例。单独成方法以控制 buildKnowledge 的圈复杂度。
func (c *Container) wireKnowledgeJudge(rag *knowledge.RAGService) {
 if c.LLMGateway == nil || c.LLMGateway.Gateway == nil || !c.Config.KnowledgeJudge.Enabled {
  return
 }
 completer := c.LLMGateway.Gateway
 timeout := c.Config.KnowledgeJudge.Timeout
 var metrics observability.MetricsProvider
 if c.Platform != nil {
  metrics = c.Platform.Metrics
 }
 rag.SetSufficiencyJudgeResolver(func(ctx context.Context, model string) (knowledgeport.SufficiencyJudge, error) {
  return &knowledgeJudge{completer: completer, model: model, timeout: timeout, metrics: metrics}, nil
 })
}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/knowledge/application/ ./api/wiring/`
Expected: PASS。若 `api/wiring/` 有测试依赖 `wireKnowledgeJudge` 的旧行为，检查是否受影响（Task 6 会重写 wiring 测试）。

- [ ] **Step 7: Commit**

```bash
git add internal/knowledge/application/rag_service.go internal/knowledge/application/evidence_gate.go internal/knowledge/application/evidence_gate_test.go internal/knowledge/application/rag_service_rerank_test.go api/wiring/knowledge.go
git commit -m "feat(knowledge): thread workspace rerank/judge models through RAG request"
```

---

### Task 6: wiring — `llmReranker` 空模型守卫 + 简化装配 + judge 目录校验

**Files:**

- Modify: `api/wiring/llm_reranker.go`（struct 22-28、newLLMReranker 30-38、Rerank 52-109、imports）
- Modify: `api/wiring/knowledge.go`（wireSemanticReranker 150-154、semanticRerankerDeps 160-185、llmRerankModelInCatalogue 190-205 → llmChatModelInCatalogue、调用点 :87、wireKnowledgeJudge 131-144 改造）
- Test: `api/wiring/llm_reranker_test.go`

**Interfaces:**

- Consumes: Task 5 的 `SetSufficiencyJudgeResolver`、`RAGQueryRequest.RerankModel`。
- Produces: `newLLMReranker(completer, timeout, metrics, logger)`（无 model 参数）；`Rerank` 顶部空模型守卫（C1 Critical：`Gateway.resolveChain` 对空模型静默回填默认模型，必须显式拒绝）；`wireSemanticReranker(rag)`（无 ctx）；`llmChatModelInCatalogue`。

- [ ] **Step 1: 写失败测试（空模型守卫）**

`api/wiring/llm_reranker_test.go` 追加：

```go
func TestLLMRerankerRejectsEmptyModel(t *testing.T) {
 r := newLLMReranker(&stubCompleter{}, constants.RerankLLMTimeout, nil, zap.NewNop())
 _, err := r.Rerank(context.Background(), knowledgeport.RerankRequest{
  Query: "q", Documents: []string{"d1", "d2"}, Model: "", TopN: 2,
 })
 if err == nil {
  t.Fatal("empty model must be rejected (fail-open via error), not silently defaulted")
 }
}
```

`stubCompleter` 若不存在则用测试内最小 stub（实现 `llmgatewaydomain.LLMCompleter.Complete` 返回固定 `&llmgatewaydomain.CompletionResponse{Content:`{"scores":[{"index":0,"score":0.9},{"index":1,"score":0.1}]}`}`）——参照该文件既有 completer stub 定义。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./api/wiring/ -run 'TestLLMRerankerRejectsEmptyModel|TestSemanticRerankerDepsGates|TestLLMRankModelInCatalogue'`
Expected: FAIL/编译错误 —— `newLLMReranker` 签名未变、`llmRerankModelInCatalogue` 未重命名。

- [ ] **Step 3: 实现 llm_reranker.go**

(a) struct 删 `model` 字段（24 行）、构造函数删 model 参数：

```go
type llmReranker struct {
 completer llmgatewaydomain.LLMCompleter // Gateway 结构性满足
 timeout   time.Duration                 // 单次调用预算（≤0 回落 RerankLLMTimeout）
 metrics   observability.MetricsProvider // 可为 nil（跳过指标记录）
 logger    *zap.Logger
}

func newLLMReranker(
 completer llmgatewaydomain.LLMCompleter,
 timeout time.Duration,
 metrics observability.MetricsProvider,
 logger *zap.Logger,
) *llmReranker {
 return &llmReranker{completer: completer, timeout: timeout, metrics: metrics, logger: logger}
}
```

(b) `Rerank` 顶部加空模型守卫（`errors` 需要新 import）：

```go
import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "strings"
 "time"
 ...
)
```

```go
func (r *llmReranker) Rerank(ctx context.Context, req knowledgeport.RerankRequest) ([]knowledgeport.RerankResult, error) {
 // 空模型显式拒绝（fail-open 由调用方降级）：Gateway.resolveChain 会对空模型
 // 静默回填 provider 默认模型，不挡在这里则未配置模型的 builtin 会用错模型
 // 重排而非降级（review C1）。
 if req.Model == "" {
  return nil, errors.New("llm rerank: empty model")
 }
 ctx, cancel := context.WithTimeout(ctx, r.rerankTimeout())
 defer cancel()
 ...
```

(c) 68 行 `Model: r.model` → `Model: req.Model`（其余不变）。

- [ ] **Step 4: 实现 knowledge.go wiring 简化**

(a) 调用点 :87 `c.wireSemanticReranker(ctx, rag)` → `c.wireSemanticReranker(rag)`。

(b) `wireSemanticReranker`（150-154）+ `semanticRerankerDeps`（160-185）+ `llmRerankModelInCatalogue`（190-205）整体替换为：

```go
// wireSemanticReranker 在 LLM gateway 可用时注入 builtin-score-v1 的语义重排器；
// 模型在运行期从请求读取（workspace 显式配置），wiring 不做模型目录预检（目录
// 校验在 application 保存路径，运行期失败 fail-open 兜底）。单独成方法以控制
// buildKnowledge 的圈复杂度。
func (c *Container) wireSemanticReranker(rag *knowledge.RAGService) {
 if r, topN := c.semanticRerankerDeps(); r != nil {
  rag.SetSemanticReranker(r, topN)
 }
}

// semanticRerankerDeps 构建 LLM 语义重排器；gateway 不可用返回 (nil, 0)。
// topN/timeout 用常量默认（RerankLLMTopN/RerankLLMTimeout，行为数字禁止内联）。
func (c *Container) semanticRerankerDeps() (knowledgeport.Reranker, int) {
 if c.LLMGateway == nil || c.LLMGateway.Gateway == nil {
  return nil, 0
 }
 var metrics observability.MetricsProvider
 if c.Platform != nil {
  metrics = c.Platform.Metrics // c.Platform 可能为 nil（review H2）
 }
 return newLLMReranker(c.LLMGateway.Gateway, constants.RerankLLMTimeout, metrics, c.Logger), constants.RerankLLMTopN
}

// llmChatModelInCatalogue 检查模型是否在 enabled 的 chat 目录中。目录查询失败或
// registry 缺失按"不在目录"处理（judge fail-closed 放行、告警，不阻断启动）。
func (c *Container) llmChatModelInCatalogue(ctx context.Context, model string) bool {
 if c.LLMGateway == nil || c.LLMGateway.Registry == nil {
  return false
 }
 names, err := c.LLMGateway.Registry.ListChatModelsByTenant(ctx)
 if err != nil {
  c.Logger.Warn("knowledge.judge.catalogue_unavailable", zap.Error(err))
  return false
 }
 for _, n := range names {
  if n == model {
   return true
  }
 }
 return false
}
```

(c) `wireKnowledgeJudge` 改造（移除 Enabled gate、用 `constants.KnowledgeJudgeTimeout`、resolver 内做 chat 目录校验 —— fail-closed）：

```go
// wireKnowledgeJudge 在 LLM gateway 可用时注入证据充分性 judge 解析器；gateway
// 不可用 → 不注入（judgeResolver nil，fail-closed 放行）。模型从请求解析
// （workspace 显式 JudgeModel），resolver 校验模型在 enabled chat 目录后装配
// （目录未知 → 返回 error → 调用方 WARN + 放行）。judge 门开关由 JudgeModel
// 空/非空表达，不再有平台级 KNOWLEDGE_JUDGE_ENABLED。单独成方法以控制
// buildKnowledge 的圈复杂度。
func (c *Container) wireKnowledgeJudge(rag *knowledge.RAGService) {
 if c.LLMGateway == nil || c.LLMGateway.Gateway == nil {
  return
 }
 completer := c.LLMGateway.Gateway
 var metrics observability.MetricsProvider
 if c.Platform != nil {
  metrics = c.Platform.Metrics
 }
 rag.SetSufficiencyJudgeResolver(func(ctx context.Context, model string) (knowledgeport.SufficiencyJudge, error) {
  if !c.llmChatModelInCatalogue(ctx, model) {
   return nil, fmt.Errorf("knowledge judge: model %q not in chat catalogue", model)
  }
  return &knowledgeJudge{completer: completer, model: model, timeout: constants.KnowledgeJudgeTimeout, metrics: metrics}, nil
 })
}
```

`knowledge.go` 需要补 `"fmt"` import（当前无）。

- [ ] **Step 5: 适配 llm_reranker_test.go**

参照该文件既有用例逐处改（`newLLMRerankerStub` 77-79 移除 `"qwen-turbo"` 参数；RerankRequest 构造点 83-85/107-109/121-123/138-140/149-151/162/174/186/207 加 `Model: "qwen-turbo"`；162/206 `newLLMReranker(stub, "m", ...)` → 移除 `"m"`；`TestLLMRankModelInCatalogue` 323-345 → 重命名 `TestLLMChatModelInCatalogue` 并调用 `llmChatModelInCatalogue`）。`TestSemanticRerankerDepsGates`（255-321）整体重写为 gateway 可用性测试：

```go
func TestSemanticRerankerDepsGates(t *testing.T) {
 t.Run("gateway 不可用不注入", func(t *testing.T) {
  c := &Container{LLMGateway: nil}
  if r, topN := c.semanticRerankerDeps(); r != nil || topN != 0 {
   t.Fatalf("semanticRerankerDeps(nil gateway) = (%v, %d), want (nil, 0)", r, topN)
  }
 })
 t.Run("gateway 可用即注入（模型运行期解析）", func(t *testing.T) {
  c := &Container{LLMGateway: &gatewayBundle{Gateway: &stubCompleter{}}, Logger: zap.NewNop()}
  r, topN := c.semanticRerankerDeps()
  if r == nil {
   t.Fatal("semanticRerankerDeps with gateway must inject reranker")
  }
  if topN != constants.RerankLLMTopN {
   t.Fatalf("topN = %d, want %d", topN, constants.RerankLLMTopN)
  }
 })
}
```

`gatewayBundle` 按该测试文件既有类型定义（`llm_reranker_test.go` 的 `newSemanticRerankContainer` 结构）。若测试文件的 `Config` 引用 `KnowledgeRerank`（`newSemanticRerankContainer` 228-253），删除该 `Config` 装配。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./api/wiring/ ./internal/knowledge/...`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add api/wiring/llm_reranker.go api/wiring/llm_reranker_test.go api/wiring/knowledge.go
git commit -m "refactor(knowledge): llm reranker reads model from request, judge checks chat catalogue"
```

---

### Task 7: handler — DTO 双向映射 + PATCH sentinel 编码

**Files:**

- Modify: `api/http/handler/rag_handler.go`（imports 3-16、toDTOConfig 42-58、fromDTOConfig 60-72、UpdateWorkspace 284-326）
- Test: `api/http/handler/rag_handler_test.go`

**Interfaces:**

- Consumes: Task 2 的 3 个字符串 sentinels；`domain.WorkspaceConfig.RerankModel/JudgeModel`。
- Produces: `toDTOConfig`/`fromDTOConfig` 各含 `RerankModel`/`JudgeModel` 映射；`UpdateWorkspace` 用 `io.ReadAll` + `encodeResetSentinels` 把显式 `""` 编码为 sentinel；`sentinelJSON` 用 `json.Marshal` 生成 NUL 转义字面量。

- [ ] **Step 1: 写失败测试**

`api/http/handler/rag_handler_test.go`：

(a) `ragModelExistsStub`（23-30）加 chat case：

```go
type ragModelExistsStub struct{ embedding map[string]bool }

func (m ragModelExistsStub) Exists(_ context.Context, model string, capability knowledgeport.ModelCapability) (bool, error) {
 switch capability {
 case knowledgeport.CapRerank:
  return false, nil
 case knowledgeport.CapChat:
  return model == "qwen-turbo", nil
 default:
  return m.embedding[model], nil
 }
}
```

(b) 追加单测：

```go
func TestUpdateWorkspaceRejectsBuiltinWithoutRerankModel(t *testing.T) {
 ws, err := domain.NewWorkspace("kb", "desc",
  domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}, domain.DefaultChunkSize, domain.DefaultTopK)
 if err != nil {
  t.Fatal(err)
 }
 ws.ID = "wsid-1"
 repo := &stubWorkspaceRepo{ws: ws} // 若既有 stub 类型名不同则沿用既有 stub，仅需 GetByName/UpdateWorkspace 实现
 svc := buildRAGHandlerService(t, repo) // 参照现有 helper，或直接构造 WorkspaceService
 svc.SetModelExists(ragModelExistsStub{embedding: map[string]bool{"text-embedding-v3": true}})
 r := newRouterWithErrorHandler()
 r.PATCH("/knowledge/workspaces/:name", injectRAGTenant("tenant-1"), func(c *gin.Context) {
  NewRAGHandler(nil, svc, zap.NewNop()).UpdateWorkspace(c)
 })

 body := `{"config":{"embedding_model":"text-embedding-v3","reranking":"builtin-score-v1","rerank_model":""}}`
 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodPatch, "/knowledge/workspaces/kb", strings.NewReader(body))
 r.ServeHTTP(w, req)
 if w.Code != http.StatusBadRequest {
  t.Fatalf("status = %d, want 400", w.Code)
 }
}

func TestDTOConfigRoundTripRerankModels(t *testing.T) {
 in := domain.WorkspaceConfig{
  EmbeddingModel: "text-embedding-v3", QueryMode: "hybrid", Reranking: "builtin-score-v1",
  RerankModel: "qwen-turbo", JudgeModel: "qwen-plus",
 }
 got := fromDTOConfig(toDTOConfig(in))
 if got.RerankModel != "qwen-turbo" || got.JudgeModel != "qwen-plus" {
  t.Fatalf("round-trip lost models: RerankModel=%q JudgeModel=%q", got.RerankModel, got.JudgeModel)
 }
}

func TestEncodeResetSentinels(t *testing.T) {
 raw := []byte(`{"reranking":"","rerank_model":"","judge_model":""}`)
 got := string(encodeResetSentinels(raw))
 if !strings.Contains(got, `"reranking":"\u0000rerank_reset"`) ||
  !strings.Contains(got, `"rerank_model":"\u0000rerank_model_reset"`) ||
  !strings.Contains(got, `"judge_model":"\u0000judge_model_reset"`) {
  t.Fatalf("sentinel encoding wrong: %s", got)
 }
 if !json.Valid([]byte(got)) {
  t.Fatalf("encoded body is not valid JSON: %s", got)
 }
}
```

若文件无 `encoding/json`/`strings`/`httptest` import 则补。既有 stub repo 若对 nil 方法 panic，确认 `stubWorkspaceRepo`（或等价）实现 `GetByName`/`UpdateWorkspace`（参照该文件既有 CreateWorkspace 测试如何构造）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./api/http/handler/ -run 'TestUpdateWorkspaceRejectsBuiltinWithoutRerankModel|TestDTOConfigRoundTripRerankModels|TestEncodeResetSentinels'`
Expected: FAIL —— 编译错误：`encodeResetSentinels` 未定义、`gen.WorkspaceConfig` 无 `RerankModel`（若 proto-gen 未跑，先确认 Task 1 已完成）。

- [ ] **Step 3: 实现 rag_handler.go**

(a) imports 追加：

```go
import (
 "bytes"
 "encoding/json"
 "errors"
 "io"
 "net/http"

 "github.com/byteBuilderX/stratum/api/middleware"
 "github.com/gin-gonic/gin"
 "go.uber.org/zap"

 gen "github.com/byteBuilderX/stratum/api/http/dto/gen"
 knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
 "github.com/byteBuilderX/stratum/internal/knowledge/domain"
 skillpkg "github.com/byteBuilderX/stratum/internal/skill/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
)
```

(b) `toDTOConfig` 末尾追加（`RerankTopK` 之后）：

```go
  RerankTopK:   int32(c.RerankTopK),
  RerankModel:  c.RerankModel,
  JudgeModel:   c.JudgeModel,
```

(c) `fromDTOConfig` 末尾追加：

```go
  RerankTopK:   int(c.RerankTopK),
  RerankModel:  c.RerankModel,
  JudgeModel:   c.JudgeModel,
```

(d) `UpdateWorkspace`（296-300）在 `ShouldBindJSON` 之前插入 sentinel 编码：

```go
 var req gen.UpdateWorkspaceRequest
 body, err := io.ReadAll(c.Request.Body)
 if err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 // 显式空字符串字段（"reranking":"" / "rerank_model":"" / "judge_model":""）
 // 是合法"关闭/清空"值，但 MergeUpdate 的 partial 合并以零值=未传，编码为
 // NUL 前缀 sentinel 区分显式清空（与 ScoreThresholdResetSentinel 同构）。
 c.Request.Body = io.NopCloser(bytes.NewReader(encodeResetSentinels(body)))
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
```

(e) 文件末尾追加两个 helper：

```go
// encodeResetSentinels 把 PATCH 请求体中显式空字符串字段编码为重置哨兵。匹配
// 原始 JSON 字节的 `"key":""`（紧凑 JSON，axios 序列化格式），替换为
// sentinelJSON 生成的转义字面量 —— NUL 字节必须转义为 \u0000 才是合法 JSON，
// 不能直接塞裸 NUL 字节（否则 ShouldBindJSON 解析失败）。
func encodeResetSentinels(raw []byte) []byte {
 raw = bytes.ReplaceAll(raw, []byte(`"reranking":""`), []byte(`"reranking":`+sentinelJSON(domain.RerankingResetSentinel)))
 raw = bytes.ReplaceAll(raw, []byte(`"rerank_model":""`), []byte(`"rerank_model":`+sentinelJSON(domain.RerankModelResetSentinel)))
 raw = bytes.ReplaceAll(raw, []byte(`"judge_model":""`), []byte(`"judge_model":`+sentinelJSON(domain.JudgeModelResetSentinel)))
 return raw
}

// sentinelJSON 返回 sentinel 字符串的合法 JSON 字面量（控制字符转义为 \u0000）。
func sentinelJSON(s string) string {
 b, _ := json.Marshal(s)
 return string(b)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./api/http/handler/`
Expected: PASS（含既有用例）。

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/rag_handler.go api/http/handler/rag_handler_test.go
git commit -m "feat(knowledge): DTO mapping and PATCH reset sentinels for rerank/judge models"
```

---

### Task 8: middleware 错误映射 + config 清理

**Files:**

- Modify: `api/middleware/error_mapping.go`（180-181 附近追加 3 条）
- Modify: `config/config.go`（63-65 删字段、81-85 删 RerankLLMConfigured、127-141 删结构体、223-236 删 bindings）
- Modify: `config/config_test.go`（删 TestLoadKnowledgeRerankConfig 208-247）

**Interfaces:**

- Consumes: Task 2 的 3 个 domain errors（`knowledgedomain.*`）。
- Produces: 3 个错误在 `MapErrorToStatus` 映射为 400；平台级 `KNOWLEDGE_JUDGE_*`/`KNOWLEDGE_RERANK_*` env 与结构体彻底移除。

- [ ] **Step 1: 写失败测试**

`api/middleware/error_mapping_test.go`（若存在；否则在既有错误映射测试内追加）验证映射：

```go
func TestMapKnowledgeRerankModelErrors(t *testing.T) {
 cases := []struct {
  err      error
  wantCode int
 }{
  {knowledgedomain.ErrRerankModelRequired, http.StatusBadRequest},
  {knowledgedomain.ErrInvalidRerankModel, http.StatusBadRequest},
  {knowledgedomain.ErrInvalidJudgeModel, http.StatusBadRequest},
 }
 for _, tc := range cases {
  if got := MapErrorToStatus(tc.err); got != tc.wantCode {
   t.Errorf("MapErrorToStatus(%v) = %d, want %d", tc.err, got, tc.wantCode)
  }
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./api/middleware/ -run 'TestMapKnowledgeRerankModelErrors'`
Expected: FAIL —— 未映射，返回 500。

- [ ] **Step 3: 实现 error_mapping.go**

在 `knowledgedomain.ErrInvalidScoreThreshold`（188）前后追加：

```go
 knowledgedomain.ErrRerankModelRequired:      http.StatusBadRequest,
 knowledgedomain.ErrInvalidRerankModel:       http.StatusBadRequest,
 knowledgedomain.ErrInvalidJudgeModel:        http.StatusBadRequest,
```

（放在 embedding 相关条目（180-181）附近，与 `ErrInvalidScoreThreshold` 一起。）

- [ ] **Step 4: 清理 config.go**

(a) 删除 `Config` struct（63-65）中 `KnowledgeJudge KnowledgeJudgeConfig` / `KnowledgeRerank KnowledgeRerankConfig` 两行（保留 `AgentFactCheck`）。
(b) 删除 `RerankLLMConfigured`（81-85，保留 `RerankConfigured`）。
(c) 删除 `KnowledgeJudgeConfig` 结构体（130-134）与 `KnowledgeRerankConfig` 结构体（136-141）。
(d) 删除 `Load()` 中 bindings（223-230 KnowledgeJudge、231-236 KnowledgeRerank）。
(e) **不删 `constants` import**：删除 bindings 后 196/203/209-215/220-221 行仍使用 `constants`（AgentFactCheck TopK/MaxClaims、其他块）。

`config/config_test.go`：删除整个 `TestLoadKnowledgeRerankConfig` 函数（208-247，含全部 `KNOWLEDGE_RERANK_*`/`RerankLLMConfigured` 断言）。

- [ ] **Step 5: 全仓编译 + 测试**

Run: `go build ./... && go test ./config/ ./api/middleware/`
Expected: PASS。若 `go build ./...` 报 `c.Config.KnowledgeJudge` / `c.Config.KnowledgeRerank` / `RerankLLMConfigured` 未定义残留引用，说明 Task 5/6 有遗漏，回到对应任务补。

- [ ] **Step 6: Commit**

```bash
git add api/middleware/error_mapping.go api/middleware/error_mapping_test.go config/config.go config/config_test.go
git commit -m "feat(knowledge): map rerank/judge model errors to 400, drop platform rerank env config"
```

---

### Task 9: 迁移 SQL 脚本 + 部署时序

**Files:**

- Create: `scripts/knowledge-rerank-workspaces-migration.sh`
- Modify: `docs/superpowers/specs/2026-08-22-llm-rerank-design.md`（§6 更新为最终 SQL 与时序）或 `docs/agent/migration-tenant.md` 追加备注

**Interfaces:**

- Consumes: 无代码依赖；交付运维动作。
- Produces: 幂等迁移脚本（逐 `tenant_<id>` schema 更新 JSONB），部署时序文档。

- [ ] **Step 1: 写迁移脚本**

`scripts/knowledge-rerank-workspaces-migration.sh`（需要 `psql` + 连接信息，通过既有 `DATABASE_URL` 约定或环境变量注入）：

```bash
#!/usr/bin/env bash
# 迁移存量 workspace：builtin-score-v1 且缺 rerank_model 的配置，将 reranking 置空，
# 使 #4.2 校验（builtin 空模型保存拒绝）上线后不受影响。
# rag_workspaces 是 tenant-only 表（tenant_schema.sql），编号迁移只操作 public
# schema，故按 public.tenants 枚举逐 tenant_<id> schema 幂等执行。
# 用法：DATABASE_URL=postgres://... bash scripts/knowledge-rerank-workspaces-migration.sh
set -euo pipefail

: "${DATABASE_URL:?DATABASE_URL is required}"

enabled=0
affected=0
for schema in $(psql "$DATABASE_URL" -tAc "SELECT 'tenant_'||id FROM public.tenants WHERE deleted_at IS NULL"); do
  n=$(psql "$DATABASE_URL" -tAc "UPDATE ${schema}.rag_workspaces SET config = config || '{\"reranking\":\"\"}' WHERE config->>'reranking' = 'builtin-score-v1' AND (config->>'rerank_model' IS NULL OR config->>'rerank_model' = '')")
  if [ -n "$n" ] && [ "$n" -gt 0 ]; then
    affected=$((affected + n))
    enabled=$((enabled + 1))
  fi
done
echo "migration done: ${affected} workspace(s) reranking cleared across ${enabled} tenant schema(s)"
```

- [ ] **Step 2: 幂等性自检**

Run: `bash -n scripts/knowledge-rerank-workspaces-migration.sh`（语法检查）。
Expected: 无输出（语法 OK）。脚本幂等：重复执行第二次 `WHERE` 谓词不再命中（config->>'reranking' 已为空）。

- [ ] **Step 3: 部署时序文档**

在 spec §6 追加（或 `docs/agent/migration-tenant.md` 备注）最终时序：

1. 先执行迁移脚本（运维动作，**须获用户许可**，远端生产写入遵守项目规则）；
2. 再部署代码（CD 流水线）；
3. 顺序不可反：校验上线后受影响 workspace 的保存会被拒，存在保存拒绝窗口。

- [ ] **Step 4: Commit**

```bash
git add scripts/knowledge-rerank-workspaces-migration.sh docs/superpowers/specs/2026-08-22-llm-rerank-design.md
git commit -m "chore(knowledge): idempotent per-tenant migration script for builtin rerank model config"
```

---

### Task 10: 前端 — 配置表单 + 保存链路

**Files:**

- Modify: `web/src/modules/knowledge/model/knowledge.ts`（workspaceConfigSchema 3-15）
- Modify: `web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`
- Modify: `web/src/modules/knowledge/hooks/useKnowledgeDetailPage.ts`
- Modify: `web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx`（:67）
- Test: `web/src/modules/knowledge/hooks/useKnowledgeDetailPage.test.tsx`、`web/src/modules/knowledge/components/__tests__/WorkspaceConfigForm.test.tsx`

**Interfaces:**

- Consumes: `llmApi.getCatalogue()` → `{chatModels, embeddingModels}`（`web/src/modules/llm/api/llm.api.ts:39-45`）。
- Produces: `WorkspaceConfigForm` 新增 `chatModels: string[]` prop；detail hook 提供 `chatModels`；保存 payload 含 `rerank_model`/`judge_model`（**judge_model 清空必须发 `""`**，否则 allowClear 置 undefined → JSON 丢 key → 后端 partial 保留旧值 → 判断门关不掉）。

- [ ] **Step 1: 写失败测试**

(a) `web/src/modules/knowledge/hooks/useKnowledgeDetailPage.test.tsx` —— mock llm.api（否则 hook 真实调用会 fetch 网络挂测试）：

```ts
vi.mock('@/modules/llm/api/llm.api', () => ({
  llmApi: {
    getCatalogue: vi.fn().mockResolvedValue({ chatModels: ['qwen-turbo'], embeddingModels: ['text-embedding-v3'] }),
  },
}));
```

(b) `web/src/modules/knowledge/components/__tests__/WorkspaceConfigForm.test.tsx` —— Harness 补 `chatModels` prop：

```tsx
const Harness = ({ initialValues = {} }: { initialValues?: Record<string, unknown> }) => {
  const [form] = Form.useForm();
  return (
    <Form form={form} initialValues={initialValues}>
      <WorkspaceConfigForm form={form} loading={false} chatModels={[]} onSubmit={vi.fn()} />
    </Form>
  );
};
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd web && npx vitest run src/modules/knowledge/hooks/useKnowledgeDetailPage.test.tsx src/modules/knowledge/components/__tests__/WorkspaceConfigForm.test.tsx`
Expected: FAIL —— 类型错误（chatModels prop 缺失）/ hook 引入 llmApi 后 mock 未覆盖导致 fetch。

- [ ] **Step 3: 实现前端**

(a) `web/src/modules/knowledge/model/knowledge.ts` —— `workspaceConfigSchema` 追加：

```ts
    reranking: z.string().optional(),
    rerank_model: z.string().optional(),
    judge_model: z.string().optional(),
    score_threshold: z.number().optional(),
```

(b) `web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`：

- `ConfigValues` 接口追加 `rerank_model?: string; judge_model?: string;`
- props 追加 `chatModels: string[];` 并解构
- 加 `const reranking = Form.useWatch('reranking', form);`
- 重排策略 tooltip 改为（review I2，与"未配置模型无法保存"一致）：

```tsx
<Form.Item label="重排策略" name="reranking" tooltip="内置重排需在模型管理中配置重排模型；未配置模型时无法保存。外部重排需在模型管理中配置">
```

- `rerank_top_k` Form.Item 之后追加 judge_model 与条件 rerank_model（reranking Select 保持"关闭"占位）：

```tsx
        {reranking === 'builtin-score-v1' && (
          <Form.Item
            label="重排模型"
            name="rerank_model"
            preserve={false}
            rules={[{ required: true, message: '内置重排必须选择重排模型' }]}
            tooltip="内置重排的 LLM 语义精排模型（chat 目录）；切换重排策略即关闭"
          >
            <Select
              style={{ width: '100%', maxWidth: 160 }}
              placeholder="选择重排模型"
              allowClear
              options={chatModels.map((m) => ({ label: m, value: m }))}
            />
          </Form.Item>
        )}
        <Form.Item label="判断模型" name="judge_model" tooltip="证据充分性判断模型（chat 目录）；清空即关闭判断门">
          <Select
            style={{ width: '100%', maxWidth: 160 }}
            placeholder="选择判断模型"
            allowClear
            options={chatModels.map((m) => ({ label: m, value: m }))}
          />
        </Form.Item>
```

`preserve={false}`（review M2）：reranking 切离 builtin 时卸载 Field、值移出 form store，payload 不再带 stale rerank_model（后端 dormant 保留）。

(c) `web/src/modules/knowledge/hooks/useKnowledgeDetailPage.ts`：

- import `llmApi from '@/modules/llm/api/llm.api'`
- `ConfigValues` 追加 `rerank_model?: string; judge_model?: string;`
- 加 state 与加载 effect：

```ts
  const [chatModels, setChatModels] = useState<string[]>([]);

  // 模型下拉数据源：chat 目录（复用模型管理 API）。
  useEffect(() => {
    let cancelled = false;
    llmApi
      .getCatalogue()
      .then((catalogue) => {
        if (cancelled) return;
        setChatModels(catalogue.chatModels ?? []);
      })
      .catch(() => {
        if (!cancelled) setChatModels([]);
      });
    return () => {
      cancelled = true;
    };
  }, []);
```

- `fetchStats` values（112-120）追加：

```ts
        rerank_model: data.config?.rerank_model,
        judge_model: data.config?.judge_model,
```

- `handleConfigSave` payload（203-213）追加（**judge_model 清空必须发 `""`** —— 前端 allowClear 置 undefined 会被 JSON 丢弃，后端 partial 保留旧值关不掉；rerank_model 仅 builtin 时随 Field 存在，否则 undefined → 省略 → dormant）：

```ts
        await knowledgeApi.update(name, {
          config: {
            embedding_model: stats?.config?.embedding_model,
            chunk_size: values.chunk_size,
            chunk_overlap: values.chunk_overlap,
            query_mode: values.query_mode,
            top_k: values.top_k,
            reranking: values.reranking,
            score_threshold: values.score_threshold,
            rerank_top_k: values.rerank_top_k,
            rerank_model: values.rerank_model,
            judge_model: values.judge_model ?? '',
          },
        });
```

- return 对象加 `chatModels`

(d) `web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx` —— 解构加 `chatModels`，:67 传 prop：

```tsx
        <WorkspaceConfigForm form={configForm} loading={configLoading} chatModels={chatModels} onSubmit={handleConfigSave} />
```

- [ ] **Step 4: 运行测试 + lint + build 确认通过**

Run: `cd web && npx vitest run src/modules/knowledge/hooks/useKnowledgeDetailPage.test.tsx src/modules/knowledge/components/__tests__/WorkspaceConfigForm.test.tsx && npm run lint && npm run build`
Expected: 全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/knowledge/model/knowledge.ts web/src/modules/knowledge/components/WorkspaceConfigForm.tsx web/src/modules/knowledge/hooks/useKnowledgeDetailPage.ts web/src/modules/knowledge/pages/KnowledgeDetailPage.tsx web/src/modules/knowledge/hooks/useKnowledgeDetailPage.test.tsx web/src/modules/knowledge/components/__tests__/WorkspaceConfigForm.test.tsx
git commit -m "feat(web): workspace rerank/judge model config with chat catalogue dropdown"
```

---

### Task 11: 历史计划同步 + 全局回归

**Files:**

- Modify: `docs/superpowers/plans/2026-08-22-llm-rerank.md`（引用 6 个 `KNOWLEDGE_RERANK_*`/`KNOWLEDGE_JUDGE_*` env 与 `KnowledgeRerankConfig` 的段落 → 迁移说明或归档标注）

**Interfaces:**

- Consumes: Task 1-10 全部产物。

- [ ] **Step 1: 同步历史计划文档**

`docs/superpowers/plans/2026-08-22-llm-rerank.md` 顶部加过期标注，并把 6 个 env 变量引用改为指向新 workspace 配置方案：

> **⚠️ 已过期（2026-08-22 迁移）**：本文档描述的平台级 `KNOWLEDGE_JUDGE_*` / `KNOWLEDGE_RERANK_*` env 配置已被 workspace 显式配置（`rerank_model`/`judge_model`）取代，详见 `docs/superpowers/specs/2026-08-22-llm-rerank-design.md` 与 `docs/superpowers/plans/2026-08-22-llm-rerank-workspace-config.md`。

- [ ] **Step 2: 全局回归**

Run:

```bash
go vet ./...
go test -v -race -timeout 30s ./...
cd web && npm run lint && npm run build
make code-quality
make risk-guardrails
```

Expected: 全绿。`make proto-gen` 产物不提交（不入 git）。

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/plans/2026-08-22-llm-rerank.md
git commit -m "docs(knowledge): mark superseded platform rerank env plan"
```

---

## Self-Review

**1. Spec coverage（spec §1 决策 × Global Constraints 映射）：**

- 决策 1（两模型显式入 workspace，无 env/兜底）→ T1/T2/T3/T5/T6/T8/T10 ✓
- 决策 2（builtin 空模型保存显式拒绝）→ T2（Validate）+ T4（builtin-first 覆盖 PATCH）+ T7（PATCH 400 测试）✓
- 决策 3（rerank fail-open / judge fail-closed）→ T5（judgeSufficiencyGate 短路 + WARN + 放行）、T6（空模型守卫 → fail-open error）、C1 空模型守卫 ✓
- 决策 4（top-N 精排）→ 既有 `rerankSemantic` `topN := min(rs.semanticTopN, len(pool))` 保留，T5 仅改模型来源 ✓
- 决策 5（模型来源唯一，CapChat 校验）→ T4（CapChat + 目录校验 + 传播/400 分离）✓
- 补充分配：`RerankConfigured` 保留（T8 只删 `RerankLLMConfigured`）；`AgentFactCheck` 不动；evaluation 快照留独立任务；golden 无需变更（均 401）✓

**2. Placeholder scan：** 每个任务均有完整代码块；Task 9 是运维脚本 + 文档（无遗留 TBD）。rag_service_rerank_test.go / llm_reranker_test.go / config_test.go 的适配以既有文件为基底，给出精确行号与替换目标（测试文件本体需在实现时对照读取，属 TDD 正常工序）。

**3. Type consistency：**

- `RerankRequest.Model`（port）→ `llmReranker.Rerank` 空模型守卫 → `req.Model`：T5 填 `req.RerankModel`、T6 守卫读 `req.Model`，一致 ✓
- `SufficiencyJudgeResolver func(ctx, model string) (port.SufficiencyJudge, error)`：T5 定义 + `SetSufficiencyJudgeResolver`，T5/T6 wiring 闭包签名一致 ✓
- 3 个 sentinel 常量名/值在 T2（定义）、T7（handler 编码）、T2 测试引用一致 ✓
- `resolvedWorkspace` 字段名在 T5 定义、searchWorkspace/searchWorkspaceWithEvidence 解构一致 ✓
- `CapChat ModelCapability = "chat"`：T4 定义，`fakeModelExists`/adapter/workspace_service 校验三处一致 ✓

**4. 任务依赖序：** T1（proto-gen）→ T2（domain）→ T3（persistence）→ T4（application 校验）→ T5（请求传递 + resolver）→ T6（wiring）→ T7（handler）→ T8（config 清理，依赖 T5/T6 先移除引用）→ T9（运维脚本，独立）→ T10（前端，依赖 T1 契约 + T7 DTO）→ T11（回归）。每任务编译/测试独立可验证。
