# LLM 语义重排（builtin-score-v1）实现计划

> **⚠️ 已过期（2026-08-22 迁移）**：本文档描述的平台级 `KNOWLEDGE_JUDGE_*` / `KNOWLEDGE_RERANK_*` env 配置已被 workspace 显式配置（`rerank_model`/`judge_model`）取代，详见 `docs/superpowers/specs/2026-08-22-llm-rerank-design.md` 与 `docs/superpowers/plans/2026-08-22-llm-rerank-workspace-config.md`。
>
> **迁移映射（2026-08-22）**：本计划中的平台级 env 模型配置已全部删除，替换为 workspace config JSONB 显式字段（无 env、无兜底）：`KNOWLEDGE_RERANK_MODEL` → `rerank_model`、`KNOWLEDGE_RERANK_TIMEOUT_SECONDS` → 常量 `RerankLLMTimeout`、`KNOWLEDGE_RERANK_TOPN` → 常量 `RerankLLMTopN`、`KNOWLEDGE_JUDGE_ENABLED` → 空/非空 `judge_model` 表达开关、`KNOWLEDGE_JUDGE_MODEL` → `judge_model`、`KNOWLEDGE_JUDGE_TIMEOUT_SECONDS` → 常量 `KnowledgeJudgeTimeout`。下方正文保留为 v1 平台级 env 实现的历史记录，不再与当前代码一致。
>
> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 RAG 内置重排策略 `builtin-score-v1` 从确定性 no-op 改造为 LLM 语义重排：复用平台 LLM 网关对召回池前 `TopN` 条 listwise 打分并覆盖 Score，未配置/失败/超时时 fail-open 降级为召回分数排序，检索永不因重排失败。

**Architecture:** 新增 wiring 组合根的 `llmReranker`（与 `knowledgeJudge` 同先例：`api/wiring/` 下，复用 `LLMCompleter.Complete` + `json_object` + `Temperature=0`），通过 `RAGService.SetSemanticReranker` 注入；`rerankSources` 的 builtin 分支改为"装配则精排、否则纯排序"。平台级配置 `KNOWLEDGE_RERANK_MODEL/_TIMEOUT_SECONDS/_TOPN`，启动期校验模型在 chat 目录。

> **已迁移（2026-08-22）**：本段所述平台级 `KNOWLEDGE_RERANK_MODEL/_TIMEOUT_SECONDS/_TOPN` 配置已删除，模型改为 workspace 显式 `rerank_model`（timeout/topN 用常量 `RerankLLMTimeout`/`RerankLLMTopN`），见 `docs/superpowers/plans/2026-08-22-llm-rerank-workspace-config.md`。

**Tech Stack:** Go 1.25（stdlib + zap + 既有 llmgateway/knowledge wiring）、React 18 + Ant Design（仅改 tooltip 文案）。

## Global Constraints

- 后端 Go 以 `go.mod` 为准（1.25.12）；行宽 ≤120；import 分组 stdlib → third-party → internal。
- 行为数字禁止内联：新常量放 `pkg/constants/knowledge.go` 第二个 const 块（`KnowledgeJudgeMaxEvidenceRunes` 之后）。
- 圈复杂度 ≤10、认知 ≤15、函数 ≤120 行、嵌套 ≤4；early return 消灭 else。
- 错误逐层 `fmt.Errorf("operation: %w", err)`；error 是最后返回值。
- **fail-open**：语义重排失败/超时/解析失败 → WARN + `IncRerankRequest(..., "builtin-llm", "degraded")` → 池不变按召回分排序；检索永不失败。
- **日志红线**：降级/失败日志只记 model/topN/pool_size/error，禁止记录 query/chunk/response 原文。
- 指标标签固定 `"builtin-llm"`；tenant 一律 `reqctx.TenantIDFromContext(ctx)`（与 Cohere 一致）。
- 前端用户可见字符串中文；tooltip 经 `Form.Item` 的 `tooltip` 属性。
- 分支：当前 worktree 分支 `feat/rag-retrieval-check`，禁止在 `main` 直接提交。每次 commit 信息末尾加 `Co-Authored-By: Claude <noreply@anthropic.com>`。
- 每 Task 按 TDD：先写 failing test → 运行确认 FAIL → 最小实现 → 运行确认 PASS → commit。

---

### Task 1: 常量 + 配置

**Files:**

- Modify: `pkg/constants/knowledge.go:87`（第二个 const 块末尾，`KnowledgeJudgeMaxEvidenceRunes` 后）
- Modify: `config/config.go:78`（`RerankConfigured` 后加 `RerankLLMConfigured`）、`config/config.go:127`（`KnowledgeJudgeConfig` 后加 `KnowledgeRerankConfig`）、`config/config.go:204-211`（`KnowledgeJudge` 装配后加 `KnowledgeRerank` 装配）
- Test: `config/config_test.go`（新增 `TestLoadKnowledgeRerankConfig`）

> **已迁移（2026-08-22）**：本 Task 的 `KnowledgeRerankConfig` 结构与 `RerankLLMConfigured()` 方法已在 workspace 显式配置迁移中删除（`config/config.go` 不再有 `Config.KnowledgeRerank` / `KnowledgeJudgeConfig`）；仅 `constants.RerankLLM*` 行为常量保留，模型配置走 workspace `rerank_model`/`judge_model`。

**Interfaces:**

- Produces: `config.Config.KnowledgeRerank`（`config.KnowledgeRerankConfig{Model string; Timeout time.Duration; TopN int}`）、`(*Config).RerankLLMConfigured() bool`、`constants.RerankLLMTopN = 10`、`constants.RerankLLMMaxTokens = 1024`、`constants.RerankLLMTimeout = 5 * time.Second`、`constants.RerankLLMMaxDocRunes = 500`。后续 Task 2/4 依赖 `RerankLLMTimeout`/`RerankLLMTopN`/`RerankLLMMaxTokens`/`RerankLLMMaxDocRunes` 与 `Config.KnowledgeRerank`。

> **已迁移（2026-08-22）**：`Config.KnowledgeRerank`/`KnowledgeRerankConfig`/`RerankLLMConfigured` 均已在 workspace 显式配置迁移中删除；`RerankLLM*` 行为常量不变。模型来源见 `docs/superpowers/specs/2026-08-22-llm-rerank-design.md` §4.1/§4.2。

- [ ] **Step 1: 新增 4 个常量**

在 `pkg/constants/knowledge.go` 第二个 const 块末尾（`KnowledgeJudgeMaxEvidenceRunes = 4000` 行后，第 87 行的 `)` 前）追加：

```go
 // RerankLLMTopN 是 builtin-score-v1 LLM 语义重排的精排候选上限（默认）：
 // 先按召回分数取前 N 条再 listwise 打分，池更小则全量精排。
 // 须 ≥ 常用 TopK（默认 5）；配置值 < 工作区 TopK 时最终条数受此硬上限约束。
 RerankLLMTopN = 10
 // RerankLLMMaxTokens caps 语义重排输出（固定 JSON 结构，1024 充足）。
 RerankLLMMaxTokens = 1024
 // RerankLLMTimeout bounds 单次 LLM 重排调用；超时按 fail-open 降级为分数排序。
 RerankLLMTimeout = 5 * time.Second
 // RerankLLMMaxDocRunes 截断喂给重排模型的候选正文（token 成本控制；只影响
 // 相关性打分上下文，不影响检索正文）。
 RerankLLMMaxDocRunes = 500
```

- [ ] **Step 2: 写 failing config 测试**

> **已迁移（2026-08-22）**：下述 `TestLoadKnowledgeRerankConfig` 用例及其 `KNOWLEDGE_RERANK_*` env 断言已在 workspace 显式配置迁移中删除（config 层不再加载这些 env）。

