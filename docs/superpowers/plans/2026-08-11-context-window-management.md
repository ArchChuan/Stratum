# 上下文窗口管理统一设计实现计划（2026-08-11）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 agent 主链路的上下文窗口、压缩、错误分类统一为一次执行一次记账的预算账本，消除每步同步 LLM 摘要、空转重试与模型无关的窗口固化。

**Architecture:** 两阶段窗口解析（模型窗口 → agent 窗口，执行时动态解析替代 Create/Update 固化）；执行级预算账本（window−reserve=usable，fixedHead/tools/history/task 四配额）；压缩冷却 + 动态时间片；错误分类统一表（Canceled/DeadlineExceeded/context_length_exceeded 永久，瞬态退避）；成本预算（token 总量）图级检查点。

**Tech Stack:** Go 1.25、Gin、OTEL、Zap。涉及 `internal/agent/application/{graph,agent.go,agent_service.go,context_budget.go}`、`internal/llmgateway/infrastructure/{errors.go,openai_compat.go,gateway.go,model_catalog.go}`、`internal/parameters/domain/registry.go`、`api/wiring/agent.go`、`pkg/constants/agent.go`。

## Global Constraints

- 项目默认原则：正确 > 清晰 > 速度；只做任务相关修改；有疑问先问。
- DDD 分层：`pkg/` 不 import `internal/`；`domain/` 仅依赖 stdlib + `pkg/constants`；`application/` 不 import pgx/Redis/NATS/Gin；`graph` 包不得 import llmgateway/infrastructure——vendor 表通过 wiring 注入闭包或导出函数访问。
- 行为数字禁止内联：跨包放 `pkg/constants/<domain>.go`；名称含 `Default`/`Max`/`Min` 或单位语义。
- Go 门禁：圈复杂度 ≤10、认知复杂度 ≤15、函数长度 ≤120 行、最大嵌套 ≤4；行宽 ≤120。
- 错误逐层 `fmt.Errorf("operation: %w", err)`；日志只用 Zap。
- 测试：表驱动；mock 外部依赖不 mock 领域逻辑；用例名描述行为。
- 提交格式 `[type](scope): description`；禁止在 main 直接提交；worktree 已建（feat/context-window-management）。
- 行为变更含 LLM 调用路径，PR 前必须 `make test-verify-before-pr` + `stratum-e2e-development` skill。

---

## 文件结构

**改动文件（12）**：

| 文件 | 职责 | 任务 |
|---|---|---|
| `internal/llmgateway/infrastructure/errors.go` | 错误分类：DeadlineExceeded 永久；ErrContextLengthExceeded 语义化 | 1, 2 |
| `internal/llmgateway/infrastructure/openai_compat.go` | 400 body error.code/message 解析 | 2 |
| `internal/llmgateway/infrastructure/model_catalog.go` | 前缀族匹配 + 导出 `LookupModelSpec`（窗口 + 输出上限） | 3 |
| `internal/agent/application/graph/window.go`（新） | 两阶段窗口解析纯函数 + window source | 4 |
| `internal/agent/application/agent_service.go` | 删 Create/buildUpdateConfig 固化；执行时解析；resolveEffectiveParameters 扩展 | 4, 6, 7 |
| `internal/agent/application/agent.go` | ExecutionConfig/ExecutionOption 新字段；executeReAct 接线；trace attr | 4, 6, 7, 9 |
| `internal/agent/domain/agent.go` | AgentConfig 新字段 | 6, 7 |
| `internal/parameters/domain/registry.go` | 新 registry key ×2 | 6, 7 |
| `internal/agent/application/graph/budget.go`（新） | 预算账本 ComputeBudget | 5 |
| `internal/agent/application/context_budget.go` | 统一账本来源 | 5 |
| `internal/agent/application/graph/compaction.go` | 阈值用 history 配额；冷却 | 5, 6 |
| `internal/agent/application/graph/react_state.go` | ReActState 新字段 | 6, 7 |
| `internal/agent/application/graph/react_llm.go` | 成本预算检查点；NoPrimaryRetry/MaxCandidates 透传 | 7, 8 |
| `internal/agent/infrastructure/capability/history_compactor.go` | BudgetPolicy 时间片 | 8 |
| `internal/agent/domain/port/capability.go` | LLMCapRequest 新字段 | 8 |
| `internal/llmgateway/domain/completion.go` | CompletionRequest 新字段 | 8 |
| `internal/llmgateway/infrastructure/gateway.go` | NoPrimaryRetry/MaxCandidates 生效 | 8 |
| `api/wiring/agent.go` | VendorWindowLookup 注入 | 4 |
| `pkg/constants/agent.go` | 冷却常量；1M ceiling；outputReserve 默认 | 4, 5, 6 |

**新文件（3）**：`graph/window.go`、`graph/budget.go`、`graph/cost_budget.go`（并入 Task 7，不单列）。

---

### Task 1: 错误分类修复——`context.DeadlineExceeded` 永久化

**Files:**

- Modify: `internal/llmgateway/infrastructure/errors.go:41-59`
- Test: `internal/llmgateway/infrastructure/errors_test.go`（已有文件，追加用例）

**Interfaces:**

- Consumes: 无
- Produces: `isTransient(err error) bool` 语义变更——DeadlineExceeded 返回 false（永久）。所有 gateway 重试/降级路径自动 fail-fast。Task 2 复用 `markPermanent`。

- [ ] **Step 1: 写失败测试**——追加到 `errors_test.go`：

```go
func TestIsTransient_DeadlineExceededIsPermanent(t *testing.T) {
 cases := []struct {
  name string
  err  error
  want bool // transient?
 }{
  {name: "canceled is permanent", err: context.Canceled, want: false},
  {name: "deadline exceeded is permanent", err: context.DeadlineExceeded, want: false},
  {name: "wrapped deadline is permanent", err: fmt.Errorf("upstream: %w", context.DeadlineExceeded), want: false},
  // http.Client timeout 包装链：url.Error → net.OpError → DeadlineExceeded
  // 必须判定永久，否则 60s client timeout 仍被当瞬态重试。
  {name: "http client timeout chain is permanent",
   err: &url.Error{Op: "Post", URL: "https://x", Err: &net.OpError{Op: "dial", Err: context.DeadlineExceeded}}, want: false},
  {name: "net timeout is transient", err: &net.OpError{Op: "dial", Err: &net.DNSError{Err: "timeout"}}, want: true},
  {name: "status 429 is transient", err: errStatus(429), want: true},
  {name: "status 503 is transient", err: errStatus(503), want: true},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   if got := isTransient(tc.err); got != tc.want {
    t.Fatalf("isTransient(%v) = %v, want %v", tc.err, got, tc.want)
   }
  })
 }
}
```

（`errStatus(n)` 用现有测试 helper——若不存在，在测试文件里定义 `type fakeStatusErr int; func (f fakeStatusErr) Error() string {...}; func (f fakeStatusErr) StatusCode() int { return int(f) }`，按 `isStatusTransient` 探测 `StatusCode() int` 方法的现有协议写。）

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/llmgateway/infrastructure/ -run TestIsTransient_DeadlineExceededIsPermanent -v`
Expected: FAIL——`deadline exceeded is permanent` 与 `http client timeout chain is permanent` 用例 `isTransient` 返回 true。

- [ ] **Step 3: 最小实现**——`errors.go` 的 `isTransient` 首行插入：

```go
func isTransient(err error) bool {
 // DeadlineExceeded 是永久错误：等待无意义，继续试只叠加时延。
 // 必须放在 isNetTransient 之前：http.Client timeout 的错误链
 // (url.Error → net.OpError → DeadlineExceeded) 会被 isNetTransient
 // 误判为网络瞬态，导致 5s 压缩预算耗尽后 gateway 空转重试。
 if errors.Is(err, context.DeadlineExceeded) {
  return false
 }
 if errors.Is(err, context.Canceled) {
  return false
 }
 // ...原 isNetTransient / isConnError / isStatusTransient 逻辑不变
}
```

（`errors` 与 `context` import 若缺失则补。）

- [ ] **Step 4: 运行确认通过**

Run: `go test -short ./internal/llmgateway/infrastructure/ -run TestIsTransient_DeadlineExceededIsPermanent -v`
Expected: PASS。再跑 `go test -short ./internal/llmgateway/infrastructure/` 全量，确认现有重试/降级测试无回归（若 `fallback_test.go` 有依赖 DeadlineExceeded 瞬态行为的用例，逐条核实语义后修正断言——DeadlineExceeded 瞬态是 bug，任何依赖它的测试都错）。

- [ ] **Step 5: 提交**

```bash
git add internal/llmgateway/infrastructure/errors.go internal/llmgateway/infrastructure/errors_test.go
git commit -m "fix(llmgateway): DeadlineExceeded 视为永久错误，消除超时空转重试"
```

---

### Task 2: `context_length_exceeded` 语义化

**Files:**

- Modify: `internal/llmgateway/infrastructure/errors.go`（新增错误类型 + 识别）
- Modify: `internal/llmgateway/infrastructure/openai_compat.go:230-254`
- Test: `internal/llmgateway/infrastructure/errors_test.go`、`openai_compat_test.go`

**Interfaces:**

- Consumes: Task 1 的 `isTransient` 语义
- Produces: `ErrContextLengthExceeded`（sentinel）——`errors.Is(err, ErrContextLengthExceeded)` 可匹配；错误实现 `Permanent() bool`（走既有 permanentMarker 探测，graph/retry.go:12 无需改动）+ `ContextLengthExceeded() bool`（Task 9 降级探测用）。导出 `IsContextLengthExceeded(err error) bool`（errors.Is 探 sentinel）。

- [ ] **Step 1: 写失败测试**

```go
func TestIsContextLengthExceeded(t *testing.T) {
 cases := []struct {
  name string
  err  error
  want bool
 }{
  {name: "bare sentinel", err: ErrContextLengthExceeded, want: true},
  {name: "wrapped", err: fmt.Errorf("complete: %w", ErrContextLengthExceeded), want: true},
  {name: "other 400", err: fmt.Errorf("complete: status 400: schema mismatch"), want: false},
  {name: "nil", err: nil, want: false},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   if got := IsContextLengthExceeded(tc.err); got != tc.want {
    t.Fatalf("IsContextLengthExceeded(%v) = %v, want %v", tc.err, got, tc.want)
   }
  })
 }
}