在 `config/config_test.go` 末尾追加（import 块需加 `"github.com/byteBuilderX/stratum/pkg/constants"`，其余 `os/strings/testing/time` 已有）：

```go
func TestLoadKnowledgeRerankConfig(t *testing.T) {
 t.Setenv("KNOWLEDGE_RERANK_MODEL", "")
 t.Setenv("KNOWLEDGE_RERANK_TIMEOUT_SECONDS", "")
 t.Setenv("KNOWLEDGE_RERANK_TOPN", "")

 cfg, err := Load()
 if err != nil {
  t.Fatalf("Load() failed: %v", err)
 }
 if cfg.KnowledgeRerank.Model != "" {
  t.Fatalf("model default must be empty, got %q", cfg.KnowledgeRerank.Model)
 }
 if cfg.RerankLLMConfigured() {
  t.Fatal("empty model must not report configured")
 }
 if cfg.KnowledgeRerank.Timeout != constants.RerankLLMTimeout {
  t.Fatalf("timeout default = %v, want %v", cfg.KnowledgeRerank.Timeout, constants.RerankLLMTimeout)
 }
 if cfg.KnowledgeRerank.TopN != constants.RerankLLMTopN {
  t.Fatalf("topN default = %d, want %d", cfg.KnowledgeRerank.TopN, constants.RerankLLMTopN)
 }

 t.Setenv("KNOWLEDGE_RERANK_MODEL", "qwen-turbo")
 t.Setenv("KNOWLEDGE_RERANK_TIMEOUT_SECONDS", "7")
 t.Setenv("KNOWLEDGE_RERANK_TOPN", "3")

 cfg, err = Load()
 if err != nil {
  t.Fatalf("Load() failed: %v", err)
 }
 if !cfg.RerankLLMConfigured() || cfg.KnowledgeRerank.Model != "qwen-turbo" {
  t.Fatalf("explicit model not applied: %+v", cfg.KnowledgeRerank)
 }
 if cfg.KnowledgeRerank.Timeout != 7*time.Second {
  t.Fatalf("timeout = %v, want 7s", cfg.KnowledgeRerank.Timeout)
 }
 if cfg.KnowledgeRerank.TopN != 3 {
  t.Fatalf("topN = %d, want 3", cfg.KnowledgeRerank.TopN)
 }
}
```

- [ ] **Step 3: 运行测试确认 FAIL**

Run: `go test ./config/ -run TestLoadKnowledgeRerankConfig -v`
Expected: FAIL — `cfg.KnowledgeRerank` 不存在编译错误（`Config` 结构无该字段）。

- [ ] **Step 4: 实现配置**

`config/config.go` 三处修改：

(a) `RerankConfigured()`（第 76-78 行）之后追加：

```go
// RerankLLMConfigured reports whether the builtin semantic rerank backend is
// available. The model is the single switch: an empty model disables it.
func (c *Config) RerankLLMConfigured() bool {
 return c.KnowledgeRerank.Model != ""
}
```

(b) `KnowledgeJudgeConfig`（第 123-127 行）之后追加：

```go
// KnowledgeRerankConfig 控制 builtin-score-v1 的 LLM 语义重排（平台级，全租户
// 统一）。默认关闭（fail-open）：Model 未配置/重排器未装配/调用失败全部降级
// 为召回分数排序，检索行为与不配置时完全一致。
type KnowledgeRerankConfig struct {
 // Model 是语义重排模型（必须存在于 chat 目录，wiring 启动时校验；
 // 空 = 关闭语义重排）。
 Model   string
 Timeout time.Duration
 // TopN 是精排候选上限（≤0 回落 RerankLLMTopN）。
 TopN int
}
```

(c) `Config` 结构体（第 64 行 `KnowledgeJudge` 后）加字段 `KnowledgeRerank KnowledgeRerankConfig`：

```go
 KnowledgeJudge          KnowledgeJudgeConfig
 KnowledgeRerank         KnowledgeRerankConfig
```

(d) `Load()` 的 cfg 字面量中 `KnowledgeJudge:`（第 204-211 行）之后追加：

```go
  KnowledgeRerank: KnowledgeRerankConfig{
   Model:   getEnv("KNOWLEDGE_RERANK_MODEL", ""),
   Timeout: time.Duration(getEnvInt("KNOWLEDGE_RERANK_TIMEOUT_SECONDS",
    int(constants.RerankLLMTimeout.Seconds()))) * time.Second,
   TopN: getEnvInt("KNOWLEDGE_RERANK_TOPN", constants.RerankLLMTopN),
  },
```

> **已迁移（2026-08-22）**：以上 `getEnv("KNOWLEDGE_RERANK_*")` 平台级 env 绑定已全部删除；模型改为 workspace 显式 `rerank_model`，timeout/topN 用常量，不再有平台级 env。

- [ ] **Step 5: 运行测试确认 PASS**

Run: `go test ./config/ -run TestLoadKnowledgeRerankConfig -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add pkg/constants/knowledge.go config/config.go config/config_test.go
git commit -m "feat(knowledge): add llm rerank config

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: `llmReranker` 组件（`api/wiring/llm_reranker.go`）

**Files:**

- Create: `api/wiring/llm_reranker.go`
- Test: `api/wiring/llm_reranker_test.go`（新增；同时含 Task 4 的 wiring 注入测试，见该 Task）

**Interfaces:**

- Consumes: `llmgatewaydomain.LLMCompleter`（`Complete(ctx, *CompletionRequest) (*CompletionResponse, error)`）、`knowledgeport.Reranker`、`knowledgeport.RerankRequest{Query, Documents []string, Model string, TopN int}`、`knowledgeport.RerankResult{Index int, Score float32}`、`observability.MetricsProvider`（`IncRerankRequest(tenantID, model, status)`、`RecordRerankDuration(model, seconds)`）、`reqctx.TenantIDFromContext`、`constants.RerankLLM*`、wiring 包已有 `truncateRunes(value string, limit int) string`（`api/wiring/workflow.go:86`）。
- Produces: `*llmReranker`（实现 `knowledgeport.Reranker`）+ `newLLMReranker(completer, model, timeout, metrics, logger) *llmReranker`。Task 4 的 `semanticRerankerDeps` 依赖二者；`llmReranker` 内部方法 `rerankTimeout() time.Duration`、`record(ctx, status)` 供测试直接断言。

- [ ] **Step 1: 写 failing 组件测试**

创建 `api/wiring/llm_reranker_test.go`（完整文件，import 含 `context/errors/strings/testing/time` + `knowledgeport` + `llmgatewaydomain` + `pkg/constants` + `pkg/observability` + `pkg/reqctx` + `go.uber.org/zap`）：

```go
package wiring

import (
 "context"
 "errors"
 "strings"
 "testing"
 "time"

 knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
 llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "github.com/byteBuilderX/stratum/pkg/reqctx"
 "go.uber.org/zap"
)

// llmRerankerCompleterStub captures the completion request for assertions.
type llmRerankerCompleterStub struct {
 model    string
 messages []llmgatewaydomain.Message
 temp     *float64
 maxTok   int
 format   *llmgatewaydomain.ResponseFormat
 content  string
 err      error
}

func (s *llmRerankerCompleterStub) Complete(_ context.Context, req *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
 s.model = req.Model
 s.messages = req.Messages
 s.temp = req.Temperature
 s.maxTok = req.MaxTokens
 s.format = req.ResponseFormat
 if s.err != nil {
  return nil, s.err
 }
 return &llmgatewaydomain.CompletionResponse{Content: s.content}, nil
}

func (s *llmRerankerCompleterStub) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
 return nil, nil
}

// blockingCompleter blocks until ctx cancellation (timeout propagation test).
type blockingCompleter struct{}

func (blockingCompleter) Complete(ctx context.Context, _ *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
 <-ctx.Done()
 return nil, ctx.Err()
}

func (blockingCompleter) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
 return nil, nil
}

// rerankMetricRecorder records the two rerank metrics alongside NoopMetrics so
// the fixed "builtin-llm" label is asserted.
type rerankMetricRecorder struct {
 observability.NoopMetrics
 inc []string // tenant:model:status
 dur []float64
}

func (m *rerankMetricRecorder) IncRerankRequest(tenantID, model, status string) {
 m.inc = append(m.inc, tenantID+":"+model+":"+status)
}

func (m *rerankMetricRecorder) RecordRerankDuration(model string, seconds float64) {
 m.dur = append(m.dur, seconds)
}

func newLLMRerankerStub(stub *llmRerankerCompleterStub, metrics observability.MetricsProvider) *llmReranker {
 return newLLMReranker(stub, "qwen-turbo", constants.RerankLLMTimeout, metrics, zap.NewNop())
}

func TestLLMRerankerParsesScoresAndConfiguresDeterministicCall(t *testing.T) {
 stub := &llmRerankerCompleterStub{content: `{"scores":[{"index":1,"score":0.9},{"index":0,"score":0.4}]}`}
 got, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
  Query: "q", Documents: []string{"a", "b"}, TopN: 2,
 })
 if err != nil {
  t.Fatal(err)
 }
 if len(got) != 2 || got[0].Index != 1 || got[0].Score != 0.9 || got[1].Index != 0 {
  t.Fatalf("results=%+v", got)
 }
 if stub.model != "qwen-turbo" || stub.temp == nil || *stub.temp != 0 {
  t.Fatalf("model=%q temp=%v want deterministic 0", stub.model, stub.temp)
 }
 if stub.maxTok != constants.RerankLLMMaxTokens || stub.format == nil || stub.format.Type != "json_object" {
  t.Fatalf("maxTokens=%d format=%+v", stub.maxTok, stub.format)
 }
 user := stub.messages[1].Content
 if !strings.Contains(user, "Query:\nq") || !strings.Contains(user, "0. a") {
  t.Fatalf("prompt must carry query and numbered candidates, got %q", user)
 }
}

func TestLLMRerankerTruncatesCandidates(t *testing.T) {
 long := strings.Repeat("长", constants.RerankLLMMaxDocRunes*2)
 stub := &llmRerankerCompleterStub{content: `{"scores":[]}`}
 if _, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
  Query: "q", Documents: []string{long}, TopN: 1,
 }); err != nil {
  t.Fatal(err)
 }
 user := stub.messages[1].Content
 candidate := user[strings.Index(user, "0. ")+3 : strings.Index(user, "\n\n输出 JSON")]
 if n := len([]rune(candidate)); n != constants.RerankLLMMaxDocRunes {
  t.Fatalf("candidate truncated to %d runes, want %d", n, constants.RerankLLMMaxDocRunes)
 }
}

func TestLLMRerankerDedupsAndSkipsInvalidIndex(t *testing.T) {
 stub := &llmRerankerCompleterStub{content: `{"scores":[{"index":0,"score":0.9},{"index":0,"score":0.1},{"index":99,"score":0.8},{"index":-1,"score":0.7}]}`}
 got, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
  Query: "q", Documents: []string{"a", "b"}, TopN: 2,
 })
 if err != nil {
  t.Fatal(err)
 }
 if len(got) != 1 || got[0].Index != 0 || got[0].Score != 0.9 {
  t.Fatalf("duplicate must keep first occurrence, invalid indexes skipped: %+v", got)
 }
}

func TestLLMRerankerSurfacesErrors(t *testing.T) {
 for name, stub := range map[string]*llmRerankerCompleterStub{
  "completer error": {err: errors.New("upstream down")},
  "bad json":        {content: "not json"},
 } {
  t.Run(name, func(t *testing.T) {
   if _, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
    Query: "q", Documents: []string{"a", "b"},
   }); err == nil {
    t.Fatal("must surface an error")
   }
  })
 }
}

func TestLLMRerankerEmptyScoresIsEmptyResult(t *testing.T) {
 stub := &llmRerankerCompleterStub{content: `{"scores":[]}`}
 got, err := newLLMRerankerStub(stub, nil).Rerank(context.Background(), knowledgeport.RerankRequest{
  Query: "q", Documents: []string{"a", "b"},
 })
 if err != nil {
  t.Fatal(err)
 }
 if len(got) != 0 {
  t.Fatalf("empty scores must yield empty results, got %+v", got)
 }
}

func TestLLMRerankerNilMetricsTolerated(t *testing.T) {
 stub := &llmRerankerCompleterStub{content: `{"scores":[]}`}
 if _, err := newLLMReranker(stub, "m", 0, nil, zap.NewNop()).Rerank(context.Background(), knowledgeport.RerankRequest{
  Query: "q", Documents: []string{"a"},
 }); err != nil {
  t.Fatal(err)
 }
}

func TestLLMRerankerRecordsMetrics(t *testing.T) {
 ctx := reqctx.WithTenantID(context.Background(), "tenant-1")

 m := &rerankMetricRecorder{}
 stub := &llmRerankerCompleterStub{content: `{"scores":[{"index":0,"score":1}]}`}
 if _, err := newLLMRerankerStub(stub, m).Rerank(ctx, knowledgeport.RerankRequest{Query: "q", Documents: []string{"a"}}); err != nil {
  t.Fatal(err)
 }
 if len(m.inc) != 1 || m.inc[0] != "tenant-1:builtin-llm:ok" {
  t.Fatalf("metrics=%v", m.inc)
 }
 if len(m.dur) != 1 {
  t.Fatalf("duration not recorded: %v", m.dur)
 }

 m2 := &rerankMetricRecorder{}
 failStub := &llmRerankerCompleterStub{err: errors.New("boom")}
 if _, err := newLLMRerankerStub(failStub, m2).Rerank(ctx, knowledgeport.RerankRequest{Query: "q", Documents: []string{"a"}}); err == nil {
  t.Fatal("must fail")
 }
 if len(m2.inc) != 1 || m2.inc[0] != "tenant-1:builtin-llm:error" {
  t.Fatalf("metrics=%v", m2.inc)
 }
}

func TestLLMRerankerAppliesTimeoutBudget(t *testing.T) {
 r := &llmReranker{timeout: 0}
 if d := r.rerankTimeout(); d != constants.RerankLLMTimeout {
  t.Fatalf("zero timeout must fall back to %v, got %v", constants.RerankLLMTimeout, d)
 }
 r2 := &llmReranker{timeout: 7 * time.Second}
 if d := r2.rerankTimeout(); d != 7*time.Second {
  t.Fatalf("explicit timeout must be kept, got %v", d)
 }
}

func TestLLMRerankerTimeoutCancelsBlockedCompleter(t *testing.T) {
 r := newLLMReranker(blockingCompleter{}, "m", 20*time.Millisecond, nil, zap.NewNop())
 _, err := r.Rerank(context.Background(), knowledgeport.RerankRequest{Query: "q", Documents: []string{"a"}})
 if err == nil {
  t.Fatal("blocked completer must be cancelled by the timeout")
 }
}
```

- [ ] **Step 2: 运行测试确认 FAIL**

Run: `go test ./api/wiring/ -run TestLLMReranker -v`
Expected: FAIL — 编译错误 `undefined: llmReranker`。

- [ ] **Step 3: 实现 `llmReranker`**

创建 `api/wiring/llm_reranker.go`（完整文件）：

```go
package wiring

import (
 "context"
 "encoding/json"
 "fmt"
 "strings"
 "time"

 knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
 llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "github.com/byteBuilderX/stratum/pkg/reqctx"
 "go.uber.org/zap"
)