// 协议层识别：400 响应 body 的 error.code == "context_length_exceeded"
func TestComplete_400ContextLengthExceeded(t *testing.T) {
 // httptest server 返回 400 + {"error":{"code":"context_length_exceeded","message":"..."}}
 // 走 openai_compat Complete 非流式路径（若现有 openai_compat_test.go 已有 mock server 模式则复用）
 // 断言：err != nil 且 IsContextLengthExceeded(err) == true
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/llmgateway/infrastructure/ -run 'TestIsContextLengthExceeded|TestComplete_400ContextLengthExceeded' -v`
Expected: FAIL——错误类型不存在（编译错误），400 用例返回普通 `status 400` 错误。

- [ ] **Step 3: 实现 sentinel**——`errors.go` 新增：

```go
// ErrContextLengthExceeded 表示请求超出模型上下文窗口：不可恢复（重试依旧
// 报错），agent 层可感知并触发降级/下修闭环。
var ErrContextLengthExceeded = &contextLengthExceededError{msg: "context length exceeded"}

type contextLengthExceededError struct{ msg string }

func (e *contextLengthExceededError) Error() string  { return e.msg }
func (e *contextLengthExceededError) Unwrap() error  { return nil }
func (e *contextLengthExceededError) Permanent() bool { return true } // permanentMarker
func (e *contextLengthExceededError) ContextLengthExceeded() bool { return true }

// IsContextLengthExceeded 报告 err（含包装链）是否为上下文超限。
func IsContextLengthExceeded(err error) bool {
 return errors.Is(err, ErrContextLengthExceeded)
}
```

（`Permanent()` 复用既有 `permanentMarker` duck-typing——`graph/retry.go:57` 的 `isPermanent` 无需改动；`ContextLengthExceeded()` 是 agent 层探测协议。**消费方不得 import llmgateway 包**——agent 的 application/infrastructure 通过本地 duck-typing 识别（`interface{ ContextLengthExceeded() bool }` + `errors.As`，Task 8/9 各自定义本地副本，与 `permanentMarker` 同模式）。）

- [ ] **Step 4: 协议层识别**——`openai_compat.go` 的 400 处理处（当前 `statusCode != 200` 分支内、读取 body 之后）：

```go
// 400 context_length_exceeded 语义化：重试不可恢复，标记永久错误
// 供 agent 层感知降级。
if statusCode == http.StatusBadRequest && isContextLengthBody(body) {
 lastErr = fmt.Errorf("%s: %w", cfg.BaseURL, ErrContextLengthExceeded)
 return nil, lastErr
}
```

配套 helper（同文件或 errors.go）：

```go
// isContextLengthBody 探测 OpenAI-compat 400 响应体的 error.code /
// error.message 是否标记上下文超限。
func isContextLengthBody(body []byte) bool {
 if len(body) == 0 || !bytes.Contains(body, []byte("context_length")) {
  return false
 }
 var payload struct {
  Error struct {
   Code    string `json:"code"`
   Message string `json:"message"`
  } `json:"error"`
 }
 if err := json.Unmarshal(body, &payload); err != nil {
  return false
 }
 code := strings.ToLower(payload.Error.Code)
 return code == "context_length_exceeded" ||
  strings.Contains(strings.ToLower(payload.Error.Message), "context_length_exceeded") ||
  strings.Contains(strings.ToLower(payload.Error.Message), "maximum context length")
}
```

（插入位置保持既有 `statusCode != 200 → isRetryableHTTPStatus → backoff` 控制流；此分支放在 `lastErr = fmt.Errorf(...)` 赋值前。注意该函数现有 400 处理已读取 body——若读取的是 `io.ReadAll` 后直接丢弃，改为先复用此判定。字节级 `bytes.Contains("context_length")` 前置过滤避免无谓 json.Unmarshal。）

- [ ] **Step 5: 运行确认通过**

Run: `go test -short ./internal/llmgateway/infrastructure/`
Expected: PASS 全量。参数校验类 400（code 非 context_length）仍走原 `!isRetryable → 直接返回` 路径（Task 9 确认不降级）。

- [ ] **Step 6: 提交**

```bash
git add internal/llmgateway/infrastructure/errors.go internal/llmgateway/infrastructure/openai_compat.go internal/llmgateway/infrastructure/errors_test.go internal/llmgateway/infrastructure/openai_compat_test.go
git commit -m "feat(llmgateway): context_length_exceeded 语义化错误与识别"
```

---

### Task 3: vendor 窗口表——前缀族匹配 + 导出

**Files:**

- Modify: `internal/llmgateway/infrastructure/model_catalog.go`
- Test: `internal/llmgateway/infrastructure/model_catalog_test.go`（若不存在则新建）

**Interfaces:**

- Consumes: 现有 `modelCatalog` map（已覆盖 OpenAI/Anthropic/Qwen/DeepSeek/GLM/Moonshot/Mistral/Yi 主流族，勿重建）
- Produces: `LookupModelSpec(name string) (contextWindow, maxOutputTokens int)`——导出；exact 命中 → 前缀族命中（如 `deepseek-v4-flash` → deepseek 族窗口）→ 0/0。Task 4（窗口）与 Task 5（outputReserve）共同消费。

- [ ] **Step 1: 写失败测试**（`model_catalog_test.go`）：

```go
func TestLookupModelSpec(t *testing.T) {
 cases := []struct {
  name string
  want int // context window; 0 = unknown
 }{
  {name: "exact match qwen-plus-latest", want: 131072},
  {name: "exact case insensitive GPT-4o", want: 128000},
  // 前缀族匹配：带版本/尺寸后缀的模型命中族窗口
  {name: "prefix family deepseek-v4-flash", want: 65536},
  {name: "prefix family qwen3-max-202508", want: 131072},
  {name: "prefix family glm-5-air", want: 128000},
  // 未知模型
  {name: "unknown model", want: 0},
  {name: "empty", want: 0},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   got, _ := LookupModelSpec(tc.name)
   if got != tc.want {
    t.Fatalf("LookupModelSpec(%q) window = %d, want %d", tc.name, got, tc.want)
   }
  })
 }
}