// llmReranker 是 builtin-score-v1 的 LLM 语义重排器（listwise）：复用平台 LLM
// 网关对候选按查询相关性打分，接口与外部 reranker 同形
// （knowledgeport.Reranker）。放在组合根（与 knowledgeJudge 同先例），
// knowledge/infrastructure/rerank 保持对兄弟 context 零依赖。
type llmReranker struct {
 completer llmgatewaydomain.LLMCompleter // Gateway 结构性满足
 model     string                        // 平台配置重排模型（chat 目录校验在 wiring 层）
 timeout   time.Duration                 // 单次调用预算（≤0 回落 RerankLLMTimeout）
 metrics   observability.MetricsProvider // 可为 nil（跳过指标记录）
 logger    *zap.Logger
}

func newLLMReranker(
 completer llmgatewaydomain.LLMCompleter,
 model string,
 timeout time.Duration,
 metrics observability.MetricsProvider,
 logger *zap.Logger,
) *llmReranker {
 return &llmReranker{completer: completer, model: model, timeout: timeout, metrics: metrics, logger: logger}
}

func (r *llmReranker) rerankTimeout() time.Duration {
 if r.timeout <= 0 {
  return constants.RerankLLMTimeout
 }
 return r.timeout
}

// Rerank 对 req.Documents 按 req.Query 相关度 listwise 打分并返回打分结果。
// 候选正文在本层内部按 RerankLLMMaxDocRunes 截断（调用方传完整候选）。
// 返回 LLM 输出的候选结果（index 去重、非法 index 跳过）；结果不足 topN
// 的补尾与最终排序由调用方负责。失败/超时/解析失败返回 error（fail-open，
// 调用方降级为召回分数排序）。
func (r *llmReranker) Rerank(ctx context.Context, req knowledgeport.RerankRequest) ([]knowledgeport.RerankResult, error) {
 ctx, cancel := context.WithTimeout(ctx, r.rerankTimeout())
 defer cancel()

 var prompt strings.Builder
 prompt.WriteString("你是严谨的检索相关性评分法官。给定查询，对下列候选文档片段按与查询的相关性打分（0 到 1，越高越相关），分数要有区分度。只输出 JSON，不输出其他内容。\n\nQuery:\n")
 prompt.WriteString(req.Query)
 prompt.WriteString("\n\nCandidates:\n")
 for i, doc := range req.Documents {
  fmt.Fprintf(&prompt, "%d. %s\n", i, truncateRunes(doc, constants.RerankLLMMaxDocRunes))
 }
 prompt.WriteString("\n输出 JSON：{\"scores\":[{\"index\":<候选编号>,\"score\":<0..1>},...]}，为每个候选恰好输出一个条目。")

 zero := float64(0) // 显式 0 = 确定性采样，避免 provider 默认温度（review M4/F2）
 start := time.Now()
 resp, err := r.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
  Model:     r.model,
  MaxTokens: constants.RerankLLMMaxTokens,
  ResponseFormat: &llmgatewaydomain.ResponseFormat{
   Type: "json_object",
  },
  Temperature: &zero,
  Messages: []llmgatewaydomain.Message{
   {Role: "system", Content: "你是严谨的检索相关性评分法官。只输出 JSON，不输出其他内容。"},
   {Role: "user", Content: prompt.String()},
  },
 })
 if err != nil {
  r.record(ctx, "error")
  return nil, fmt.Errorf("llm rerank: %w", err)
 }
 r.recordDuration(start)

 var parsed struct {
  Scores []struct {
   Index int     `json:"index"`
   Score float32 `json:"score"`
  } `json:"scores"`
 }
 if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
  r.record(ctx, "error")
  return nil, fmt.Errorf("llm rerank: parse scores: %w", err)
 }
 results := make([]knowledgeport.RerankResult, 0, len(parsed.Scores))
 seen := make(map[int]struct{}, len(parsed.Scores))
 for _, s := range parsed.Scores {
  if s.Index < 0 || s.Index >= len(req.Documents) {
   continue // 防御 LLM 幻觉输出非法 index
  }
  if _, ok := seen[s.Index]; ok {
   continue // 重复 index 保留首次出现
  }
  seen[s.Index] = struct{}{}
  results = append(results, knowledgeport.RerankResult{Index: s.Index, Score: s.Score})
 }
 r.record(ctx, "ok")
 return results, nil
}

// record 记录重排请求指标（三态之一：ok/error/degraded）。标签固定
// "builtin-llm"（不暴露平台重排模型名）；tenant 从 ctx 取号（RerankRequest
// 无 tenant 字段，与 Cohere 一致）。metrics 可为 nil（跳过记录）。
func (r *llmReranker) record(ctx context.Context, status string) {
 if r.metrics != nil {
  r.metrics.IncRerankRequest(reqctx.TenantIDFromContext(ctx), "builtin-llm", status)
 }
}

// recordDuration 记录单次重排调用耗时（HTTP 往返成功返回后）。
func (r *llmReranker) recordDuration(start time.Time) {
 if r.metrics != nil {
  r.metrics.RecordRerankDuration("builtin-llm", time.Since(start).Seconds())
 }
}
```

- [ ] **Step 4: 运行测试确认 PASS**

Run: `go test ./api/wiring/ -run TestLLMReranker -v`
Expected: PASS（全部 8 个测试）。

- [ ] **Step 5: 代码质量门禁**

Run: `bash scripts/quality/risk-regression-guard.sh --explain`（无命中可跳过该项）且 `make code-quality` 中 `llm_reranker.go` 无超限告警。

- [ ] **Step 6: Commit**

```bash
git add api/wiring/llm_reranker.go api/wiring/llm_reranker_test.go
git commit -m "feat(wiring): add llm semantic reranker

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: `RAGService` 语义重排（`rag_service.go`）

**Files:**

- Modify: `internal/knowledge/application/rag_service.go:188-205`（结构体加字段）、`:228-230`（`SetSufficiencyJudge` 后加 setter）、`:999-1000`（builtin 分支）
- Test: `internal/knowledge/application/rag_service_rerank_test.go`（追加测试）

**Interfaces:**

- Consumes: `knowledgeport.Reranker`、`knowledgeport.RerankRequest`、`knowledgeport.RerankResult`（Task 2 无关，本 Task 直接使用）、`reqctx.TenantIDFromContext`（`rag_service.go` 已 import）、`constants.MinRerankCandidates`。
- Produces: `RAGService.SetSemanticReranker(r knowledgeport.Reranker, topN int)`（Task 4 wiring 调用）；`rerankSemantic(ctx, req, pool []Source) ([]Source, error)`（本文件私有，直接测试）。

- [ ] **Step 1: 写 failing 语义重排测试**

在 `internal/knowledge/application/rag_service_rerank_test.go` 追加（import 块加 `"fmt"` 与 `"github.com/byteBuilderX/stratum/pkg/reqctx"`，其余已有）：