func TestLookupModelSpec_ReturnsMaxOutput(t *testing.T) {
 _, maxOut := LookupModelSpec("qwen-max")
 if maxOut != 8192 {
  t.Fatalf("qwen-max maxOut = %d, want 8192", maxOut)
 }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/llmgateway/infrastructure/ -run TestLookupModelSpec -v`
Expected: FAIL——`LookupModelSpec` 未定义（编译失败）；prefix 用例因现 `lookupModelSpec` 是私有 exact 匹配。

- [ ] **Step 3: 实现**——`model_catalog.go` 新增（保留现有私有 `lookupModelSpec` 不动，新导出函数包装）：

```go
// familyPrefixes 是模型族 → 静态窗口的回退键（Spec D8：只覆盖主流族）。
// 带版本/尺寸后缀的模型（如 deepseek-v4-flash、qwen3-max-202508）
// 通过前缀匹配命中族窗口；族窗口取该族主流模型的 context_window。
var familyPrefixes = []struct {
 prefix  string
 window  int
 maxOut  int
}{
 {prefix: "deepseek", window: 65536, maxOut: 8192},
 {prefix: "qwen", window: 131072, maxOut: 8192},
 {prefix: "glm", window: 128000, maxOut: 4096},
 {prefix: "gpt-", window: 128000, maxOut: 16384},
 {prefix: "gpt4", window: 128000, maxOut: 16384},
 {prefix: "claude", window: 200000, maxOut: 16384},
 {prefix: "moonshot", window: 128000, maxOut: 4096},
 {prefix: "mistral", window: 128000, maxOut: 4096},
 {prefix: "yi-", window: 32768, maxOut: 4096},
}

// LookupModelSpec 返回模型的静态能力（上下文窗口 + 最大输出 token）。
// 匹配顺序：全名 exact（大小写不敏感）→ 前缀族 → 0/0 表示未知。
// 供 agent 执行时窗口解析与输出预留共用（wiring 注入，graph 包不直接引用）。
func LookupModelSpec(name string) (contextWindow, maxOutputTokens int) {
 if name == "" {
  return 0, 0
 }
 if cw, mo := lookupModelSpec(name); cw > 0 {
  return cw, mo
 }
 lower := toLower(name)
 for _, f := range familyPrefixes {
  if strings.HasPrefix(lower, f.prefix) {
   return f.window, f.maxOut
  }
 }
 return 0, 0
}
```

（`strings` 已在 `toLower` 中手动实现——`toLower` 注释写明"避免 import strings"；此处新增 `strings.HasPrefix` 是函数级选择，把 `import "strings"` 加入本文件即可，不删除 `toLower`（既有调用点保留）。若维护者倾向零 import，可改 `len(f.prefix) <= len(lower) && lower[:len(f.prefix)] == f.prefix`，两者等价——计划选 strings 直读。）

- [ ] **Step 4: 运行确认通过**

Run: `go test -short ./internal/llmgateway/infrastructure/`
Expected: PASS 全量（现有 ListModels 相关测试走私有 `lookupModelSpec`，不受影响）。

- [ ] **Step 5: 提交**

```bash
git add internal/llmgateway/infrastructure/model_catalog.go internal/llmgateway/infrastructure/model_catalog_test.go
git commit -m "feat(llmgateway): vendor 模型能力表前缀族匹配与导出"
```

---

### Task 4: 窗口两阶段解析（执行时动态解析）

**Files:**

- Create: `internal/agent/application/graph/window.go`
- Modify: `internal/agent/application/agent_service.go`（删 2292-2305 的 `deriveMaxContextTokens`；删 Create:256 与 buildUpdateConfig:643 固化调用；Execute/ExecuteStream 构造 `agentExecContext` 处改为执行时解析）
- Modify: `internal/agent/application/agent.go`（`ExecutionConfig` 加 `WindowSource string`；`agentExecutionAttributes` 加 trace attr）
- Modify: `api/wiring/agent.go:267-287`（`AgentServiceDeps` 注入 `VendorWindowLookup func(string) (int, int)`）
- Modify: `pkg/constants/agent.go`（删 `DefaultAgentContextTokensCeiling`=32768；加 `MaxContextWindowTokens`=1_048_576、`MinContextWindowTokens`=2000）
- Test: `internal/agent/application/graph/window_test.go`

**Interfaces:**

- Consumes: `agentport.ModelContextProvider.GetChatModelContextWindow(ctx, tenantID, model)`（已有）；`LookupModelSpec`（Task 3）；`constants.DefaultContextWindowRatio`（0.85，已有）
- Produces:
  - `type WindowSource string`；常量 `WindowExplicit`/`WindowRegistry`/`WindowVendorTable`/`WindowFallback`
  - `func ResolveModelWindow(ctx, tenantID, model string, provider agentport.ModelContextProvider, vendor func(string) (int, int)) (window int, source WindowSource)`——阶段 A：registry > vendor 表 > 0/fallback
  - `func ResolveAgentWindow(modelWindow int, explicit int) (window int, source WindowSource)`——阶段 B：显式+已知 → clamp[Min, w×0.85]；显式+UNKNOWN → 显式原值（D7，不 clamp）；未配置+已知 → w×0.85；全空 → `DefaultAgentContextTokens`(8000)。返回值 source 供 trace 与 WARN 日志。
  - `AgentServiceDeps.VendorWindowLookup func(string) (int, int)`（wiring 注入 `llmgateway.LookupModelSpec`）

- [ ] **Step 1: 写失败测试**（`window_test.go`）：

```go
func TestResolveModelWindow(t *testing.T) {
 // provider 桩：registry 命中 / registry 未知 / provider 为 nil
 // vendor 桩：vendor 命中 / vendor 未知
 cases := []struct {
  name     string
  provider func(context.Context, string, string) (int, error)
  vendor   func(string) (int, int)
  want     int
  wantSrc  WindowSource
 }{
  {name: "registry wins", provider: func(...) (int, error) { return 200000, nil }, vendor: nil, want: 200000, wantSrc: WindowRegistry},
  {name: "vendor table fallback",
   provider: func(...) (int, error) { return 0, nil },
   vendor:   func(string) (int, int) { return 131072, 8192 }, want: 131072, wantSrc: WindowVendorTable},
  {name: "provider error degrades to vendor", ...},
  {name: "both unknown", provider: ..., vendor: func(string) (int, int) { return 0, 0 }, want: 0, wantSrc: WindowFallback},
  {name: "nil provider", provider: nil, vendor: func(string) (int, int) { return 0, 0 }, want: 0, wantSrc: WindowFallback},
 }
 // 逐用例 t.Run；provider/vendor 调用次数断言（registry 命中时 vendor 不被调用）
}

func TestResolveAgentWindow(t *testing.T) {
 const ratio = constants.DefaultContextWindowRatio
 cases := []struct {
  name      string
  modelWin  int // 0 = UNKNOWN
  explicit  int // 0 = 未配置
  want      int
  wantSrc   WindowSource
 }{
  // 显式 + 已知窗口 → clamp 到 [Min, w×0.85]
  {name: "explicit within clamp", modelWin: 200000, explicit: 30000, want: 30000, wantSrc: WindowExplicit},
  {name: "explicit above ratio cap clamps", modelWin: 200000, explicit: 200000, want: int(200000 * ratio), wantSrc: WindowExplicit},
  {name: "explicit below min clamps", modelWin: 131072, explicit: 500, want: constants.MinContextWindowTokens, wantSrc: WindowExplicit},
  // 显式 + UNKNOWN 窗口 → 显式原值生效（D7：未知假设无权压制显式配置）
  {name: "explicit unknown window not clamped", modelWin: 0, explicit: 40000, want: 40000, wantSrc: WindowExplicit},
  // 未配置 + 已知 → w×0.85
  {name: "derived from known window", modelWin: 131072, explicit: 0, want: int(131072 * ratio), wantSrc: WindowRegistry},
  // 全空 → 保守默认 8000
  {name: "fallback default", modelWin: 0, explicit: 0, want: constants.DefaultAgentContextTokens, wantSrc: WindowFallback},
 }
 // 逐用例 t.Run；want 计算用常量不做内联魔法数字
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/agent/application/graph/ -run 'TestResolveModelWindow|TestResolveAgentWindow' -v`
Expected: FAIL——`window.go` 不存在（编译失败）。

- [ ] **Step 3: 实现**（`window.go`，行宽 ≤120、单一职责）：

```go
package graph

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

// WindowSource 标记一次执行窗口解析的最终来源（Spec 第 1 节）。
type WindowSource string

const (
 WindowExplicit    WindowSource = "explicit"     // 管理员显式配置
 WindowRegistry    WindowSource = "registry"     // 模型 registry context_window
 WindowVendorTable WindowSource = "vendor_table" // 内置厂商静态表
 WindowFallback    WindowSource = "fallback"     // 保守默认 8000
)

// ResolveModelWindow 解析模型真实窗口（阶段 A）：
// registry context_window > 0 → vendor 静态表 → 0 = UNKNOWN。
// vendor 通过注入函数访问（wiring 适配 llmgateway），graph 包不跨层依赖。
func ResolveModelWindow(
 ctx context.Context,
 tenantID, model string,
 provider port.ModelContextProvider,
 vendor func(string) (int, int),
) (window int, source WindowSource) {
 if provider != nil {
  if cw, err := provider.GetChatModelContextWindow(ctx, tenantID, model); err == nil && cw > 0 {
   return cw, WindowRegistry
  }
 }
 if vendor != nil {
  if cw, _ := vendor(model); cw > 0 {
   return cw, WindowVendorTable
  }
 }
 return 0, WindowFallback
}

// ResolveAgentWindow 解析 agent 执行窗口（阶段 B）。explicit=0 表示未配置。
// clamp 上限 w×0.85 只在模型窗口已知时适用；UNKNOWN 时显式值直接生效
// （D7：显式配置是最可信信息，未知假设无权压制它）。
func ResolveAgentWindow(modelWindow, explicit int) (window int, source WindowSource) {
 known := modelWindow > 0
 switch {
 case explicit > 0 && known:
  window = clampWindow(explicit, int(float64(modelWindow)*constants.DefaultContextWindowRatio))
  return window, WindowExplicit
 case explicit > 0:
  return explicit, WindowExplicit
 case known:
  return int(float64(modelWindow) * constants.DefaultContextWindowRatio), WindowRegistry
 default:
  return constants.DefaultAgentContextTokens, WindowFallback
 }
}

// clampWindow 将显式窗口约束到 [MinContextWindowTokens, ratioCap]。
func clampWindow(explicit, ratioCap int) int {
 if explicit < constants.MinContextWindowTokens {
  return constants.MinContextWindowTokens
 }
 if ratioCap > 0 && explicit > ratioCap {
  return ratioCap
 }
 return explicit
}
```

- [ ] **Step 4: 常量变更**——`pkg/constants/agent.go`：

```go
// 删：
// DefaultAgentContextTokensCeiling = 32768  // 模型无关 cap，已被 MaxContextWindowTokens 替代
// 增：
// MaxContextWindowTokens 是解析窗口的 1M 硬 ceiling（Spec 第 1 节）。
MaxContextWindowTokens = 1_048_576
// MinContextWindowTokens 是显式配置 clamp 的下界。
MinContextWindowTokens = 2_000
```

（`ResolveAgentWindow` 的 clamp 上界由 w×0.85 决定，1M ceiling 由调用侧执行（见 Step 5 的 `resolveExecutionWindow`）。先删除常量，编译器会暴露遗留引用——`deriveMaxContextTokens` 删除后 `DefaultAgentContextTokensCeiling` 引用归零。）

- [ ] **Step 5: 删固化 + 执行时解析**——`agent_service.go`：

1. 删除 `deriveMaxContextTokens`（2292-2305）。
2. 删除 Create（256）与 buildUpdateConfig（643）中 `deriveMaxContextTokens` 调用——这两处原把推导结果写进 `AgentConfig.MaxContextTokens` 固化；删除后 `MaxContextTokens` 只存管理员显式值（0=未配置），语义变为"执行时解析"的输入。
3. 新增执行时解析函数（放在 Execute/ExecuteStream 附近）：

```go
// resolveExecutionWindow 执行时解析 agent 窗口（Spec 第 1 节两阶段），
// 替代 Create/Update 的一次性固化：管理员后补配置下次执行立即生效。
// 返回 (解析窗口, 来源)；来源为 vendor_table/fallback 时 WARN。
func (s *AgentService) resolveExecutionWindow(
 ctx context.Context,
 tenantID, model string,
 explicit int,
) (int, graph.WindowSource) {
 modelWin, src := graph.ResolveModelWindow(
  ctx, tenantID, model, s.deps.ModelContextProvider, s.deps.VendorWindowLookup,
 )
 if modelWin > constants.MaxContextWindowTokens {
  modelWin = constants.MaxContextWindowTokens
 }
 window, agentSrc := graph.ResolveAgentWindow(modelWin, explicit)
 if src == graph.WindowVendorTable || src == graph.WindowFallback {
  s.deps.Logger.Warn("agent: model window resolved from fallback source",
   zap.String("model", model), zap.String("source", string(src)),
   zap.Int("model_window", modelWin), zap.Int("window", window))
 }
 _ = agentSrc // 窗口值已含来源语义；agentSrc 供 trace（Task 4 Step 6）
 return window, agentSrc
}
```

（`agentSrc` 返回给调用方写入 `ExecutionConfig.WindowSource`；`src`（模型窗口来源）与 `agentSrc`（执行窗口来源）中 vendor/fallback WARN 以模型窗口来源为准——执行窗口来源 explicit 时不应 WARN。实现时确认 `graph` import 别名：`agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"`。）

1. Execute/ExecuteStream 构造 `agentExecContext` 处：`maxContextTokens` 字段由 `cfg.MaxContextTokens` 直读改为调用 `resolveExecutionWindow`（显式配置 + 模型窗口 → 解析值），并把 `cfg.WindowSource` 回填。

- [ ] **Step 6: trace attribute**——`agent.go`：

`ExecutionConfig` 加字段：

```go
// WindowSource 记录本次执行窗口解析来源（window_source trace 用）。
WindowSource string
```

`agentExecutionAttributes`（1198）无条件追加（非 CaptureParameters gated——Spec 要求窗口来源始终可观测）：

```go
attribute.String("stratum.window_source", cfg.WindowSource),
attribute.Int("stratum.window_tokens", maxContextTokens),
```

（`maxContextTokens` 参数已是解析后值——确认 Execute 路径传入的即 Step 5 解析结果。）

- [ ] **Step 7: wiring 注入**——`api/wiring/agent.go` 的 `AgentServiceDeps{...}`（267-287）加：

```go
VendorWindowLookup: llmgateway.LookupModelSpec,
```

`AgentServiceDeps` 结构体（agent_service.go 顶部）加字段：

```go
// VendorWindowLookup 解析内置厂商静态能力表（窗口 + 最大输出）。
// 由 wiring 注入 llmgateway.LookupModelSpec；nil 时回退链跳过 vendor 层。
VendorWindowLookup func(string) (int, int)
```

- [ ] **Step 8: 运行确认通过**

Run: `go vet ./internal/agent/... ./api/wiring/... && go test -short ./internal/agent/application/graph/ -run 'TestResolveModelWindow|TestResolveAgentWindow' -v`
Expected: PASS。再 `go test -short ./internal/agent/...` 全量——`agent_service_test.go` 若有断言依赖 Create 固化的 MaxContextTokens 值，核实后修正（新语义：MaxContextTokens=0 表示未配置，执行时解析）。`resolve_effective_parameters_test.go` 若引用 `deriveMaxContextTokens` 一并清理。

- [ ] **Step 9: 提交**

```bash
git add internal/agent/application/graph/window.go internal/agent/application/graph/window_test.go internal/agent/application/agent_service.go internal/agent/application/agent.go api/wiring/agent.go pkg/constants/agent.go
git commit -m "feat(agent): 窗口两阶段解析替代 Create/Update 一次性固化"
```

---

### Task 5: 预算账本——统一窗口与压缩的预算来源

**Files:**

- Create: `internal/agent/application/graph/budget.go`
- Modify: `internal/agent/application/context_budget.go:60-164`
- Modify: `internal/agent/application/graph/compaction.go:110-151`（阈值改用 history 配额）
- Modify: `internal/agent/application/graph/react_llm.go:407`（`fitToolsToContextBudget` 用 ToolsCap 代替 budget）
- Modify: `pkg/constants/agent.go`（`DefaultOutputReserveTokens`、`DefaultToolsBudgetRatio`、`DefaultFixedHeadRatio`）
- Test: `internal/agent/application/graph/budget_test.go`

**Interfaces:**

- Consumes: `MaxContextWindowTokens`（Task 4 常量）；`LookupModelSpec` 第二返回值（输出上限）
- Produces:
  - `type Budget struct { Window, Usable, OutputReserve, FixedHeadCap, ToolsCap, HistoryCap, TaskHint int }`
  - `func ComputeBudget(window, outputReserve int, safetyRatio float64) Budget`——usable = window − safetyReserve(window×ratio) − outputReserve；fixedHead ≤20% usable；tools ≤20% usable；history = usable − fixedHead − tools − taskHint；1M ceiling 在此 enforce。
  - `func (b Budget) History() int`——压缩阈值基准配额

- [ ] **Step 1: 写失败测试**（`budget_test.go`）：

```go
func TestComputeBudget(t *testing.T) {
 // window 200000, safetyRatio 0.8(默认), outputReserve 8192
 // usable = 200000 - 200000*0.8 - 8192 = 31808
 // fixedHead = 20% usable = 6361; tools = 6361; history = usable - 2*6361 - taskHint
 cases := []struct {
  name     string
  window   int
  reserve  int
  ratio    float64
  wantUsable int
  wantFixedHead int
  wantTools int
 }{
  {name: "qwen-max window", window: 131072, reserve: 8192, ratio: 0.8,
   wantUsable: 131072 - int(131072*0.8) - 8192},
  // 断言配额比例：fixedHead ≤ 20% usable、tools ≤ 20% usable
 }
 // 断言 history = usable - fixedHead - tools（taskHint 单列）
 // 断言 window > MaxContextWindowTokens 时 clamp
}

func TestComputeBudget_HistoryIsolation(t *testing.T) {
 // 工具 token 不再压垮 history：同样 window，tools 配额固定 20%，
 // history 配额 = usable - fixedHead - tools，独立于工具实际 token 数。
 // 断言 History() 不随 tools token 变化（配额而非实际占用）
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/agent/application/graph/ -run TestComputeBudget -v`
Expected: FAIL——`budget.go` 不存在。

- [ ] **Step 3: 实现**（`budget.go`）：

```go
package graph

import "github.com/byteBuilderX/stratum/pkg/constants"

// Budget 是一次执行的上下文预算账本（Spec 第 2 节）：window → usable →
// 四配额。一次执行一个快照，初始组装与 ReAct 循环共享同一来源。
type Budget struct {
 Window       int // 阶段 B 解析结果（1M ceiling 已 clamp）
 Usable       int // window − safetyReserve − outputReserve
 FixedHeadCap int // system + memory 配额（20% usable）
 ToolsCap     int // 工具定义配额（20% usable）
 HistoryCap   int // 可压缩区（= usable − fixedHead − tools − task）
}

// ComputeBudget 计算执行预算。safetyRatio 是 registry 参数
// agent.compaction_safety_ratio（0 = 用 constants 默认）。
// outputReserve 是主模型输出预留（Task 3 的 maxOut / 显式 max_tokens / 常量）。
func ComputeBudget(window, outputReserve int, safetyRatio float64) Budget {
 if window > constants.MaxContextWindowTokens {
  window = constants.MaxContextWindowTokens
 }
 ratio := safetyRatio
 if ratio <= 0 || ratio >= 1 {
  ratio = constants.LoopCompactionSafetyRatio
 }
 usable := window - int(float64(window)*ratio) - outputReserve
 if usable < 0 {
  usable = 0
 }
 fixedHead := int(float64(usable) * constants.DefaultFixedHeadRatio)
 tools := int(float64(usable) * constants.DefaultToolsBudgetRatio)
 history := usable - fixedHead - tools
 return Budget{
  Window: window, Usable: usable,
  FixedHeadCap: fixedHead, ToolsCap: tools, HistoryCap: history,
 }
}
```

`pkg/constants/agent.go` 增：

```go
// DefaultFixedHeadRatio 是 system+memory 的预算配额比例（Spec 第 2 节）。
DefaultFixedHeadRatio = 0.2
// DefaultToolsBudgetRatio 是工具定义的预算配额比例（Spec 第 2 节）。
DefaultToolsBudgetRatio = 0.2
// DefaultOutputReserveTokens 是主模型输出预留的保守默认（无显式 max_tokens
// 且 vendor 表未知时的兜底）。
DefaultOutputReserveTokens = 4_096
```

- [ ] **Step 4: 统一 context_budget.go**——`BuildContextMessagesWithCompaction`（60-164）：

- 签名不变，内部预算计算改为调用 `ComputeBudget`；`maxTokens`（调用侧传入的解析窗口）作为 `Budget.Window` 输入。
- outputReserve 来源：显式 `cfg.MaxTokens`（>0）> vendor 表 `maxOut` > `DefaultOutputReserveTokens`。`BuildContextMessagesWithCompaction` 增加参数或读取调用侧传入值——选择：新增可选参数 `outputReserve int`（0 = 自动链）。调用点 `agent.go:544`（executeReAct）与 `:664`（executePlanning）传入。
- system+memory 配额用 `FixedHeadCap`（保留 min 200t 起步语义：`min(systemTokens, 200)` 向上兼容现状）；`history` 区计算用 `HistoryCap`。
- 保留现有 `summaryReserve` 与 overflow 压缩候选逻辑不变（只换预算输入）。

- [ ] **Step 5: 阈值修正**——`compaction.go`：

`compactLoopMessagesWithPolicy`（110-138）内 `compactionThreshold(budget, reservedTokens, correction, safetyRatio)`（143-151）调用处的 `budget` 参数改为 `HistoryCap`（即 `ComputeBudget(...).HistoryCap`）。签名不变，调用方传值变化：

```go
// 阈值基于预算账本的 history 配额：工具 token 走 ToolsCap 独立配额，
// 不再压垮可压缩区（Spec 第 2 节根因修复）。
threshold := compactionThreshold(budget.HistoryCap, reservedTokens, correction, safetyRatio)
```

（`budget` 由 `ReActState.MaxContextTokens` 经 `ComputeBudget` 派生——`MaxContextTokens` 语义不变仍为解析窗口；`compactLoopMessagesWithPolicy` 内部增加一次 `ComputeBudget` 调用，或由调用方传入预计算 `Budget`——选择：调用方传 `Budget` 结构体，避免二次计算。函数签名改为 `compactLoopMessagesWithPolicy(ctx, s, budget Budget, ...)`，同步修改 `react_llm.go` 调用点。）

- [ ] **Step 6: tools 配额**——`react_llm.go:407` `fitToolsToContextBudget(tools, messages, budget, ...)`：`budget` 参数改为 `ToolsCap`（调用点 `makeLLMNode` 内从预计算 `Budget` 取 `ToolsCap`）。函数内部"从预算中 fit 工具定义"逻辑不变——它不再从 history 配额里扣。

- [ ] **Step 7: 运行确认通过**

Run: `go test -short ./internal/agent/...`
Expected: PASS。`compaction_test.go` 现有阈值用例若直接传 `MaxContextTokens` 作 budget，改传 `ComputeBudget(...).HistoryCap` 并核对断言（这是测试意图的同步，不是放宽）。

- [ ] **Step 8: 提交**

```bash
git add internal/agent/application/graph/budget.go internal/agent/application/graph/budget_test.go internal/agent/application/context_budget.go internal/agent/application/graph/compaction.go internal/agent/application/graph/react_llm.go pkg/constants/agent.go
git commit -m "feat(agent): 执行级预算账本统一窗口与压缩预算来源"
```

---

### Task 6: 压缩冷却

**Files:**

- Modify: `internal/agent/application/graph/react_state.go`（ReActState 加 `CompactionCooldownSec int`、`LastCompactionAt time.Time`）
- Modify: `internal/agent/application/graph/compaction.go`
- Modify: `internal/agent/application/agent.go`（`WithCompactionCooldownSec` ExecutionOption；`buildReActInitState` 接线）
- Modify: `internal/agent/application/agent_service.go`（`resolveEffectiveParameters` 加 key）
- Modify: `internal/parameters/domain/registry.go`（`agent.compaction_cooldown_sec`）
- Modify: `internal/agent/domain/agent.go`（AgentConfig 加 `CompactionCooldownSec int`）
- Modify: `pkg/constants/agent.go`（`DefaultCompactionCooldown`）
- Test: `internal/agent/application/graph/compaction_test.go` 追加

**Interfaces:**

- Consumes: `ReActState`（Task 5 后已有 Budget）
- Produces: `WithCompactionCooldownSec(sec int) ExecutionOption`；registry key `agent.compaction_cooldown_sec`（0=unset）；`constants.DefaultCompactionCooldown = 10 * time.Second`

- [ ] **Step 1: 写失败测试**（`compaction_test.go` 追加）：

```go
func TestCompactLoop_CooldownSuppressesRepeat(t *testing.T) {
 // 构造超限状态，首次压缩触发（compactor 记录调用次数）；
 // 设置 LastCompactionAt = time.Now()（冷却内），再次超限检查
 // 不得再次调用 LLM compactor——走截断路径。
 // 断言 compactor 调用次数 == 1
}

func TestCompactLoop_CooldownExpiredAllowsAgain(t *testing.T) {
 // 同上但 LastCompactionAt = time.Now().Add(-DefaultCompactionCooldown - time.Second)
 // 断言 compactor 调用次数 == 2
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/agent/application/graph/ -run TestCompactLoop_Cooldown -v`
Expected: FAIL——无冷却逻辑（每次超限都调 compactor）。

- [ ] **Step 3: 实现**：

`react_state.go` ReActState 加：

```go
// CompactionCooldownSec 是压缩冷却窗口（秒）：一次执行内压缩触发后，
// 冷却期内超限只截断不重复触发同步 LLM 摘要。0 = 用常量默认。
CompactionCooldownSec int
// LastCompactionAt 是最近一次 LLM 压缩完成时间；零值表示未压缩过。
LastCompactionAt time.Time
```

`compaction.go`——`compactLoopMessagesWithPolicy` 的 lazy 检查命中后、调用 compactor 前：

```go
// 冷却期内跳过 LLM 压缩，走截断兜底（Spec 第 4 节）：压缩是
// 尽力而为机制，同步 LLM 摘要不应每步触发。
if s.CompactionCooldownSec > 0 && !s.LastCompactionAt.IsZero() &&
 time.Since(s.LastCompactionAt) < time.Duration(s.CompactionCooldownSec)*time.Second {
 return evictUntilFit(ctx, budget, messages, reservedTokens, correction, safetyRatio)
}
```

压缩成功后（`summarizeMiddle` 返回处）：

```go
s.LastCompactionAt = time.Now()
```

（`time` import 已存在于 compaction.go。`CompactionCooldownSec` 0 时行为与现状一致——用 `buildReActInitState` 默认填 `constants.DefaultCompactionCooldown` 秒数：`ec.cfg.CompactionCooldownSec` 0 → 填默认。选择：**默认值在 buildReActInitState 解析**，与 `CompactionSafetyRatio` 0=default 语义一致。）

`agent.go`：

```go
// WithCompactionCooldownSec sets the in-loop compaction cooldown in seconds.
// 0 = default constant.
func WithCompactionCooldownSec(sec int) ExecutionOption {
 return func(cfg *ExecutionConfig) {
  cfg.CompactionCooldownSec = sec
 }
}
```

`agent_service.go` `resolveEffectiveParameters`（1869-1907）declared map 与解析块加：

```go
"agent.compaction_cooldown_sec": cfg.CompactionCooldownSec,
...
if v, ok := effective["agent.compaction_cooldown_sec"].(int64); ok {
 options = append(options, WithCompactionCooldownSec(int(v)))
}
```

`registry.go` `registerAgentParams` 追加定义（默认 int64(0)，`Optimizable: false`——冷却非评测维度）：

```go
{
 Key: "agent.compaction_cooldown_sec", Scope: ScopeResource, Category: "agent",
 DisplayName: "压缩冷却(秒)", Description: "压缩触发后的冷却窗口,0 表示默认常量",
 ValueType: TypeInt, Default: int64(0),
 VisualHint: VisualHint{Control: ControlSlider, Min: f(0), Max: f(120), Step: f(5), Unit: "s"},
 Optimizable: false,
},
```

`domain/agent.go` AgentConfig 加：

```go
// CompactionCooldownSec overrides the in-loop compaction cooldown.
// 0 = default constant.
CompactionCooldownSec int
```

`pkg/constants/agent.go`：

```go
// DefaultCompactionCooldown 是一次执行内压缩触发后的冷却窗口（Spec 第 4 节，
// 建议默认 10s，实现时按压测验证）。registry 参数 agent.compaction_cooldown_sec
// 覆盖它（0 = 本常量）。
DefaultCompactionCooldown = 10 * time.Second
```

`buildReActInitState`（agent.go:724）加：

```go
CompactionCooldownSec: ec.cfg.CompactionCooldownSec,
```

（0 → 冷却禁用；若产品默认要启用，在 buildReActInitState 里 0 时填 `int(constants.DefaultCompactionCooldown.Seconds())`——**计划选默认启用**：spec 根因就是无冷却，0 默认禁用等于没修。实现时在 `buildReActInitState` 一行解析：`cooldown := ec.cfg.CompactionCooldownSec; if cooldown <= 0 { cooldown = int(constants.DefaultCompactionCooldown.Seconds()) }`，与 `CompactionSafetyRatio` 的 0=default 处理一致。）

- [ ] **Step 4: 运行确认通过**

Run: `go test -short ./internal/agent/... ./internal/parameters/...`
Expected: PASS。`resolve_effective_parameters_test.go` 有全 key 覆盖测试（`TestPromptResolver_resolveAllCoversEveryKey` 同类模式）——新 key 注册后若测试枚举断言全 key 列表，需同步。

- [ ] **Step 5: 提交**

```bash
git add internal/agent/application/graph/react_state.go internal/agent/application/graph/compaction.go internal/agent/application/agent.go internal/agent/application/agent_service.go internal/parameters/domain/registry.go internal/agent/domain/agent.go pkg/constants/agent.go
git commit -m "feat(agent): 压缩冷却窗口，消除每步同步 LLM 摘要"
```

---

### Task 7: 成本预算（token 总量，图级检查点）

**Files:**

- Create: `internal/agent/application/graph/cost_budget.go`
- Modify: `internal/agent/domain/agent.go`（AgentConfig 加 `MaxTokensPerExecution int`）
- Modify: `internal/agent/application/agent.go`（`WithMaxTokensPerExecution`；`buildReActInitState` 接线；`collectGraphResult` 终止标记）
- Modify: `internal/agent/application/agent_service.go`（`resolveEffectiveParameters` 加 key）
- Modify: `internal/parameters/domain/registry.go`（`agent.max_tokens_per_execution`）
- Modify: `internal/agent/application/graph/react_state.go`（ReActState 加 `MaxTokensPerExecution int`、`TerminatedBy string`）
- Modify: `internal/agent/application/graph/react_llm.go`（`makeLLMNode` 检查点；`nodeLLM` 条件边）
- Test: `internal/agent/application/graph/budget_test.go` 追加

**Interfaces:**

- Consumes: `ReActState.TotalTokens`（已有，ledger.Record 累计）；`Budget`（Task 5）
- Produces: `WithMaxTokensPerExecution(tokens int) ExecutionOption`；registry key `agent.max_tokens_per_execution`（0=不设限）；终止语义：`ReActState.TerminatedBy == "cost_budget"` → 条件边 END → `executeReAct` 收尾读 `finalState.TerminatedBy` 写 `result`（业务终止非错误）

- [ ] **Step 1: 写失败测试**（`budget_test.go` 追加）：

```go
func TestCostBudget_ExceededTerminates(t *testing.T) {
 // 用 BuildReActGraph + 桩 gateway：模拟每次 LLM 调用返回 usage
 // Total 超过 MaxTokensPerExecution 预算，图执行后：
 // finalState.TerminatedBy == "cost_budget"
 // 图不返回错误（业务终止非错误路径）
}
```

（桩 gateway：`port.CapabilityGateway` 实现——参照 `graph/react_test.go` 现有桩模式；usage 通过 `TokenRecorder`/`port.TokenUsage` 注入。）

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/agent/application/graph/ -run TestCostBudget_ExceededTerminates -v`
Expected: FAIL——无终止语义（图跑满步数或报错）。

- [ ] **Step 3: 实现**：

`cost_budget.go`：

```go
package graph

// CostBudgetTerminated 是成本预算超限的业务终止标记（Spec 第 3 节）。
// 属业务终止而非错误：返回已产出部分结果，trace terminated_by=cost_budget。
const CostBudgetTerminated = "cost_budget"

// budgetExceeded 报告累计 token 是否超过执行预算（0 = 不设限）。
func budgetExceeded(total, maxTokensPerExecution int) bool {
 return maxTokensPerExecution > 0 && total > maxTokensPerExecution
}
```

`react_state.go` 加：

```go
// MaxTokensPerExecution 是本次执行的累计 LLM token 预算（0 = 不设限）。
// 图级每次 LLM 调用后累计检查，超限终止循环（Spec 第 3 节）。
MaxTokensPerExecution int
// TerminatedBy 标记业务终止原因（如 CostBudgetTerminated）；空 = 正常结束。
TerminatedBy string
```

`react_llm.go`：

1. `nodeLLM` 条件边（react_llm.go:29-38）追加：

```go
if s.TerminatedBy != "" {
 return []string{END}
}
```

1. `makeLLMNode` 内 `routeLLM` 成功后（`appendLLMResponse`/ledger 累计处）：

```go
// 成本预算检查点：每次 LLM 调用后累计（Spec 第 3 节）。
if budgetExceeded(s.TotalTokens, s.MaxTokensPerExecution) {
 s.TerminatedBy = CostBudgetTerminated
 return s, nil
}
```

（`TotalTokens` 由 `TokenRecorder.Record` 返回值累计——确认 `makeLLMNode` 现状的累计写法后在此追加检查；`TotalTokens` 已在 `ReActState` 且 `collectGraphResult` 已消费。）

`agent.go`：

```go
// WithMaxTokensPerExecution sets the execution-wide LLM token budget.
// 0 = unlimited (gateway/provider default).
func WithMaxTokensPerExecution(tokens int) ExecutionOption {
 return func(cfg *ExecutionConfig) {
  cfg.MaxTokensPerExecution = tokens
 }
}
```

`buildReActInitState`（agent.go:724）加：

```go
MaxTokensPerExecution: ec.cfg.MaxTokensPerExecution,
```

`collectGraphResult`（agent.go:804）加：

```go
if finalState.TerminatedBy == CostBudgetTerminated {
 result.TerminatedBy = finalState.TerminatedBy
}
```

`AgentResult`（domain/agent.go 或 application）加字段：

```go
// TerminatedBy 记录业务终止原因（如 cost_budget）；空 = 正常完成。
TerminatedBy string
```

`agent_service.go` `resolveEffectiveParameters` 加：

```go
"agent.max_tokens_per_execution": cfg.MaxTokensPerExecution,
...
if v, ok := effective["agent.max_tokens_per_execution"].(int64); ok {
 options = append(options, WithMaxTokensPerExecution(int(v)))
}
```

`registry.go` `registerAgentParams` 追加：

```go
{
 Key: "agent.max_tokens_per_execution", Scope: ScopeResource, Category: "agent",
 DisplayName: "单次执行 Token 预算", Description: "本次执行累计 LLM token 上限,0 表示不设限",
 ValueType: TypeInt, Default: int64(0),
 VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(2000000), Step: f(10000), Unit: "tokens"},
 Optimizable: false,
},
```

`domain/agent.go` AgentConfig 加：

```go
// MaxTokensPerExecution is the execution-wide LLM token budget. 0 = unlimited.
MaxTokensPerExecution int
```

- [ ] **Step 4: 运行确认通过**

Run: `go test -short ./internal/agent/...`
Expected: PASS。`agent_service_test.go` 若断言 `resolveEffectiveParameters` 产出全 key 列表，同步新增两条。

- [ ] **Step 5: 提交**

```bash
git add internal/agent/application/graph/cost_budget.go internal/agent/application/graph/react_state.go internal/agent/application/graph/react_llm.go internal/agent/application/agent.go internal/agent/application/agent_service.go internal/agent/domain/agent.go internal/parameters/domain/registry.go
git commit -m "feat(agent): 执行级 token 成本预算，超限业务终止"
```

---

### Task 8: 压缩动态时间片 + NoPrimaryRetry/MaxCandidates

**Files:**

- Modify: `internal/agent/infrastructure/capability/history_compactor.go:44-89`
- Modify: `internal/agent/domain/port/capability.go`（`LLMCapRequest` 加字段）
- Modify: `internal/llmgateway/domain/completion.go`（`CompletionRequest` 加字段）
- Modify: `internal/llmgateway/infrastructure/gateway.go`（`invokeWithFallback`/`invokeCandidate` 生效）
- Modify: `api/wiring/agent_llm_adapter.go`（透传）
- Test: `internal/agent/infrastructure/capability/history_compactor_test.go`、`internal/llmgateway/infrastructure/fallback_test.go`

**Interfaces:**

- Consumes: `port.CapabilityGateway.Route`（压缩路径）；Task 1 的 isTransient 语义
- Produces: `port.LLMCapRequest.NoPrimaryRetry bool`、`port.LLMCapRequest.MaxCandidates int`（0=默认 3）；`domain.CompletionRequest.NoPrimaryRetry bool`、`.MaxCandidates int`；压缩路径 BudgetPolicy{Total:5s, NoPrimaryRetry:true, MaxCandidates:2}

- [ ] **Step 1: 写失败测试**

`history_compactor_test.go` 追加（若无文件则新建，参照 `capability` 包现有测试桩——`gw` 桩实现 `port.CapabilityGateway` 记录每次调用 ctx 与请求字段）：

```go
func TestCompactHistory_BudgetSlices(t *testing.T) {
 // 桩 gw：第一次 Route 失败（503 瞬态）、第二次成功。
 // 断言：两次调用的 ctx deadline 分别 ≈ 5s/3 与 剩余/2（允许 jitter 容差），
 // 且请求携带 NoPrimaryRetry=true、MaxCandidates=2。
}

func TestCompactHistory_BudgetExhaustedFailsFast(t *testing.T) {
 // 桩 gw：全部失败；断言：总耗时 ≤ 5s + 容差，返回错误
 // （链耗尽 → markPermanent → breadcrumb 语义由上层处理）
}
```

`fallback_test.go` 追加（gateway 层）：

```go
func TestInvokeWithFallback_NoPrimaryRetrySkipsImmediateRetry(t *testing.T) {
 // 主模型第一次 503；NoPrimaryRetry=true 时不得立即重试主模型，
 // 直接进入候选链；断言主模型被调用 1 次
}

func TestInvokeWithFallback_MaxCandidatesTruncates(t *testing.T) {
 // 配置 3 个候选；MaxCandidates=2 时只尝试前 2 个
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/agent/infrastructure/capability/ ./internal/llmgateway/infrastructure/ -run 'TestCompactHistory_Budget|TestInvokeWithFallback_' -v`
Expected: FAIL——字段不存在（编译失败）；现有压缩路径无时间片。

- [ ] **Step 3: 字段透传**：

`port/capability.go` `LLMCapRequest` 加：

```go
// NoPrimaryRetry 禁止 gateway 对主模型瞬态失败的立即重试（压缩路径用：
// 时间片内一次主尝试，失败直接降级候选）。0 值（false）= 默认允许一次立即重试。
NoPrimaryRetry bool
// MaxCandidates 限制 fallback 候选数量；0 = gateway 默认（3）。
MaxCandidates int
```

`llmgateway/domain/completion.go` `CompletionRequest` 加同名字段（bool + int）。

`api/wiring/agent_llm_adapter.go`：`LLMCapRequest → CompletionRequest` 映射处透传两字段。

`gateway.go`：

```go
// invokeWithFallback（194-219）：候选链按 req.MaxCandidates 截断：
candidates := chain.candidates
if req.MaxCandidates > 0 && len(candidates) > req.MaxCandidates {
 candidates = candidates[:req.MaxCandidates]
}
// invokeCandidate（226-254）：主模型立即重试条件加 NoPrimaryRetry 守卫：
if isPrimary && !req.NoPrimaryRetry && !outputStarted && isTransient(err) {
 // 立即重试 1 次
}
```

（`req` 即 `*domain.CompletionRequest`——确认 `invokeWithFallback`/`invokeCandidate` 签名中可访问；若只传 `chain`，把 `req` 一并传入。）

- [ ] **Step 4: 时间片**——`history_compactor.go` `CompactHistory`（44-89）：

```go
// BudgetPolicy 是压缩路径的时间片策略（Spec 第 4 节）：总预算 5s、
// 主模型不立即重试、最多 2 个候选。每次尝试 slice = remaining/remaining_attempts，
// 各自独立 ctx——保留 fallback 容灾但不放大用户可感知时延。
type BudgetPolicy struct {
 Total          time.Duration
 NoPrimaryRetry bool
 MaxCandidates  int
}

// DefaultCompactionBudgetPolicy 是压缩路径默认策略。
var DefaultCompactionBudgetPolicy = BudgetPolicy{
 Total:          5 * time.Second,
 NoPrimaryRetry: true,
 MaxCandidates:  2,
}
```

`CompactHistory` 内循环改造：

```go
policy := DefaultCompactionBudgetPolicy
// total 5s；attempts = 1 主 + MaxCandidates 候选
attempts := 1 + policy.MaxCandidates
remaining := policy.Total
for i := 0; i < attempts; i++ {
 slice := remaining / time.Duration(attempts-i)
 if slice <= 0 {
  slice = 1 * time.Millisecond
 }
 sliceCtx, sliceCancel := context.WithTimeout(ctx, slice)
 resp, err := gw.Route(sliceCtx, port.CapabilityRequest{
  TraceID: ..., TenantID: ..., Type: port.CapLLM,
  LLM: &port.LLMCapRequest{
   Model: model, Messages: msgs, MaxTokens: compactionMaxTokens,
   NoPrimaryRetry: policy.NoPrimaryRetry,
   MaxCandidates:  policy.MaxCandidates,
  },
 })
 sliceCancel()
 remaining -= time.Since(sliceStart)
 if err == nil {
  return resp, nil
 }
 // DeadlineExceeded（Task 1 后 isTransient=false）→ gateway 已立即停链，
 // 此处同步停（时间片耗尽继续尝试无意义）
 if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
  return nil, err
 }
 // 永久错误（含 context_length_exceeded、参数 400）→ 立即停
 if isPermanent(err) || isContextLengthExceeded(err) {
  return nil, err
 }
 // 瞬态 → 进入下一 slice
}
return nil, lastErr
```

（现状的 `compactionTimeout = min(5s, remaining/2)` 被替换；`gw.Route` 内部 gateway 链在 slice ctx 内工作。`isPermanent` 是 graph 包私有——history_compactor 在 `infrastructure/capability` 包，不能引用，也不能 import llmgateway（DDD：infrastructure 不 import 兄弟 context 的 infrastructure）。本地定义同款 duck-typing 探测，与 graph/retry.go 的 `permanentMarker` 同模式：

```go
// 与 graph/retry.go 的 permanentMarker 同模式的本地副本（DDD 跨包探测）。
type permanentMarker interface{ Permanent() bool }

// isPermanent 经 errors.As 探测永久标记。
func isPermanent(err error) bool {
 var m permanentMarker
 return errors.As(err, &m)
}

// contextLengthMarker 是 llmgateway ErrContextLengthExceeded 的跨包探测协议。
type contextLengthMarker interface{ ContextLengthExceeded() bool }

// isContextLengthExceeded 报告错误链是否含上下文超限标记。
func isContextLengthExceeded(err error) bool {
 var m contextLengthMarker
 return errors.As(err, &m)
}
```

（若该包已有 `isPermanent` 类似物，直接复用；无则加上述代码块到文件顶部。`errors` import 已存在。）

- [ ] **Step 5: 运行确认通过**

Run: `go test -short ./internal/agent/... ./internal/llmgateway/...`
Expected: PASS。`gateway_test.go`/`fallback_test.go` 现有断言若依赖主模型立即重试（无 NoPrimaryRetry 时行为不变），不受影响。

- [ ] **Step 6: 提交**

```bash
git add internal/agent/infrastructure/capability/history_compactor.go internal/agent/domain/port/capability.go internal/llmgateway/domain/completion.go internal/llmgateway/infrastructure/gateway.go api/wiring/agent_llm_adapter.go
git commit -m "feat(agent): 压缩动态时间片与 NoPrimaryRetry/MaxCandidates"
```

---

### Task 9: 最终请求 `context_length_exceeded` 降级最小请求重试一次

**Files:**

- Modify: `internal/agent/application/agent.go`（`executeReAct` 的 `cg.Invoke` 错误分支 602-611）
- Modify: `internal/agent/application/graph/react_llm.go`（若降级构造需要压缩历史）
- Test: `internal/agent/application/agent_test.go` 或 `graph/react_test.go`

**Interfaces:**

- Consumes: `IsContextLengthExceeded`（llmgateway 导出，Task 2）；`compactLoopMessagesWithPolicy`（截断路径）；`ReActState.Messages`/`ec.input`/`ec.systemPrompt`
- Produces: 无新导出——`executeReAct` 内联降级分支

- [ ] **Step 1: 写失败测试**

`react_test.go` 追加（图级：模拟 gateway 对超长请求返回 `ErrContextLengthExceeded` 包装错误，executeReAct 层验证降级）——若 executeReAct 是 `BaseAgent` 私有方法且测试难构造，降级逻辑提取为可测纯函数：

```go
// buildMinimalRetryMessages 构造降级最小请求：system + 纯截断历史
// （剔除全部工具结果与 assistant tool_calls）+ 当前 task。
// 不调 LLM（降级场景模型已 400 边缘，LLM 压缩可能再失败）。
func buildMinimalRetryMessages(systemPrompt, task string, history []port.LLMMessage, window int) []port.LLMMessage

func TestBuildMinimalRetryMessages(t *testing.T) {
 // history 含 tool 消息 → 降级结果剔除；
 // 总量 ≤ window；
 // 首条为 system，末条为 {role: user, content: task}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short ./internal/agent/application/graph/ -run TestBuildMinimalRetryMessages -v`
Expected: FAIL——函数不存在。

- [ ] **Step 3: 实现**——`react_llm.go`（或 `compaction.go`）加：

```go
// buildMinimalRetryMessages 构造最终请求 400 context_length_exceeded 后的
// 降级最小请求（Spec D4）：system + 纯截断历史（成对剔除工具交换）+ task。
// 非流式、单次调用；再次失败即终止，不换模型不退避。
func buildMinimalRetryMessages(systemPrompt, task string, messages []port.LLMMessage, window int) []port.LLMMessage {
 out := make([]port.LLMMessage, 0, len(messages)+2)
 out = append(out, port.LLMMessage{Role: "system", Content: systemPrompt})
 budget := window - len(systemPrompt) - len(task) - 64 // 留余量
 if budget <= 0 {
  budget = 1
 }
 // 保留最近消息，成对剔除工具交换（assistant tool_calls 与其 tool 结果）：
 // 只删 tool 消息会让模型看到"调用了工具但没有结果"，破坏消息配对，
 // 也留下无内容的 assistant 消息。
 for i := len(messages) - 1; i >= 0 && budget > 0; i-- {
  msg := messages[i]
  if msg.Role == "tool" {
   continue
  }
  if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
   continue
  }
  budget -= len(msg.Content)
  if budget < 0 {
   break
  }
  out = append(out, msg)
 }
 // 反转恢复时间顺序，去掉 system 重复
 out = out[1:]
 slices.Reverse(out)
 return append(out, port.LLMMessage{Role: "user", Content: task})
}

// IsContextLengthExceeded 经 duck-typing 探测错误链的 context_length 标记
// （llmgateway ErrContextLengthExceeded 实现该标记方法；graph 不 import
// llmgateway，跨层识别与 permanentMarker 同模式）。
func IsContextLengthExceeded(err error) bool {
 var m interface{ ContextLengthExceeded() bool }
 return errors.As(err, &m)
}
```

（`slices.Reverse` Go 1.21+ 可用；`len()` 是字符数近似 token——降级请求的关键是"必然小于原请求"，字符数下界足够。`IsContextLengthExceeded` 定义在此供 Task 9 的 `executeReAct` 与 Task 8 各自消费——Task 8 的 capability 包不 import graph，用其自身本地副本。）

`agent.go` `executeReAct`（602-611）错误分支改造：

```go
finalState, runErr := cg.Invoke(graphCtx, initState, runCfg)
reactSpan.End()
// 最终请求 context_length_exceeded 降级：循环已结束、工具成本已花，
// 最小请求必然更小，只重试一次（Spec D4）。
if runErr != nil && agentgraph.IsContextLengthExceeded(runErr) && isFinalRequest(finalState) {
 retryMessages := buildMinimalRetryMessages(ec.systemPrompt, ec.input, finalState.Messages, maxTokens)
 // 复用 routeLLM 语义：非流式单次 Route，RetryFn 一层退避
 finalResp, retryErr := retryMinimalFinalRequest(graphCtx, ec, retryMessages)
 if retryErr == nil {
  finalState.Output = finalResp
  runErr = nil
 }
}
if runErr != nil {
 return fmt.Errorf("react: %w", runErr)
}
```

配套：

```go
// isFinalRequest 报告图终止时是否处于"最终回答请求"位置：
// 最后一条消息不是等待工具调用的 assistant 消息。
func isFinalRequest(s agentgraph.ReActState) bool {
 if len(s.Messages) == 0 {
  return true
 }
 last := s.Messages[len(s.Messages)-1]
 return !(last.Role == "assistant" && len(last.ToolCalls) > 0)
}

// retryMinimalFinalRequest 以最小请求重试一次最终回答。
// 参数校验类 400 不在此路径（isFinalRequest 只拦 context_length）。
func retryMinimalFinalRequest(ctx context.Context, ec agentExecContext, messages []port.LLMMessage) (string, error) {
 resp, err := agentgraph.RetryFn(ctx, agentgraph.DefaultRetry, func() (port.CapabilityResponse, error) {
  return ec.capGW.Route(ctx, port.CapabilityRequest{
   TraceID: ec.cfg.TraceID, TenantID: ec.cfg.TenantID, Type: port.CapLLM,
   LLM: &port.LLMCapRequest{
    Model: ec.llmModel, Messages: messages,
    Temperature: ec.cfg.Temperature, MaxTokens: ec.cfg.MaxTokens,
   },
  })
 })
 if err != nil {
  return "", err
 }
 return resp.Text, nil
}
```

（`RetryFn`/`DefaultRetry` 是 graph 包导出；`CapabilityResponse` 文本字段名以 `port.CapabilityResponse` 为准——`agent_llm_adapter.go` 的 `buildAgentCapabilityResponse` 已定义，实现时对照字段。）

- [ ] **Step 4: 运行确认通过**

Run: `go test -short ./internal/agent/...`
Expected: PASS。图级错误路径测试确认：非 context_length 错误不降级（`isFinalRequest` 分支）。

- [ ] **Step 5: 提交**

```bash
git add internal/agent/application/agent.go internal/agent/application/graph/react_llm.go
git commit -m "feat(agent): 最终请求 context_length_exceeded 降级最小请求重试一次"
```

---

### Task 10: 全量回归、E2E 与 PR

**Files:**

- 无代码改动（验证与流程）

- [ ] **Step 1: 全量单测 + race**

Run: `go vet ./... && go test -v -race -timeout 30s ./...`
Expected: 全绿。重点核对：`internal/llmgateway`（错误分类回归）、`internal/agent`（窗口/预算/冷却/降级）、`api/http`（contract golden 不变——无 HTTP 契约变更）。

- [ ] **Step 2: 质量门禁**

Run: `make code-quality && bash scripts/quality/risk-regression-guard.sh --explain && make risk-guardrails`
Expected: 无新增门禁超限（新函数 ≤10 圈复杂度/≤120 行）。`pkg/constants` 新常量命名含 `Default`/`Max`/`Min` 前缀；无内联行为数字残留（grep `5 * time.Second|10 * time.Second|0.85|0.2` 于 agent 包，除 constants 外应为零命中）。

- [ ] **Step 3: E2E（stratum-e2e-development）**

按 skill 流程：本地无头 Chromium 验证——

1. 长对话 → 压缩发生 → 早期关键事实仍在最终回答（语义断言）
2. 冷却生效：一次执行内压缩不连续触发（trace 检查压缩 span 间隔）
3. 窗口链：显式配置 clamp / UNKNOWN 不 clamp（trace `stratum.window_source` 断言）
4. 成本预算：配置小预算 → 执行终止 + 部分结果 + `terminated_by: cost_budget`
5. 最终请求降级：构造超长输入 → 400 → 降级最小请求成功返回

Run: `make test-verify-before-pr`
Expected: 全绿；failed/skipped/unreconciled 阻断项必须清零。

- [ ] **Step 4: base 同步检查 + PR**

```bash
git fetch origin main
git merge-base --is-ancestor origin/main HEAD && echo "base up-to-date" || echo "base behind — 先合并 origin/main 再继续"
git push -u origin feat/context-window-management
gh pr create --base main --title "feat(agent): 上下文窗口统一管理（预算账本/冷却/错误分类/成本预算）" --body "What: ...\nWhy: ...\nHowToTest: ..."
```

PR body 含 What/Why/HowToTest 三节；合入条件：CI 全绿 + base 不落后 + 本计划全部任务完成。

---

## Self-Review 记录（写后自查）

- **Spec 覆盖**：
  - 第 1 节（两阶段窗口）→ Task 4；vendor 表 → Task 3（复用已有 `model_catalog.go`，非新文件——与 spec 影响面表格"新文件"差异已在任务中说明）；`window_source` trace → Task 4 Step 6；WARN 日志 → Task 4 Step 5
  - 第 2 节（预算账本）→ Task 5（含 outputReserve 来源链：显式 max_tokens > vendor maxOut > 常量默认）
  - 第 3 节（执行约束）→ 成本预算 Task 7；单点超时/步数上限不动
  - 第 4 节（冷却/时间片）→ Task 6、Task 8
  - 第 5 节（错误处理）→ Task 1、2、9；瞬态退避不动
  - 第 6 节（测试）→ 各任务测试 + Task 10 回归
- **Placeholder 扫描**：无 TBD/TODO；所有步骤含完整代码或精确修改点。`agent_llm_adapter.go`/`AgentServiceDeps` 具体行号实现时以 grep 定位（已给符号名与文件）。
- **类型一致性**：`LookupModelSpec` 返回 `(int, int)` 全程一致；`Budget` 字段名 `FixedHeadCap/ToolsCap/HistoryCap` 跨 Task 5/6/7 一致；`TerminatedBy` 常量 `CostBudgetTerminated` 跨 Task 7/9 一致；context_length 识别跨 Task 2/8/9 一致——llmgateway 侧 `ErrContextLengthExceeded`/`IsContextLengthExceeded`（sentinel），agent 侧 duck-typing 本地副本（`interface{ ContextLengthExceeded() bool }` + `errors.As`，Task 8 capability 包与 Task 9 graph 包各自定义，不跨层 import）；`WindowSource` 四常量跨 Task 4 一致。
- **DDD 检查**：无 application/infrastructure 对兄弟 context infrastructure 的新 import——vendor 表经 wiring 注入（`AgentServiceDeps.VendorWindowLookup`）；context_length 探测走 duck-typing；`graph.IsContextLengthExceeded` 是 graph 包内导出（agent.go 已 import graph）。
- **已知实现期核实点**（不是 placeholder，是需对源码确认的对接细节）：`makeLLMNode` 内 ledger 累计写法、`CapabilityResponse` 文本字段名、`invokeWithFallback` 是否持有 `req` 引用、`agentExecContext` 构造行号（grep `maxContextTokens` 定位）、`evictUntilFit` 签名、`compactLoopMessagesWithPolicy` 的 `s` 是值还是指针（`LastCompactionAt` 写回依赖指针语义——若为值，改为返回更新后的 state 或传 `*ReActState`）。