```go
func TestRAGQueryBuiltinSemanticRerankRescores(t *testing.T) {
 vectors := NewMockVectorStore()
 vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
  {ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.9},
  {ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.1},
  {ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 0.5},
 })
 reranker := &fakeReranker{results: []knowledgeport.RerankResult{
  {Index: 2, Score: 0.9}, {Index: 0, Score: 0.5}, {Index: 1, Score: 0.2},
 }}
 service := vectorRAGService(vectors)
 service.SetSemanticReranker(reranker, 10)

 got, err := service.Query(context.Background(), RAGQueryRequest{
  TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
  ViewerID: "test-user",
  TopK:     3, EmbeddingModel: "embedding-3", Reranking: "builtin-score-v1",
 })
 if err != nil {
  t.Fatal(err)
 }
 if reranker.calls != 1 {
  t.Fatalf("semantic rerank calls=%d, want 1", reranker.calls)
 }
 if reranker.lastReq.Model != "" {
  t.Fatalf("semantic rerank must pass empty model sentinel, got %q", reranker.lastReq.Model)
 }
 if len(reranker.lastReq.Documents) != 3 || reranker.lastReq.TopN != 3 {
  t.Fatalf("semantic rerank must score the whole pool: %+v", reranker.lastReq)
 }
 // LLM 分数覆盖召回分数，结果按 LLM 分数降序。
 if len(got.Sources) != 3 || got.Sources[0].ChunkID != "chunk-c" || got.Sources[0].Score != 0.9 ||
  got.Sources[1].ChunkID != "chunk-a" || got.Sources[1].Score != 0.5 ||
  got.Sources[2].ChunkID != "chunk-b" || got.Sources[2].Score != 0.2 {
  t.Fatalf("sources=%+v", got.Sources)
 }
}

func TestRAGQueryBuiltinSemanticRerankFailsOpenOnError(t *testing.T) {
 vectors := NewMockVectorStore()
 vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
  {ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.1},
  {ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.05},
  {ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 0.2},
 })
 reranker := &fakeReranker{err: errors.New("llm down")}
 metrics := &rerankMetrics{}
 service := vectorRAGService(vectors)
 service.SetSemanticReranker(reranker, 10)
 service.SetMetrics(metrics)

 ctx := reqctx.WithTenantID(context.Background(), "tenant-1")
 got, err := service.Query(ctx, RAGQueryRequest{
  TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
  ViewerID: "test-user",
  TopK:     3, EmbeddingModel: "embedding-3", Reranking: "builtin-score-v1",
 })
 if err != nil {
  t.Fatalf("rerank failure must not fail the query: %v", err)
 }
 // fail-open：保持召回分数排序（chunk-b 的 L2 最小 → 相似度最高）。
 if len(got.Sources) != 3 || got.Sources[0].ChunkID != "chunk-b" || got.Sources[0].Score != l2ToSim(0.05) {
  t.Fatalf("fallback must keep recall-score ordering: %+v", got.Sources)
 }
 if len(metrics.requests) != 1 || metrics.requests[0] != "tenant-1:builtin-llm:degraded" {
  t.Fatalf("metrics=%v", metrics.requests)
 }
}

func TestRAGQueryBuiltinSemanticRerankSkipsTinyPool(t *testing.T) {
 vectors := NewMockVectorStore()
 vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
  {ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.5},
 })
 reranker := &fakeReranker{}
 service := vectorRAGService(vectors)
 service.SetSemanticReranker(reranker, 10)

 got, err := service.Query(context.Background(), RAGQueryRequest{
  TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
  ViewerID: "test-user",
  TopK:     1, EmbeddingModel: "embedding-3", Reranking: "builtin-score-v1",
 })
 if err != nil {
  t.Fatal(err)
 }
 if reranker.calls != 0 {
  t.Fatal("single-candidate pool must skip the LLM call")
 }
 if len(got.Sources) != 1 || got.Sources[0].ChunkID != "chunk-a" {
  t.Fatalf("skipped rerank keeps retrieval order: %+v", got.Sources)
 }
}

func TestRerankSemanticNarrowsToTopN(t *testing.T) {
 pool := make([]Source, 0, 20)
 for i := 0; i < 20; i++ {
  pool = append(pool, Source{
   DocumentID: "doc", ChunkID: fmt.Sprintf("chunk-%02d", i),
   Content: fmt.Sprintf("content %d", i), Score: float32(20 - i),
  })
 }
 reranker := &fakeReranker{results: []knowledgeport.RerankResult{
  {Index: 0, Score: 1.0}, {Index: 1, Score: 0.9}, {Index: 2, Score: 0.8},
 }}
 service := vectorRAGService(nil)
 service.SetSemanticReranker(reranker, 5)

 narrowed, err := service.rerankSemantic(context.Background(), RAGQueryRequest{Question: "q"}, pool)
 if err != nil {
  t.Fatal(err)
 }
 // 池 20 条 > semanticTopN 5 → 只精排召回分前 5；返回 5 条。
 if reranker.calls != 1 || len(reranker.lastReq.Documents) != 5 || reranker.lastReq.TopN != 5 {
  t.Fatalf("semantic rerank must score the top-5 recall candidates: %+v", reranker.lastReq)
 }
 if len(narrowed) != 5 {
  t.Fatalf("narrowed pool = %d, want 5", len(narrowed))
 }
 if narrowed[0].ChunkID != "chunk-00" || narrowed[0].Score != 1.0 {
  t.Fatalf("LLM rescored candidate first: %+v", narrowed[0])
 }
}

func TestRAGQueryBuiltinSemanticRerankPartialTailFill(t *testing.T) {
 vectors := NewMockVectorStore()
 vectors.SetSearchResults([]knowledgeport.VectorSearchResult{
  {ID: "chunk-a", SourceDocument: "doc-a", Content: "a", Score: 0.8},
  {ID: "chunk-b", SourceDocument: "doc-b", Content: "b", Score: 0.7},
  {ID: "chunk-c", SourceDocument: "doc-c", Content: "c", Score: 0.6},
 })
 reranker := &fakeReranker{results: []knowledgeport.RerankResult{
  {Index: 2, Score: 0.9}, // LLM 只返回第 3 条
 }}
 service := vectorRAGService(vectors)
 service.SetSemanticReranker(reranker, 10)

 got, err := service.Query(context.Background(), RAGQueryRequest{
  TenantID: "tenant-1", WorkspaceID: "workspace-1", Question: "query", Mode: "vector",
  ViewerID: "test-user",
  TopK:     3, EmbeddingModel: "embedding-3", Reranking: "builtin-score-v1",
 })
 if err != nil {
  t.Fatal(err)
 }
 // LLM 只给 chunk-c 0.9；chunk-a/b 未被打分，按召回分（L2 0.8/0.7→sim）补尾。
 if len(got.Sources) != 3 {
  t.Fatalf("sources=%+v", got.Sources)
 }
 if got.Sources[0].ChunkID != "chunk-c" || got.Sources[0].Score != 0.9 {
  t.Fatalf("LLM-scored candidate must sort first, got %+v", got.Sources[0])
 }
 if got.Sources[1].ChunkID != "chunk-b" || got.Sources[1].Score != l2ToSim(0.7) ||
  got.Sources[2].ChunkID != "chunk-a" || got.Sources[2].Score != l2ToSim(0.8) {
  t.Fatalf("tail-filled candidates keep recall scores, got %+v", got.Sources[1:])
 }
}
```

- [ ] **Step 2: 运行测试确认 FAIL**

Run: `go test ./internal/knowledge/application/ -run 'TestRAGQueryBuiltinSemantic|TestRerankSemantic' -v`
Expected: FAIL — 编译错误 `undefined: SetSemanticReranker`。

- [ ] **Step 3: 实现字段 + setter + `rerankSemantic` + builtin 分支**

`rag_service.go` 四处修改：

(a) `RAGService` 结构体（第 202 行 `sufficiencyJudge` 后）加字段：

```go
 // semanticReranker 是 builtin-score-v1 的 LLM 语义重排器；nil = 未装配
 // （fail-open，builtin 走纯召回分数排序）。semanticTopN 是精排候选上限
 // （≤0 由 wiring 在注入前回落 RerankLLMTopN）。
 semanticReranker knowledgeport.Reranker
 semanticTopN     int
```

(b) `SetSufficiencyJudge`（第 228-230 行）后加 setter：

```go
// SetSemanticReranker 注入 builtin-score-v1 的 LLM 语义重排器；wiring 在注入
// 前把 ≤0 的 topN 解析为 RerankLLMTopN 默认。
func (rs *RAGService) SetSemanticReranker(r knowledgeport.Reranker, topN int) {
 rs.semanticReranker = r
 rs.semanticTopN = topN
}
```

(c) `rerankSources` 的 builtin 分支（第 999-1000 行）替换为：

```go
 case "builtin-score-v1":
  if rs.semanticReranker != nil && len(pool) >= 2 {
   narrowed, err := rs.rerankSemantic(ctx, req, pool)
   if err != nil {
    // fail-open：LLM 重排失败降级为召回分数排序，检索永不失败。
    rs.logger.Warn("knowledge.retrieval.llm_rerank_degraded",
     zap.Error(err), zap.Int("pool_size", len(pool)))
    if rs.metrics != nil {
     rs.metrics.IncRerankRequest(reqctx.TenantIDFromContext(ctx), "builtin-llm", "degraded")
    }
   } else {
    pool = narrowed
   }
  }
  sort.SliceStable(pool, func(i, j int) bool { return pool[i].Score > pool[j].Score })
```

(d) `rerankExternal` 之后（第 1086 行后）新增方法：

```go
// rerankSemantic 用平台 LLM 语义重排器对召回池精排：先按召回分数取前
// semanticTopN 条（与池取 min）listwise 打分，未被打分的候选按召回分补尾。
// LLM 分数覆盖召回分数；返回后由 rerankSources 统一降序排序（LLM 分与召回
// 分同池混排）。失败向上传播，调用方按 fail-open 降级。
func (rs *RAGService) rerankSemantic(ctx context.Context, req RAGQueryRequest, pool []Source) ([]Source, error) {
 topN := min(rs.semanticTopN, len(pool))
 if topN < 2 {
  return pool, nil // 单候选/空池语义重排无意义，保持池走排序
 }
 docs := make([]string, topN) // 截断由重排器内部负责
 for i := range docs {
  docs[i] = pool[i].Content
 }
 // Model 传空哨兵：LLM 重排器用平台配置模型，忽略该字段。
 results, err := rs.semanticReranker.Rerank(ctx, knowledgeport.RerankRequest{
  Query: req.Question, Documents: docs, Model: "", TopN: topN,
 })
 if err != nil {
  return nil, err
 }
 narrowed := make([]Source, 0, topN)
 used := make(map[int]struct{}, len(results))
 for _, r := range results {
  if r.Index < 0 || r.Index >= topN {
   continue
  }
  if _, ok := used[r.Index]; ok {
   continue
  }
  used[r.Index] = struct{}{}
  s := pool[r.Index]
  s.Score = r.Score
  narrowed = append(narrowed, s)
 }
 for i := 0; i < topN && len(narrowed) < topN; i++ {
  if _, ok := used[i]; !ok {
   narrowed = append(narrowed, pool[i]) // 未被打分候选按召回分补尾
  }
 }
 return narrowed, nil
}
```

- [ ] **Step 4: 运行测试确认 PASS**

Run: `go test ./internal/knowledge/application/ -run 'TestRAGQueryBuiltinSemantic|TestRerankSemantic|TestRAGQueryBuiltinRerankStableScoreDesc' -v`
Expected: PASS（新 5 个 + 既有 stable-sort 回归）。既有 `TestRAGQueryBuiltinRerankStableScoreDesc` 验证未装配时 builtin 仍纯排序。

- [ ] **Step 5: 代码质量门禁**

Run: `make code-quality`，确认 `rag_service.go` 新增/修改函数（`rerankSemantic` ≤120 行、圈复杂度 ≤10）无超限。

- [ ] **Step 6: Commit**

```bash
git add internal/knowledge/application/rag_service.go internal/knowledge/application/rag_service_rerank_test.go
git commit -m "feat(knowledge): rerank builtin pool with llm semantic scorer

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: Wiring 注入 + spec §4.4 更新

**Files:**

- Modify: `api/wiring/knowledge.go:83`（`wireKnowledgeJudge` 后加调用）、`:140`（`wireKnowledgeJudge` 后加方法）
- Modify: `docs/superpowers/specs/2026-08-22-llm-rerank-design.md:216-263`（§4.4 代码块更新为 `semanticRerankerDeps` 拆分形态）
- Test: `api/wiring/llm_reranker_test.go`（追加 wiring 注入测试；复用 `newKnowledgeRegistry` helper，来自 `knowledge_embed_resolver_test.go` 同包）

**Interfaces:**

- Consumes: `Container.LLMGateway.Gateway`、`Container.LLMGateway.Registry`（`*llmgateway.ModelRegistry`）、`Container.Config.KnowledgeRerank` + `RerankLLMConfigured()`（Task 1）、`Container.Platform.Metrics`（可为 nil）、`newLLMReranker`（Task 2）、`knowledge.RAGService.SetSemanticReranker`（Task 3）、`llmgateway.ModelRegistry.ListChatModelsByTenant(ctx) ([]string, error)`。

> **已迁移（2026-08-22）**：`Container.Config.KnowledgeRerank`/`RerankLLMConfigured`/`llmRerankModelInCatalogue` 均已删除；`wireSemanticReranker` 改为仅按 gateway 可用性注入，模型运行期从 `req.RerankModel`（workspace 显式配置）读取。

- Produces: `(*Container).wireSemanticReranker(ctx, rag)`（buildKnowledge 调用点）、`(*Container).semanticRerankerDeps(ctx) (knowledgeport.Reranker, int)`（测试面）、`(*Container).llmRerankModelInCatalogue(ctx, model) bool`。后续无 Task 消费，但 spec §4.4 需一致。

- [ ] **Step 1: 写 failing wiring 注入测试**

在 `api/wiring/llm_reranker_test.go` 末尾追加（import 块加 `"github.com/byteBuilderX/stratum/config"`、`llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"`、`"github.com/byteBuilderX/stratum/internal/llmgateway/domain"`（别名 `domain`）、`"go.uber.org/zap/zapcore"`、`"go.uber.org/zap/zaptest/observer"`；`context/errors/strings/testing/time` 已有）：

```go
// hasLogMessage reports whether any captured log entry carries the message.
func hasLogMessage(entries []observer.LoggedEntry, msg string) bool {
 for _, e := range entries {
  if e.Message == msg {
   return true
  }
 }
 return false
}

// newSemanticRerankContainer builds a minimal Container whose chat catalogue
// holds exactly `model` (empty → empty catalogue). 不复用 newKnowledgeRegistry
// （其 chatProtos 传 nil → ModelRegistry.supports(CapChat) 恒 false，chat 模型
// 全被 listModelsByCapability 过滤掉）；显式注入 chat 协议 map 使
// ListChatModelsByTenant 能返回该模型。
func newSemanticRerankContainer(model string, topN int) *Container {
 var models []domain.Model
 if model != "" {
  models = []domain.Model{{
   ID: model, ProviderID: "provider-1", Name: model, Enabled: true,
   Capabilities: []domain.ModelCapability{domain.CapChat},
  }}
 }
 return &Container{
  Config: &config.Config{KnowledgeRerank: config.KnowledgeRerankConfig{Model: model, TopN: topN}},
  Logger: zap.NewNop(),
  LLMGateway: &LLMGateway{
   Gateway: llmgateway.NewGateway(nil, nil, nil),
   Registry: llmgateway.NewModelRegistry(
    &knowledgeModelRepo{models: models},
    &knowledgeProviderRepo{provider: domain.Provider{
     ID: "provider-1", Kind: domain.ProviderOpenAICompat, Enabled: true,
     BaseURL: "https://example.test/v1", APIKey: "test-key",
    }},
    map[domain.ProviderKind]llmgateway.ChatProtocol{domain.ProviderOpenAICompat: nil},
    map[domain.ProviderKind]llmgateway.EmbedProtocol{domain.ProviderOpenAICompat: nil},
    time.Minute,
   ),
  },
 }
}

func TestSemanticRerankerDepsGates(t *testing.T) {
 ctx := context.Background()

 t.Run("nil gateway not injected", func(t *testing.T) {
  c := newSemanticRerankContainer("qwen-turbo", 0)
  c.LLMGateway = nil
  if r, topN := c.semanticRerankerDeps(ctx); r != nil || topN != 0 {
   t.Fatalf("nil gateway must not inject: r=%v topN=%d", r, topN)
  }
 })
 t.Run("nil gateway pointer not injected", func(t *testing.T) {
  c := newSemanticRerankContainer("qwen-turbo", 0)
  c.LLMGateway.Gateway = nil
  if r, _ := c.semanticRerankerDeps(ctx); r != nil {
   t.Fatalf("nil gateway pointer must not inject: r=%v", r)
  }
 })
 t.Run("empty model not injected", func(t *testing.T) {
  c := newSemanticRerankContainer("", 0)
  if r, topN := c.semanticRerankerDeps(ctx); r != nil || topN != 0 {
   t.Fatalf("empty model must not inject: r=%v topN=%d", r, topN)
  }
 })
 t.Run("model absent from catalogue not injected", func(t *testing.T) {
  core, logs := observer.New(zapcore.WarnLevel)
  c := newSemanticRerankContainer("qwen-turbo", 0)
  c.Config = &config.Config{KnowledgeRerank: config.KnowledgeRerankConfig{Model: "not-managed", TopN: 5}}
  c.Logger = zap.New(core)
  if r, topN := c.semanticRerankerDeps(ctx); r != nil || topN != 0 {
   t.Fatalf("absent model must not inject: r=%v topN=%d", r, topN)
  }
  if !hasLogMessage(logs.All(), "knowledge.rerank.model_unavailable") {
   t.Fatal("absent model must WARN at wiring time")
  }
 })
 t.Run("defaults resolved when zero", func(t *testing.T) {
  c := newSemanticRerankContainer("qwen-turbo", 0) // TopN=0, Timeout=0, Platform=nil
  r, topN := c.semanticRerankerDeps(ctx)
  lr, ok := r.(*llmReranker)
  if !ok || lr == nil {
   t.Fatalf("must inject llmReranker, got %T", r)
  }
  if topN != constants.RerankLLMTopN {
   t.Fatalf("topN=%d want default %d", topN, constants.RerankLLMTopN)
  }
  if lr.timeout != constants.RerankLLMTimeout {
   t.Fatalf("timeout=%v want default %v", lr.timeout, constants.RerankLLMTimeout)
  }
  if lr.model != "qwen-turbo" {
   t.Fatalf("model=%q", lr.model)
  }
  if lr.metrics != nil {
   t.Fatal("nil platform must yield nil metrics")
  }
 })
 t.Run("explicit values kept", func(t *testing.T) {
  c := newSemanticRerankContainer("qwen-turbo", 0)
  c.Config = &config.Config{KnowledgeRerank: config.KnowledgeRerankConfig{
   Model: "qwen-turbo", TopN: 3, Timeout: 7 * time.Second,
  }}
  r, topN := c.semanticRerankerDeps(ctx)
  lr := r.(*llmReranker)
  if topN != 3 || lr.timeout != 7*time.Second {
   t.Fatalf("topN=%d timeout=%v", topN, lr.timeout)
  }
 })
}

func TestLLMRankModelInCatalogue(t *testing.T) {
 ctx := context.Background()
 managed := newSemanticRerankContainer("qwen-turbo", 0)

 if !managed.llmRerankModelInCatalogue(ctx, "qwen-turbo") {
  t.Fatal("managed chat model must be found")
 }
 if managed.llmRerankModelInCatalogue(ctx, "missing") {
  t.Fatal("absent model must not be found")
 }

 nilRegistry := newSemanticRerankContainer("qwen-turbo", 0)
 nilRegistry.LLMGateway.Registry = nil
 if nilRegistry.llmRerankModelInCatalogue(ctx, "qwen-turbo") {
  t.Fatal("nil registry must report not found")
 }

 nilGateway := newSemanticRerankContainer("qwen-turbo", 0)
 nilGateway.LLMGateway = nil
 if nilGateway.llmRerankModelInCatalogue(ctx, "qwen-turbo") {
  t.Fatal("nil gateway must report not found")
 }
}
```

- [ ] **Step 2: 运行测试确认 FAIL**

Run: `go test ./api/wiring/ -run 'TestSemanticRerankerDepsGates|TestLLMRankModelInCatalogue' -v`
Expected: FAIL — 编译错误 `undefined: semanticRerankerDeps`。

- [ ] **Step 3: 实现 wiring 注入**

`api/wiring/knowledge.go` 三处修改：

(a) import 块加 `"github.com/byteBuilderX/stratum/pkg/observability"`（放在 `constants` 之后，internal 组）：

```go
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/byteBuilderX/stratum/pkg/httpclient"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "github.com/byteBuilderX/stratum/pkg/textchunk"
```

(b) `buildKnowledge` 中 `c.wireKnowledgeJudge(rag)`（第 83 行）之后加调用：

```go
 // builtin-score-v1 的 LLM 语义重排（fail-open）：未配置/模型不在 chat 目录
 // 时保持未装配，builtin 走纯召回分数排序。
 c.wireSemanticReranker(ctx, rag)
```

(c) `wireKnowledgeJudge`（第 140 行 `}` 后）追加方法：

```go
// wireSemanticReranker 在 LLM gateway 可用、KNOWLEDGE_RERANK_MODEL 已配置且
// 模型在 chat 目录时注入 builtin-score-v1 的语义重排器；任一条件不满足保持
// 未装配（fail-open，builtin 走纯召回分数排序）。单独成方法以控制
// buildKnowledge 的圈复杂度。
func (c *Container) wireSemanticReranker(ctx context.Context, rag *knowledge.RAGService) {
 if r, topN := c.semanticRerankerDeps(ctx); r != nil {
  rag.SetSemanticReranker(r, topN)
 }
}

// semanticRerankerDeps 解析并构建 LLM 语义重排器；任一前置条件不满足返回
// (nil, 0)。topN/timeout 的 ≤0 默认值在此解析（回落常量），使 wiring 层
// 单测可直接验证注入决策而无需构造完整 Gateway（RAGService.semanticReranker
// 为 application 包未导出字段，行为探针会因 Gateway.Complete nil-panic）。
func (c *Container) semanticRerankerDeps(ctx context.Context) (knowledgeport.Reranker, int) {
 if c.LLMGateway == nil || c.LLMGateway.Gateway == nil || !c.Config.RerankLLMConfigured() {
  return nil, 0
 }
 krc := c.Config.KnowledgeRerank
 if !c.llmRerankModelInCatalogue(ctx, krc.Model) {
  // fail-open 装配：模型不在 chat 目录 → WARN + 不注入，builtin 走纯排序。
  // 配置错误在启动期暴露，而非运行期每查询失败（review F7）。
  c.Logger.Warn("knowledge.rerank.model_unavailable",
   zap.String("model", krc.Model), zap.String("reason", "model not in chat catalogue"))
  return nil, 0
 }
 topN := krc.TopN
 if topN <= 0 {
  topN = constants.RerankLLMTopN
 }
 timeout := krc.Timeout
 if timeout <= 0 {
  timeout = constants.RerankLLMTimeout
 }
 var metrics observability.MetricsProvider
 if c.Platform != nil {
  metrics = c.Platform.Metrics // c.Platform 可能为 nil（review H2）
 }
 return newLLMReranker(c.LLMGateway.Gateway, krc.Model, timeout, metrics, c.Logger), topN
}

// llmRerankModelInCatalogue 检查平台配置的重排模型是否在 enabled 的 chat 目录
// 中。目录查询失败或 registry 缺失按"不在目录"处理（fail-open 不注入、告警，
// 不阻断启动）。
func (c *Container) llmRerankModelInCatalogue(ctx context.Context, model string) bool {
 if c.LLMGateway == nil || c.LLMGateway.Registry == nil {
  return false
 }
 names, err := c.LLMGateway.Registry.ListChatModelsByTenant(ctx)
 if err != nil {
  c.Logger.Warn("knowledge.rerank.catalogue_unavailable", zap.Error(err))
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

> **已迁移（2026-08-22）**：该实现形态已改为仅按 gateway 可用性注入（`KNOWLEDGE_RERANK_MODEL` 平台级配置与目录预检已删除），模型运行期从 `req.RerankModel` 读取并经空模型守卫 fail-open，见 `docs/superpowers/plans/2026-08-22-llm-rerank-workspace-config.md` Task 6。

- [ ] **Step 4: 更新 spec §4.4 代码块**

将 `docs/superpowers/specs/2026-08-22-llm-rerank-design.md` 第 216-263 行的 go 代码块整体替换为（`wireSemanticReranker` 保持 buildKnowledge 面向形态，新增 `semanticRerankerDeps` 作为可单测的实现核心）：

```go
func (c *Container) wireSemanticReranker(ctx context.Context, rag *knowledge.RAGService) {
    if r, topN := c.semanticRerankerDeps(ctx); r != nil {
        rag.SetSemanticReranker(r, topN)
    }
}

// semanticRerankerDeps 解析并构建 LLM 语义重排器；任一前置条件不满足返回
// (nil, 0)。topN/timeout 的 ≤0 默认值在此解析（回落常量），使 wiring 层
// 单测可直接验证注入决策而无需构造完整 Gateway（RAGService.semanticReranker
// 为 application 包未导出字段，行为探针会因 Gateway.Complete nil-panic）。
func (c *Container) semanticRerankerDeps(ctx context.Context) (knowledgeport.Reranker, int) {
    if c.LLMGateway == nil || c.LLMGateway.Gateway == nil || !c.Config.RerankLLMConfigured() {
        return nil, 0
    }
    krc := c.Config.KnowledgeRerank
    if !c.llmRerankModelInCatalogue(ctx, krc.Model) {
        // fail-open 装配：模型不在 chat 目录 → WARN + 不注入，builtin 走纯排序。
        // 配置错误在启动期暴露，而非运行期每查询失败（review F7）。
        c.Logger.Warn("knowledge.rerank.model_unavailable",
            zap.String("model", krc.Model), zap.String("reason", "model not in chat catalogue"))
        return nil, 0
    }
    topN := krc.TopN
    if topN <= 0 {
        topN = constants.RerankLLMTopN
    }
    timeout := krc.Timeout
    if timeout <= 0 {
        timeout = constants.RerankLLMTimeout
    }
    var metrics observability.MetricsProvider
    if c.Platform != nil {
        metrics = c.Platform.Metrics // c.Platform 可能为 nil（review H2）
    }
    return newLLMReranker(c.LLMGateway.Gateway, krc.Model, timeout, metrics, c.Logger), topN
}

// llmRerankModelInCatalogue 检查平台配置的重排模型是否在 chat 模型目录。
// 目录查询失败按"不在目录"处理（fail-open 不注入），不静默放行。
func (c *Container) llmRerankModelInCatalogue(ctx context.Context, model string) bool {
    if c.LLMGateway == nil || c.LLMGateway.Registry == nil {
        return false
    }
    names, err := c.LLMGateway.Registry.ListChatModelsByTenant(ctx)
    if err != nil {
        c.Logger.Warn("knowledge.rerank.catalogue_unavailable", zap.Error(err))
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

- [ ] **Step 5: 运行全包测试确认 PASS**

Run: `go test ./api/wiring/ -run 'TestSemanticRerankerDepsGates|TestLLMRankModelInCatalogue|TestLLMReranker|TestKnowledgeJudge' -v`
Expected: PASS（含既有 knowledgeJudge 回归，验证未破坏注入路径）。

- [ ] **Step 6: 代码质量门禁**

Run: `make code-quality`，确认 `knowledge.go` 的 `semanticRerankerDeps`/`wireSemanticReranker`/`llmRerankModelInCatalogue` 无超限。

- [ ] **Step 7: Commit**

```bash
git add api/wiring/knowledge.go api/wiring/llm_reranker_test.go docs/superpowers/specs/2026-08-22-llm-rerank-design.md
git commit -m "feat(wiring): inject llm reranker under builtin-score-v1

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: 前端 tooltip 更新

**Files:**

- Modify: `web/src/modules/knowledge/components/WorkspaceConfigForm.tsx:94`（`重排策略` Form.Item 的 tooltip）

**Interfaces:**

- 无对外接口。仅文案：`builtin-score-v1` 选项的 tooltip 表达"LLM 语义精排 + 需配置重排模型 + 未配置降级"（保留原"外部重排需在模型管理中配置"指引，review L3）。

- [ ] **Step 1: 更新 tooltip 文案**

`web/src/modules/knowledge/components/WorkspaceConfigForm.tsx` 第 94 行 `<Form.Item label="重排策略" name="reranking" tooltip="内置重排按相关度二次排序；外部重排需在模型管理中配置">` 的 `tooltip` 改为：

```tsx
tooltip="内置重排由平台 LLM 模型语义精排，需在模型管理中配置重排模型；未配置时自动降级为分数排序。外部重排需在模型管理中配置"
```

`<Option value="builtin-score-v1">内置重排</Option>`（第 97 行）保持不变。

- [ ] **Step 2: 前端构建验证**

Run: `make fe-lint && make fe-build`
Expected: 全绿（lint 无新告警、build 成功）。

- [ ] **Step 3: Commit**

```bash
git add web/src/modules/knowledge/components/WorkspaceConfigForm.tsx
git commit -m "feat(web): describe llm semantic rerank in rerank strategy tooltip

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 全量验证（PR 前门禁）

**Files:**

- 无代码改动；跑门禁并暴露任何失败。

**Interfaces:**

- 验证 Task 1-5 的交付物集成正确、无回归。

- [ ] **Step 1: 后端全量快速验证**

Run: `go vet ./... && go test -short ./...`
Expected: 全部 PASS。

- [ ] **Step 2: 契约与竞态验证**

Run: `go test ./api/http/ -run Contract -v && go test -race -timeout 30s ./api/wiring/ ./internal/knowledge/application/ ./config/`
Expected: 契约 golden 不变（无 proto 改动）；三个包 `-race` 全绿。

- [ ] **Step 3: 代码质量门禁**

Run: `make code-quality`
Expected: 无新增超限告警（新增函数满足 圈复杂度≤10、认知≤15、≤120 行、嵌套≤4）。

- [ ] **Step 4: 变更影响自检**

Run: `git diff --stat origin/main...HEAD && git log --oneline HEAD~5..HEAD`
确认变更集只含：`pkg/constants/knowledge.go`、`config/config.go`、`config/config_test.go`、`api/wiring/llm_reranker.go`、`api/wiring/llm_reranker_test.go`、`api/wiring/knowledge.go`、`internal/knowledge/application/rag_service.go`、`internal/knowledge/application/rag_service_rerank_test.go`、`web/src/modules/knowledge/components/WorkspaceConfigForm.tsx`、spec/plan 文档。无 `config/prod.yaml`、无密钥、无其他 context 文件。

- [ ] **Step 5: 汇报验收结论**

向用户汇报：任务 1-5 已提交、Task 6 门禁结果、可开 PR（base main）进入 CI → 合并 → CD 部署的下一步。
