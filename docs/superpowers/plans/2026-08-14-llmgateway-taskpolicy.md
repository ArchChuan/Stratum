# llmgateway 任务策略（llmdomain 领域构造层 + 结构化重试内核）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 memory 各 worker 的 inline `CompletionRequest` 构造与结构化带错重试收敛到 `internal/llmgateway/domain` 领域构造层 + 非泛型内核，同时退役 `memport` 传输镜像、把 `memoryLLMAdapter` 收敛为纯 tenantID 注入。

**Architecture:** 8 个独立可绿任务，走「alias-bridge → 逐包翻转 → 删除镜像」分解。Task 1-2 在 llmdomain 新建 request builder + 结构化重试内核（stdlib only）；Task 3 给 `ValidationError` 补 `Field()` 方法（实现内核 `FieldError` 接口，白名单降级摘要）；Task 4 把 `memport` 镜像改写为 llmdomain 无损别名（bridge，让每个下游包可独立迁移且全仓保持编译绿）+ 收敛 `memoryLLMAdapter` 为纯透传；Task 5-7 把 pipeline / workers / wiring 测试逐个翻转到 llmdomain；Task 8 删除 `completion.go` 并全仓验证。

**Tech Stack:** Go 1.25.12、stdlib only（结构化内核不 import zap，降级 WARN 由消费方外壳记录）、`pkg/constants` 行为常量。

## Global Constraints

- 工作目录：所有命令在 `/home/yang/go-projects/stratum-llmgateway-adapters`（worktree，分支 `feat/llmgateway-adapters`）根目录执行。禁止在 main 提交。
- 分层：`pkg/` 不 import `internal/`；domain 仅依赖 stdlib + `pkg/constants`；application 不 import pgx/Redis/NATS/Gin。跨 context 只能 import 兄弟 context 的 **domain**（`internal/llmgateway/domain` 是 spec §3 指定的唯一允许目标；application/infrastructure 被 CLAUDE.md 禁止）。
- depguard `domain-no-thirdparty` 只 deny `pgx/go-redis/gin/zap/cron`，不拦截 `llmgateway/domain`，因此 bridge 期 `memory/domain/port/completion.go` 临时 import `llmdomain` 合法；测试文件从 depguard 豁免。
- 行为数字进 `pkg/constants`，禁止内联。
- Go 门禁：圈复杂度 ≤10 / 认知复杂度 ≤15 / 函数 ≤120 行 / 嵌套 ≤4；行宽 ≤120；import 按 stdlib → third-party → internal 分组。
- 降级日志白名单：只记字段名 + 计数，禁止原始模型输出、违规值、PII。
- 行为契约必须保留（Task 5 的 `structured_output_test.go` 全部断言）：
  - typed error 为 `llmdomain.ErrStructuredOutputFailed`（原 `pipeline.ErrStructuredExtractionFailed`，删除后迁移）；
  - attempts=3（1 次初始 + `MemoryMaxStructuredRetries=2` 次带错重试）；
  - 错误字符串含 `invalid_fields=fact_type`、不含违规值（防 PII）；
  - WARN 事件 `memory.structured.degraded` 只发一次、含 `invalid_field_fact_type`、不含敏感值；
  - correction 为 system role、内容以 `{correction:` 开头；
  - partial success（≥1 条通过立即返回子集，不重试）；empty array（`[]`）是合法空结果（不触发重试）。
- 已批准行为变更（spec §4，Task 5 落地）：
  - `maybeTriggerSummary` 温度 0.3→0.2 且 `NoPrimaryRetry` false→true（经 `NewSummarizeRequest` 收敛，压缩路径语义）；
  - `history_summarizer` 温度 .2 经 `NewSummarizeRequest` 收敛（.2→.2 无变化）；
  - evaluation judge/casegen 保持 inline（YAGNI，无收敛任务默认值）；
  - agent 现行链路不接入 `NewChatRequest`（spec §10，范围外）。
- 提交格式：`[type](scope): description`，type ∈ `feat|fix|refactor|perf|test|docs|chore|ci`，末尾追加 `Co-Authored-By: Claude <noreply@anthropic.com>`。
- 每个 task 的 gate 通过后才能 commit；Task 8 全仓验证后整个 workstream 完成。

---

### Task 1: llmdomain request builders + `TaskSummarizeTemperature`

**Files:**

- Create: `pkg/constants/llmgateway.go`（追加常量块）
- Create: `internal/llmgateway/domain/task_request.go`
- Test: `internal/llmgateway/domain/task_request_test.go`

**Interfaces:**

- Consumes: `pkg/constants`（`TaskSummarizeTemperature`、`ReasoningEffortHigh`）；`llmdomain` 既有类型 `Message{Role,Content string}`、`Tool{Function ToolFunction}`、`ToolFunction{Name string}`、`ResponseFormat{Type string}`、`CompletionRequest{...}`（见 `internal/llmgateway/domain/llm.go`）。
- Produces: 三个 request builder（Task 5/6 消费）与 `TaskSummarizeTemperature`（Task 5 消费）：
  - `NewChatRequest(model string, msgs []Message, tools []Tool, effort string) *CompletionRequest`
  - `NewSummarizeRequest(model, instructions string, items []string, maxTokens int) *CompletionRequest`
  - `NewExtractRequest(model, system, user string, temperature float32, maxTokens int) *CompletionRequest`
  - `func JSONObject() *ResponseFormat`（返回 `&ResponseFormat{Type: "json_object"}`，Task 2 内核复用）

- [ ] **Step 1: 追加行为常量**

在 `pkg/constants/llmgateway.go` 末尾（`IsValidReasoningEffort` 函数之后）追加：

```go
// 任务策略默认值 —— 由 llmdomain 构造器消费，禁止消费方内联。
const (
 // TaskSummarizeTemperature 是总结任务的默认温度（单轮文本生成，
 // 低温度换取稳定压缩；压缩路径语义：主模型一次失败直接降级候选）。
 TaskSummarizeTemperature float32 = 0.2
)
```

- [ ] **Step 2: 写失败测试**

Create `internal/llmgateway/domain/task_request_test.go`：

```go
package domain

import (
 "testing"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

func TestJSONObject(t *testing.T) {
 if got := JSONObject(); got == nil || got.Type != "json_object" {
  t.Fatalf("JSONObject() = %#v, want type json_object", got)
 }
}

func TestNewChatRequestPassesThrough(t *testing.T) {
 msgs := []Message{{Role: "user", Content: "hi"}}
 tools := []Tool{{Function: ToolFunction{Name: "search"}}}
 req := NewChatRequest("qwen-max", msgs, tools, constants.ReasoningEffortHigh)

 if req.Model != "qwen-max" {
  t.Fatalf("Model = %q, want qwen-max", req.Model)
 }
 if len(req.Messages) != 1 || req.Messages[0].Content != "hi" {
  t.Fatalf("Messages = %#v, want passthrough", req.Messages)
 }
 if len(req.Tools) != 1 || req.Tools[0].Function.Name != "search" {
  t.Fatalf("Tools = %#v, want passthrough", req.Tools)
 }
 if req.ReasoningEffort != constants.ReasoningEffortHigh {
  t.Fatalf("ReasoningEffort = %q, want %q", req.ReasoningEffort, constants.ReasoningEffortHigh)
 }
 if req.Temperature != 0 || req.ResponseFormat != nil || req.NoPrimaryRetry {
  t.Fatalf("chat request must be passthrough with zero task defaults: %#v", req)
 }
}

func TestNewSummarizeRequest(t *testing.T) {
 req := NewSummarizeRequest("qwen-turbo", "压缩：", []string{"a", "b"}, 512)

 if req.Model != "qwen-turbo" {
  t.Fatalf("Model = %q, want qwen-turbo", req.Model)
 }
 if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
  t.Fatalf("Messages must be single user message: %#v", req.Messages)
 }
 if req.Messages[0].Content != "压缩：a\nb" {
  t.Fatalf("Content = %q, want instructions + items joined", req.Messages[0].Content)
 }
 if req.Temperature != constants.TaskSummarizeTemperature {
  t.Fatalf("Temperature = %v, want %v", req.Temperature, constants.TaskSummarizeTemperature)
 }
 if req.MaxTokens != 512 {
  t.Fatalf("MaxTokens = %d, want 512", req.MaxTokens)
 }
 if !req.NoPrimaryRetry {
  t.Fatalf("summarize must set NoPrimaryRetry (compression path semantics)")
 }
 if req.ResponseFormat != nil || len(req.Tools) != 0 {
  t.Fatalf("summarize must have no response_format / tools: %#v", req)
 }
}

func TestNewSummarizeRequestNilItems(t *testing.T) {
 req := NewSummarizeRequest("m", "指令", nil, 0)
 if req.Messages[0].Content != "指令" {
  t.Fatalf("nil items must keep instructions only, got %q", req.Messages[0].Content)
 }
}

func TestNewExtractRequest(t *testing.T) {
 t.Run("with system", func(t *testing.T) {
  req := NewExtractRequest("qwen-plus", "sys", "usr", 0.1, 4096)
  if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
   t.Fatalf("Messages = %#v, want [system, user]", req.Messages)
  }
  if req.Temperature != 0.1 || req.MaxTokens != 4096 {
   t.Fatalf("temp/maxTokens = %v/%d, want 0.1/4096", req.Temperature, req.MaxTokens)
  }
  if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
   t.Fatalf("extract must set json_object response_format: %#v", req.ResponseFormat)
  }
 })
 t.Run("without system", func(t *testing.T) {
  req := NewExtractRequest("m", "", "usr", 0, 0)
  if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
   t.Fatalf("Messages = %#v, want [user] only", req.Messages)
  }
 })
}
```

- [ ] **Step 3: 验证失败**

Run: `go test ./internal/llmgateway/domain/... ./pkg/constants/...`
Expected: 编译失败，报 `undefined: NewChatRequest` / `undefined: JSONObject` 等。

- [ ] **Step 4: 写实现**

Create `internal/llmgateway/domain/task_request.go`：

```go
package domain

import (
 "strings"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

// jsonObjectType 是 OpenAI-compatible JSON mode 的 response_format type。
const jsonObjectType = "json_object"

// JSONObject 返回 OpenAI-compatible JSON mode 的 response_format。
// provider 保证返回合法 JSON，服务端校验退化为语义层；不支持 json_object 的
// 模型由 gateway applyCapabilityGate 能力门控清空（fail-closed），
// 请求侧可无条件设置。
func JSONObject() *ResponseFormat {
 return &ResponseFormat{Type: jsonObjectType}
}

// NewChatRequest 原样透传构造通用对话请求（agent 现行链路可选接入，本次不强制）。
// 不设 temperature / response_format / NoPrimaryRetry：透传语义，零任务默认值。
func NewChatRequest(model string, msgs []Message, tools []Tool, effort string) *CompletionRequest {
 return &CompletionRequest{
  Model:           model,
  Messages:        msgs,
  Tools:           tools,
  ReasoningEffort: effort,
 }
}

// NewSummarizeRequest 构造单轮总结请求：
//   - 单条 user 消息 = instructions + items（\n 连接，items 可为 nil）；
//   - Temperature = TaskSummarizeTemperature（低温度稳定压缩）；
//   - MaxTokens = 参数传入（调用方锁输出长度，0 = 不锁）；
//   - 无 tools；
//   - NoPrimaryRetry = true：压缩路径语义——时间片内主模型一次失败直接降级候选，
//     不消耗主模型重试预算。
func NewSummarizeRequest(model, instructions string, items []string, maxTokens int) *CompletionRequest {
 content := instructions
 if len(items) > 0 {
  content += strings.Join(items, "\n")
 }
 return &CompletionRequest{
  Model:          model,
  Messages:       []Message{{Role: "user", Content: content}},
  Temperature:    constants.TaskSummarizeTemperature,
  MaxTokens:      maxTokens,
  NoPrimaryRetry: true,
 }
}

// NewExtractRequest 构造结构化抽取请求：
//   - system != "" 时消息为 [system, user]，否则只有 user；
//   - ResponseFormat = json_object（能力门控在 gateway 兜底）；
//   - temperature / maxTokens 显式参数，消费方传各自 pkg/constants 常量。
func NewExtractRequest(model, system, user string, temperature float32, maxTokens int) *CompletionRequest {
 msgs := make([]Message, 0, 2)
 if system != "" {
  msgs = append(msgs, Message{Role: "system", Content: system})
 }
 msgs = append(msgs, Message{Role: "user", Content: user})
 return &CompletionRequest{
  Model:          model,
  Messages:       msgs,
  Temperature:    temperature,
  MaxTokens:      maxTokens,
  ResponseFormat: JSONObject(),
 }
}
```

- [ ] **Step 5: 验证通过**

Run: `go test ./internal/llmgateway/domain/... ./pkg/constants/...`
Expected: PASS（6 个测试全绿）。

- [ ] **Step 6: Commit**

```bash
git add pkg/constants/llmgateway.go internal/llmgateway/domain/task_request.go internal/llmgateway/domain/task_request_test.go
git commit -m "feat(llmgateway): add llmdomain task request builders

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: llmdomain 结构化重试内核（stdlib only）

**Files:**

- Create: `internal/llmgateway/domain/structured.go`
- Test: `internal/llmgateway/domain/structured_test.go`

**Interfaces:**

- Consumes: Task 1 的 `JSONObject()`；`llmdomain` 既有类型 `Message`/`CompletionRequest`/`CompletionResponse`。
- Produces: 结构化重试内核与白名单摘要类型（Task 3 消费 `FieldError`；Task 5 消费全部）：
  - `var ErrStructuredOutputFailed = errors.New(...)`（`Unwrap` 目标，消费方用 `errors.Is` 判定）
  - `type Completer interface { Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) }`
  - `type FieldError interface { Field() string }`
  - `type FailureSummary struct { Attempts int; ParseErrors int; InvalidFields map[string]int }`
  - `func (s *FailureSummary) Record(err error)`、`func (s *FailureSummary) FieldNames() []string`
  - `type StructuredOutputError struct { Kind string; Summary FailureSummary }`（`Error()` / `Unwrap()`）
  - `func StructuredRetryLoop(ctx context.Context, client Completer, req *CompletionRequest, maxRetries int, kind string, attempt func(content string) error) (string, error)`

- [ ] **Step 1: 写失败测试**

Create `internal/llmgateway/domain/structured_test.go`：

```go
package domain

import (
 "context"
 "errors"
 "strings"
 "sync"
 "testing"
)

// testFieldErr 实现 FieldError：验证内核按字段名做白名单摘要、不泄露违规值。
type testFieldErr struct{ field, value string }

func (e *testFieldErr) Error() string { return "field " + e.field + " got " + e.value }
func (e *testFieldErr) Field() string { return e.field }

// captureLLMStub 记录每次请求的消息副本与 response_format，逐次返回 contents[i]。
type captureLLMStub struct {
 mu       sync.Mutex
 contents []string
 requests [][]Message
 formats  []*ResponseFormat
}

func (s *captureLLMStub) Complete(_ context.Context, req *CompletionRequest) (*CompletionResponse, error) {
 s.mu.Lock()
 defer s.mu.Unlock()
 s.requests = append(s.requests, append([]Message(nil), req.Messages...))
 s.formats = append(s.formats, req.ResponseFormat)
 idx := len(s.requests) - 1
 if idx >= len(s.contents) {
  idx = len(s.contents) - 1
 }
 return &CompletionResponse{Content: s.contents[idx]}, nil
}

// errLLMStub 每次调用恒返回 err。
type errLLMStub struct{ err error }

func (s *errLLMStub) Complete(context.Context, *CompletionRequest) (*CompletionResponse, error) {
 return nil, s.err
}

func TestStructuredRetryLoopRetriesWithCorrection(t *testing.T) {
 llm := &captureLLMStub{contents: []string{"bad", "good"}}
 _, err := StructuredRetryLoop(context.Background(), llm,
  &CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}},
  2, "extract_facts", func(content string) error {
   if content != "good" {
    return &testFieldErr{field: "importance", value: "1.5"}
   }
   return nil
  })
 if err != nil {
  t.Fatal(err)
 }
 if len(llm.requests) != 2 {
  t.Fatalf("calls = %d, want 2 (initial + correction retry)", len(llm.requests))
 }
 last := llm.requests[1]
 if len(last) != 2 || last[1].Role != "system" {
  t.Fatalf("retry must append system-role correction, got %#v", last)
 }
 if !strings.Contains(last[1].Content, "{correction: ") {
  t.Fatalf("correction must carry error context, got %q", last[1].Content)
 }
}

func TestStructuredRetryLoopExhaustsWithWhitelist(t *testing.T) {
 const secret = "hunter2-secret"
 llm := &captureLLMStub{contents: []string{"always-bad"}}
 _, err := StructuredRetryLoop(context.Background(), llm,
  &CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}},
  2, "extract_facts", func(string) error {
   return &testFieldErr{field: "fact_type", value: secret}
  })
 if !errors.Is(err, ErrStructuredOutputFailed) {
  t.Fatalf("err = %v, want ErrStructuredOutputFailed", err)
 }
 var soe *StructuredOutputError
 if !errors.As(err, &soe) {
  t.Fatalf("err = %T, want *StructuredOutputError", err)
 }
 if soe.Summary.Attempts != 3 {
  t.Fatalf("attempts = %d, want 3 (1 initial + 2 retries)", soe.Summary.Attempts)
 }
 msg := err.Error()
 for _, want := range []string{"attempts=3", "invalid_fields=fact_type"} {
  if !strings.Contains(msg, want) {
   t.Fatalf("err %q missing %q", msg, want)
  }
 }
 if strings.Contains(msg, secret) {
  t.Fatalf("typed error must not leak invalid value, got %q", msg)
 }
}

func TestStructuredRetryLoopFailFastOnProviderError(t *testing.T) {
 llm := &errLLMStub{err: errors.New("upstream 500")}
 _, err := StructuredRetryLoop(context.Background(), llm,
  &CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}},
  2, "extract_facts", func(string) error { return nil })
 if err == nil {
  t.Fatal("provider error must propagate")
 }
 if !strings.Contains(err.Error(), "llm complete") {
  t.Fatalf("error must be wrapped with stage, got %v", err)
 }
 if errors.Is(err, ErrStructuredOutputFailed) {
  t.Fatalf("provider hard error must not be ErrStructuredOutputFailed: %v", err)
 }
}

func TestStructuredRetryLoopSetsJSONObjectOnCloneOnly(t *testing.T) {
 llm := &captureLLMStub{contents: []string{"ok"}}
 callerReq := &CompletionRequest{Messages: []Message{{Role: "user", Content: "x"}}}
 if _, err := StructuredRetryLoop(context.Background(), llm, callerReq, 0, "kind",
  func(string) error { return nil }); err != nil {
  t.Fatal(err)
 }
 if len(llm.formats) != 1 || llm.formats[0] == nil || llm.formats[0].Type != "json_object" {
  t.Fatalf("kernel must set json_object on the request it sends, got %#v", llm.formats)
 }
 if callerReq.ResponseFormat != nil {
  t.Fatalf("kernel must not mutate caller request (clone semantics), got %#v", callerReq.ResponseFormat)
 }
}
```

- [ ] **Step 2: 验证失败**

Run: `go test ./internal/llmgateway/domain/...`
Expected: 编译失败，报 `undefined: StructuredRetryLoop` 等。

- [ ] **Step 3: 写实现**

Create `internal/llmgateway/domain/structured.go`：

```go
package domain

import (
 "context"
 "errors"
 "fmt"
 "sort"
 "strings"
)

// ErrStructuredOutputFailed 表示结构化输出经过全部带错重试后仍 0 条通过校验。
// 调用方必须保留失败语义（MarkFailed/DLQ），禁止静默标记 completed。
var ErrStructuredOutputFailed = errors.New("structured output: all candidates failed validation")

// Completer 是结构化重试内核依赖的最小完成接口（单次非流式）。
// memory pipeline 的 LLMClient / workers 的 TenantLLMClient 均为其别名。
type Completer interface {
 Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
}

// FieldError 由消费方校验错误实现（如 memory 的 ValidationError），
// 供白名单降级摘要取字段名。实现必须 nil-safe（Field 方法对 nil 接收者
// 返回空串，防 typed-nil 经 errors.As 命中后解引用 panic）。
type FieldError interface {
 Field() string
}

// FailureSummary 是降级日志的白名单摘要：只记计数与字段名，禁止原始模型输出
// 与校验违规值（防 PII/原文泄露）。
type FailureSummary struct {
 Attempts      int
 ParseErrors   int
 InvalidFields map[string]int
}

// Record 累计单次失败：FieldError 记字段名（白名单），其余记 parse 错误数。
func (s *FailureSummary) Record(err error) {
 var fe FieldError
 if errors.As(err, &fe) {
  if s.InvalidFields == nil {
   s.InvalidFields = make(map[string]int)
  }
  s.InvalidFields[fe.Field()]++
  return
 }
 s.ParseErrors++
}

// FieldNames 返回已排序的字段名列表（确定性日志输出）。
func (s *FailureSummary) FieldNames() []string {
 names := make([]string, 0, len(s.InvalidFields))
 for f := range s.InvalidFields {
  names = append(names, f)
 }
 sort.Strings(names)
 return names
}

// StructuredOutputError 是带白名单摘要的 typed error。Error() 只含字段名与计数，
// 不含违规值；Unwrap 到 ErrStructuredOutputFailed 供 errors.Is 判定。
type StructuredOutputError struct {
 Kind    string
 Summary FailureSummary
}

func (e *StructuredOutputError) Error() string {
 return fmt.Sprintf("%w: %s (attempts=%d, parse_errors=%d, invalid_fields=%s)",
  ErrStructuredOutputFailed, e.Kind, e.Summary.Attempts, e.Summary.ParseErrors,
  strings.Join(e.Summary.FieldNames(), ","))
}

func (e *StructuredOutputError) Unwrap() error { return ErrStructuredOutputFailed }

// correctionMessage 把校验错误上下文构造成 system role 消息丢回模型自修复。
// 用户约束：重试必须告诉模型错误在哪里（具体字段/值/原因），而非简单重试。
// 用 system role 而非 user role，避免模型把校验错误当用户陈述污染上下文。
func correctionMessage(correction string) Message {
 return Message{Role: "system", Content: "{correction: " + correction + "}"}
}

// cloneReq 浅拷贝请求并复制 Messages 切片，避免带错重试追加 correction 时
// 原地写共享底层数组污染调用方请求。response_format 设于副本上。
func cloneReq(req *CompletionRequest) *CompletionRequest {
 cloned := *req
 cloned.Messages = append([]Message(nil), req.Messages...)
 return &cloned
}

// StructuredRetryLoop 是结构化 JSON 输出的非泛型带错重试内核（stdlib only，
// 不 import zap）：
//  1. 在请求副本上设 response_format=json_object，provider 保证合法 JSON；
//  2. attempt 返回 nil = 本次解析+校验通过 → 返回原始输出字符串；
//     attempt 返回非 nil = 失败 → 白名单记录 + 构造带错 correction（system role）
//     丢回模型，最多重试 maxRetries 次；
//  3. provider 硬错误（网络/4xx/5xx）fail-fast，不消耗重试；
//  4. 全部失败 → 返回 *StructuredOutputError（白名单摘要），消费方外壳负责
//     记录降级 WARN（llmdomain 不 import zap）。
//
// kind 是日志阶段名（extract_facts|enrich|supersede），白名单枚举。
// 消费方保留薄泛型 CompleteStructured[T] 外壳调用本内核，JSON 解析、逐条校验、
// 部分成功语义（≥1 通过返回子集）留本地。
func StructuredRetryLoop(
 ctx context.Context,
 client Completer,
 req *CompletionRequest,
 maxRetries int,
 kind string,
 attempt func(content string) error,
) (string, error) {
 req = cloneReq(req)
 req.ResponseFormat = JSONObject()

 var summary FailureSummary
 for try := 0; try <= maxRetries; try++ {
  summary.Attempts = try + 1
  resp, err := client.Complete(ctx, req)
  if err != nil {
   // provider 硬错误 fail-fast：重试无法自愈，且严格端点 400 已由
   // 网关能力门控拦截，这里出现的错误向上传播即可。
   return "", fmt.Errorf("llm complete (%s): %w", kind, err)
  }
  if aerr := attempt(resp.Content); aerr != nil {
   summary.Record(aerr)
   req.Messages = append(req.Messages, correctionMessage(aerr.Error()))
   continue
  }
  return resp.Content, nil
 }
 return "", &StructuredOutputError{Kind: kind, Summary: summary}
}
```

- [ ] **Step 4: 验证通过**

Run: `go test ./internal/llmgateway/domain/...`
Expected: PASS（4 个测试全绿）。

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/domain/structured.go internal/llmgateway/domain/structured_test.go
git commit -m "feat(llmgateway): add structured retry loop kernel

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: `ValidationError.Field()`（实现内核 `FieldError` 接口）

**Files:**

- Modify: `internal/memory/domain/port/validation.go`（追加方法）
- Test: `internal/memory/domain/port/validation_test.go`

**Interfaces:**

- Consumes: Task 2 的 `FieldError interface { Field() string }`（语义上满足即可，不 import llmdomain）。
- Produces: `func (e *ValidationError) Field() string`（nil-safe）。Task 5 的 `structured_output_test.go` 依赖它：`*port.ValidationError` 经内核 `errors.As(FieldError)` 命中后记 `invalid_field_<字段名>`。

- [ ] **Step 1: 写失败测试**

Create `internal/memory/domain/port/validation_test.go`：

```go
package port

import (
 "errors"
 "testing"
)

func TestValidationErrorField(t *testing.T) {
 e := &ValidationError{Location: "facts", Field: "fact_type", Value: "bad", Reason: "invalid enum"}
 if got := e.Field(); got != "fact_type" {
  t.Fatalf("Field() = %q, want fact_type", got)
 }
}

// TestValidationErrorFieldNilSafe 验证 typed-nil 不 panic：内核 errors.As 命中
// FieldError 类型后调用 Field()，nil 接收者必须返回空串而非 panic。
func TestValidationErrorFieldNilSafe(t *testing.T) {
 var nilErr *ValidationError
 var wrapped error = nilErr // typed-nil 包进 error 接口
 var fe interface{ Field() string }
 if !errors.As(wrapped, &fe) {
  t.Fatal("errors.As must match typed-nil *ValidationError")
 }
 if got := fe.Field(); got != "" {
  t.Fatalf("typed-nil Field() = %q, want empty", got)
 }
 if got := (*ValidationError)(nil).Field(); got != "" {
  t.Fatalf("direct nil Field() = %q, want empty", got)
 }
}
```

- [ ] **Step 2: 验证失败**

Run: `go test ./internal/memory/domain/port/...`
Expected: 编译失败，报 `e.Field undefined (type *ValidationError has no field or method Field)`。

- [ ] **Step 3: 写实现**

在 `internal/memory/domain/port/validation.go` 末尾（`Summary()` 之后）追加：

```go
// Field 返回字段名，实现 llmdomain.FieldError（结构化失败白名单摘要）。
// nil-safe：typed-nil（(*ValidationError)(nil) 包进 error 接口）经 errors.As
// 命中类型后由内核调用 Field() 不 panic，返回空串。
func (e *ValidationError) Field() string {
 if e == nil {
  return ""
 }
 return e.Field
}
```

- [ ] **Step 4: 验证通过**

Run: `go test ./internal/memory/domain/port/...`
Expected: PASS（2 个测试全绿）。

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/port/validation.go internal/memory/domain/port/validation_test.go
git commit -m "feat(memory): add ValidationError.Field for whitelist summary

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: memport 镜像 bridge 为 llmdomain 别名 + `memoryLLMAdapter` 收敛为纯透传

**Files:**

- Modify: `internal/memory/domain/port/completion.go`（整体改写为别名）
- Modify: `api/wiring/memory.go:46-89`（适配器方法收敛 + 删除 `toLLMResponseFormat`）
- Modify: `internal/memory/infrastructure/pipeline/enricher.go:517`（`resp.CompletionTokens` → `resp.Usage.CompletionTokens`）
- Gate: 全仓现有测试（无需新增：alias-bridge 行为保持，现有测试即行为契约）

**Interfaces:**

- Consumes: `llmdomain` 的 `Message`/`ResponseFormat`/`CompletionRequest`/`CompletionResponse`/`Completer`。
- Produces: `memport` 五个类型/接口变成 llmdomain 的**无损别名**（类型逐字段一致）。bridge 期（Task 5-7）下游包继续写 `memport.X` 也能编译；Task 8 删文件前，`memory/domain/port` 是唯一还引用 llmdomain 的地方（符合 spec §3）。

- [ ] **Step 1: 改写 completion.go 为别名**

把 `internal/memory/domain/port/completion.go` 整个文件替换为：

```go
package port

import (
 llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// 传输镜像退役桥接（过渡态，plan Task 8 删除本文件）。
//
// 此前 memport 是 llmgateway domain 的「有损镜像」：Temperature float64、无 json
// tag、CompletionResponse 只留 Content+CompletionTokens，wiring 需手工逐字段转换。
// 改为无损别名后类型逐字段一致，转换层删除；消费者按包逐个迁移到 llmdomain，
// 迁移完成（Task 8）后本文件整体删除。llmdomain 是 spec §3 指定的唯一可跨
// context import 的 domain。
type CompletionMessage = llmdomain.Message
type ResponseFormat = llmdomain.ResponseFormat
type CompletionRequest = llmdomain.CompletionRequest
type CompletionResponse = llmdomain.CompletionResponse
type Completer = llmdomain.Completer
```

注意：删除原 `context` import（别名不再引用 `Completer` 定义）。

- [ ] **Step 2: 收敛 memoryLLMAdapter**

把 `api/wiring/memory.go` 第 46-89 行（`memoryLLMAdapter` 的 `Complete` 方法 + `toLLMResponseFormat` 函数）整体替换为（struct 保留）：

```go
type memoryLLMAdapter struct {
 client   memoryGatewayCompleter
 tenantID string
}

func (a memoryLLMAdapter) Complete(ctx context.Context, req *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
 if a.client == nil {
  return nil, fmt.Errorf("memory llm adapter: client is nil")
 }
 if req == nil {
  return nil, fmt.Errorf("memory llm adapter: request is nil")
 }
 if a.tenantID != "" {
  ctx = reqctx.WithTenantID(ctx, a.tenantID)
 }
 resp, err := a.client.Complete(ctx, req)
 if err != nil {
  return nil, err
 }
 if resp == nil {
  return nil, fmt.Errorf("memory llm adapter: provider returned nil response")
 }
 return resp, nil
}
```

删除整个 `toLLMResponseFormat`（第 82-89 行）——bridge 后 `memport.ResponseFormat == llmdomain.ResponseFormat`，转换已不需要。`memport` import 保留（`MechanismBaselineResolver`/`LLMExtractor`/`LLMSuperseder`/`MemoryRepo`/`FactRepo`/`HistoryRepo`/`EmbedClient` 仍在用）。

- [ ] **Step 3: 修复 enricher.go 别名有损读取点**

`internal/memory/infrastructure/pipeline/enricher.go:517`：

```go
 if err := w.writeSummary(ctx, schema, ev, summary, resp.CompletionTokens); err != nil {
```

→

```go
 if err := w.writeSummary(ctx, schema, ev, summary, resp.Usage.CompletionTokens); err != nil {
```

原因：llmdomain 的 `CompletionResponse` 没有 `CompletionTokens` 顶层字段（token 在 `Usage` 上），别名后该读取点必须适配。全仓仅此处 + 第 2 步适配器引用 `.CompletionTokens`。

- [ ] **Step 4: 验证全仓绿**

Run: `go test ./internal/memory/... ./api/wiring/...`
Expected: PASS。bridge 保持类型逐字段一致，所有下游（pipeline/workers/wiring + 测试）仍写 `memport.X` 但实际是 llmdomain 别名，行为不变。若编译报错，检查是否有遗漏的 `.CompletionTokens` 读取点（`grep -rn '\.CompletionTokens' --include='*.go' internal/memory api/wiring`）。

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/port/completion.go api/wiring/memory.go internal/memory/infrastructure/pipeline/enricher.go
git commit -m "refactor(memory): bridge memport mirror to llmdomain aliases

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: pipeline 包翻转 → llmdomain（外壳重写调内核 + builders）

**Files:**

- Modify: `pkg/constants/memory.go:152`（追加 `MemoryEnrichLLMTemperature`）
- Modify: `internal/memory/infrastructure/pipeline/structured_output.go`（整体重写为薄泛型外壳）
- Modify: `internal/memory/infrastructure/pipeline/pipeline.go:31`（`LLMClient = llmdomain.Completer`）
- Modify: `internal/memory/infrastructure/pipeline/llm_extractor.go:94-115`（builder + 签名）
- Modify: `internal/memory/infrastructure/pipeline/enricher.go:366-386, 501-517`（builders）
- Test: `internal/memory/infrastructure/pipeline/structured_output_test.go`（memport→llmdomain）
- Test: `internal/memory/infrastructure/pipeline/llm_extractor_test.go`（memport→llmdomain）

**Interfaces:**

- Consumes: Task 1 的 `NewExtractRequest`/`NewSummarizeRequest`；Task 2 的 `StructuredRetryLoop`/`Completer`/`FailureSummary`/`StructuredOutputError`/`ErrStructuredOutputFailed`；`pkg/constants` 的 `MemoryMaxStructuredRetries`/`MemoryExtractLLMMaxTokens`/`MemoryEnrichLLMTemperature`。
- Produces: `pipeline.CompleteStructured[T]`（薄泛型外壳，签名改为 llmdomain；Task 6 的 llm_superseder 消费）、`pipeline.LLMClient = llmdomain.Completer`（wiring 消费）。删除 `ErrStructuredExtractionFailed`/`JSONObject()`/`CorrectionMessage()`（已确认无包外引用）。

- [ ] **Step 1: 追加常量**

在 `pkg/constants/memory.go` 的 `MemoryExtractLLMMaxTokens` 行（第 152 行）之后、`MemoryMaxStructuredRetries` 之前插入：

```go
 MemoryEnrichLLMTemperature = 0.1 // 富化抽取任务温度（低温度换取字段语义稳定）
```

- [ ] **Step 2: 重写 structured_output.go 为薄泛型外壳**

把 `internal/memory/infrastructure/pipeline/structured_output.go` 整个文件替换为：

```go
package pipeline

import (
 "context"
 "errors"

 "go.uber.org/zap"

 llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

// CompleteStructured 是结构化 JSON 输出的统一泛型外壳（memory 三个直接 JSON
// 调用点共用）。重试/带错 correction/白名单摘要由 llmdomain.StructuredRetryLoop
// 内核承担；本外壳只做类型化解析、逐条校验与部分成功语义：
//  1. 解析失败 → 交给内核走带错重试；
//  2. 校验失败 → 交给内核走带错重试；
//  3. 全部失败 → 记录降级 WARN（白名单）+ 透传 *llmdomain.StructuredOutputError
//     （调用方保留 MarkFailed/DLQ 语义）。
//
// kind 是日志阶段名（extract_facts|enrich|supersede），白名单枚举。
func CompleteStructured[T any](
 ctx context.Context,
 client llmdomain.Completer,
 req *llmdomain.CompletionRequest,
 parse func(string) (T, error),
 validate func(T) error,
 logger *zap.Logger,
 kind string,
) (T, error) {
 var zero T
 var parsed T
 _, err := llmdomain.StructuredRetryLoop(ctx, client, req, constants.MemoryMaxStructuredRetries, kind,
  func(content string) error {
   v, perr := parse(content)
   if perr != nil {
    return perr
   }
   if verr := validate(v); verr != nil {
    return verr
   }
   parsed = v
   return nil
  })
 if err != nil {
  var soe *llmdomain.StructuredOutputError
  if errors.As(err, &soe) {
   logStructuredDegraded(logger, kind, soe.Summary)
  }
  return zero, err
 }
 return parsed, nil
}

// logStructuredDegraded 记录结构化输出降级 WARN，仅含白名单字段。
// logger 可能为 nil（测试/降级启动），nil 安全。
func logStructuredDegraded(logger *zap.Logger, kind string, s llmdomain.FailureSummary) {
 if logger == nil {
  return
 }
 fields := []zap.Field{
  zap.String("stage", "memory."+kind+".structured_degraded"),
  zap.Int("attempts", s.Attempts),
  zap.Int("parse_errors", s.ParseErrors),
 }
 for _, f := range s.FieldNames() {
  fields = append(fields, zap.Int("invalid_field_"+f, s.InvalidFields[f]))
 }
 logger.Warn("memory.structured.degraded", fields...)
}
```

删除原文件全部内容：`ErrStructuredExtractionFailed`、`JSONObject()`、`CorrectionMessage()`、`structuredFailureSummary`/`record()`/`fieldNames()`、`cloneReq()`、旧 `CompleteStructured` 循环（逻辑移入内核）。`fmt`/`sort`/`strings`/`memport` import 一并删除。

- [ ] **Step 3: pipeline.go 别名翻转**

`internal/memory/infrastructure/pipeline/pipeline.go`：

- import 增加 `llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"`。
- 第 31 行 `type LLMClient = memport.Completer` → `type LLMClient = llmdomain.Completer`。
- `memport` import 保留（`MechanismBaselineResolver` 第 49、98 行仍在用）。

- [ ] **Step 4: llm_extractor.go 用 builder**

`internal/memory/infrastructure/pipeline/llm_extractor.go`：

- import 增加 `llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"`；`memport` import 保留（`ExtractedFact`/`ValidationError`）。
- 第 96-103 行 `ExtractFacts` 的请求构造替换：

```go
 system := fmt.Sprintf(e.systemPromptOr(), userID, agentID, e.maxFacts(ctx))
 req := llmdomain.NewExtractRequest(e.extractionModel, system, message, 0, constants.MemoryExtractLLMMaxTokens)
 return extractFactsStructured(ctx, e.client, req, e.logger)
```

- 第 110-115 行 `extractFactsStructured` 签名（client/req 换 llmdomain）：

```go
func extractFactsStructured(
 ctx context.Context,
 client llmdomain.Completer,
 req *llmdomain.CompletionRequest,
 logger *zap.Logger,
) ([]*memport.ExtractedFact, error) {
```

函数体不变（partial-success closure 用 `memport.ValidationError`/`memport.ExtractedFact`）。

- [ ] **Step 5: enricher.go 用 builders**

`internal/memory/infrastructure/pipeline/enricher.go`：

- import 增加 `llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"`；`port` import 保留（`ValidationError`/`MechanismBaselineResolver`）。
- 第 366-374 行 `callEnrichLLM` 请求构造替换：

```go
 prompt := formatEnrichmentPrompt(eff.enrichmentTmpl, role, content)
 req := llmdomain.NewExtractRequest(eff.model, "", prompt, constants.MemoryEnrichLLMTemperature, 0)
```

（`constants` import 已存在。）

- 第 501-508 行 `maybeTriggerSummary` 请求构造替换：

```go
 prompt := formatSummaryPrompt(eff.summaryTmpl, input)
 req := llmdomain.NewSummarizeRequest(eff.summaryModel, prompt, nil, 0)
```

**行为变更（spec §4 已批准）**：temperature 0.3→0.2（`TaskSummarizeTemperature`）、`NoPrimaryRetry` false→true（压缩路径语义）。第 517 行已是 Task 4 修复的 `resp.Usage.CompletionTokens`，本步不再动。

- [ ] **Step 6: 迁移 structured_output_test.go**

`internal/memory/infrastructure/pipeline/structured_output_test.go`：

- import 增加 `llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"`；`memport` import 保留（`validateFactList` 用 `memport.ExtractedFact`）。
- `seqLLMStub`：`requests [][]memport.CompletionMessage` → `[][]llmdomain.Message`；`Complete` 签名 `*memport.CompletionRequest`/`*memport.CompletionResponse` → `*llmdomain.CompletionRequest`/`*llmdomain.CompletionResponse`；`append([]memport.CompletionMessage(nil), req.Messages...)` → `append([]llmdomain.Message(nil), req.Messages...)`；`&memport.CompletionResponse{Content: ...}` → `&llmdomain.CompletionResponse{Content: ...}`。
- `errLLMStub.Complete` 签名同样 → llmdomain。
- 5 处 `&memport.CompletionRequest{Messages: []memport.CompletionMessage{{Role: "user", Content: "x"}}}`（第 71-73、96-98、117-119、180-182 行）→ `&llmdomain.CompletionRequest{Messages: []llmdomain.Message{{Role: "user", Content: "x"}}}`。
- 3 处 `ErrStructuredExtractionFailed`（第 105、120、183 行）→ `llmdomain.ErrStructuredOutputFailed`。
- 其余断言（attempts=3、invalid_fields=fact_type、PII 白名单、partial success、empty array、correction 前缀）全部保留——它们是行为契约。

- [ ] **Step 7: 迁移 llm_extractor_test.go**

`internal/memory/infrastructure/pipeline/llm_extractor_test.go`：

- import 删除 `memport`、增加 `llmdomain`（memport 仅用于 completion 类型）。
- `extractorLLMStub.Complete` 签名 → llmdomain；`&memport.CompletionResponse{Content: ...}` → `&llmdomain.CompletionResponse{Content: ...}`。

- [ ] **Step 8: 验证通过**

Run: `go test ./internal/memory/infrastructure/pipeline/...`
Expected: PASS（5 个 structured 测试 + 3 个 extractor 测试全绿，断言不变）。

- [ ] **Step 9: 确认全仓仍编译（bridge 兜底）**

Run: `go build ./...`
Expected: 成功。workers（llm_superseder/history_summarizer）与 wiring 仍写 `memport.X`，经 Task 4 的别名保持编译绿。

- [ ] **Step 10: Commit**

```bash
git add pkg/constants/memory.go internal/memory/infrastructure/pipeline/structured_output.go internal/memory/infrastructure/pipeline/pipeline.go internal/memory/infrastructure/pipeline/llm_extractor.go internal/memory/infrastructure/pipeline/enricher.go internal/memory/infrastructure/pipeline/structured_output_test.go internal/memory/infrastructure/pipeline/llm_extractor_test.go
git commit -m "refactor(memory): flip pipeline completion to llmdomain

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: workers 包翻转 → llmdomain（builders + 测试迁移）

**Files:**

- Modify: `pkg/constants/memory.go:165`（追加 `MemorySupersedeJudgeMaxTokens`）
- Modify: `internal/memory/infrastructure/workers/tenant_llm.go:11`（`TenantLLMClient = llmdomain.Completer`）
- Modify: `internal/memory/infrastructure/workers/history_summarizer.go:76-80`（builder）
- Modify: `internal/memory/infrastructure/workers/llm_superseder.go:90-96`（builder + 常量）
- Test: `internal/memory/infrastructure/workers/llm_superseder_test.go`（memport→llmdomain）
- Test: `internal/memory/infrastructure/workers/history_summarizer_test.go`（memport→llmdomain）
- Test: `internal/memory/infrastructure/workers/supersede_worker_test.go:195-196`（port→llmdomain）

**Interfaces:**

- Consumes: Task 5 的 `pipeline.CompleteStructured`（llmdomain 签名）、Task 1 的 `NewExtractRequest`/`NewSummarizeRequest`、Task 2 的 `Completer`、`pkg/constants` 的 `MemorySupersedeJudgeMaxTokens`。
- Produces: `memworkers.TenantLLMClient = llmdomain.Completer`（wiring 消费）。`memport` 在此包仅剩 `SupersedeJudgment`（memory 领域类型，保留）。

- [ ] **Step 1: 追加常量**

在 `pkg/constants/memory.go` 的 `MemoryInlineSupersedeLLMPerFact` 行（第 165 行）之后追加：

```go
 MemorySupersedeJudgeMaxTokens = 256 // 取代判定请求的 max_tokens 上限
```

- [ ] **Step 2: tenant_llm.go 别名翻转**

`internal/memory/infrastructure/workers/tenant_llm.go`：

- import 删除 `memport`、增加 `llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"`。
- 第 11 行 `type TenantLLMClient = memport.Completer` → `type TenantLLMClient = llmdomain.Completer`。

- [ ] **Step 3: history_summarizer.go 用 builder**

`internal/memory/infrastructure/workers/history_summarizer.go`：

- import 删除 `memport`（仅此处用）、增加 `llmdomain`。
- 第 75-80 行替换：

```go
 prompt := s.summarizePrefixOr() + strings.Join(items, "\n")
 resp, err := client.Complete(ctx, llmdomain.NewSummarizeRequest(s.summaryModel, s.summarizePrefixOr(), items, 0))
```

注意 `prompt` 变量不再需要（builder 内部拼接），删除该行；`strings` import 如无其他使用一并删除（`SummarizeHistory` 是唯一 `strings.Join` 调用点，确认后删除）。

- [ ] **Step 4: llm_superseder.go 用 builder + 常量**

`internal/memory/infrastructure/workers/llm_superseder.go`：

- import 增加 `llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"` 与 `"github.com/byteBuilderX/stratum/pkg/constants"`；`memport` import 保留（`SupersedeJudgment`）。
- 第 89-96 行替换：

```go
 prompt := fmt.Sprintf(s.judgePromptOr(), oldFact, newFact)
 judgment, err := pipeline.CompleteStructured(ctx, client, llmdomain.NewExtractRequest(
  s.judgeModel, "", prompt, 0, constants.MemorySupersedeJudgeMaxTokens,
 ), parseSupersedeJudgment,
  func(j memport.SupersedeJudgment) error { return j.Validate() },
  s.logger, "supersede")
```

行为对照：原 `MaxTokens: 256` 内联 → `constants.MemorySupersedeJudgeMaxTokens`；`ResponseFormat` 原先由 `CompleteStructured` 克隆时设置，现由 `NewExtractRequest` 设置，等价。

- [ ] **Step 5: 迁移 llm_superseder_test.go**

`internal/memory/infrastructure/workers/llm_superseder_test.go`：

- import 删除 `memport`、增加 `llmdomain`。
- 第 18 行 `type completionClientFunc func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error)` → llmdomain 版。
- 第 20 行 `func (f completionClientFunc) Complete(...)` 签名 → llmdomain。
- 全部 8 处 inner `func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error)`（第 26、30、68、71、124、166、192、218 行）→ llmdomain；其中 `return &memport.CompletionResponse{Content: ...}` → `&llmdomain.CompletionResponse{Content: ...}`。
- 第 93 行 `callCompletionServer(ctx context.Context, baseURL string, req *memport.CompletionRequest) (*memport.CompletionResponse, error)` → llmdomain；第 118 行 `return &memport.CompletionResponse{...}` → llmdomain。函数体访问 `req.Model`/`req.Messages`/`m.Role`/`m.Content`——llmdomain 类型全有，不动。
- 第 217 行 `var requests [][]memport.CompletionMessage` → `[][]llmdomain.Message`；第 219 行 `append([]memport.CompletionMessage(nil), req.Messages...)` → `append([]llmdomain.Message(nil), req.Messages...)`。

- [ ] **Step 6: 迁移 history_summarizer_test.go**

`internal/memory/infrastructure/workers/history_summarizer_test.go`：

- import 删除 `memport`、增加 `llmdomain`（memport 仅用于 completion 类型）。
- 4 处 inner `func(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error)`（第 22、44、65、91 行）→ llmdomain；对应 `&memport.CompletionResponse{Content: ...}` → llmdomain。

- [ ] **Step 7: 迁移 supersede_worker_test.go:195-196**

`internal/memory/infrastructure/workers/supersede_worker_test.go`：

- import 增加 `llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"`（`port` import 保留，`SupersedeCandidate` 在用）。
- 第 195-196 行：

```go
  return completionClientFunc(func(context.Context, *port.CompletionRequest) (*port.CompletionResponse, error) {
   return &port.CompletionResponse{Content: `{"supersedes":true,"reason":"updated"}`}, nil
  }), nil
```

→

```go
  return completionClientFunc(func(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
   return &llmdomain.CompletionResponse{Content: `{"supersedes":true,"reason":"updated"}`}, nil
  }), nil
```

- [ ] **Step 8: 验证通过**

Run: `go test ./internal/memory/infrastructure/workers/...`
Expected: PASS（llm_superseder 6 个 + history_summarizer 4 个 + supersede_worker 全套全绿）。

- [ ] **Step 9: Commit**

```bash
git add pkg/constants/memory.go internal/memory/infrastructure/workers/tenant_llm.go internal/memory/infrastructure/workers/history_summarizer.go internal/memory/infrastructure/workers/llm_superseder.go internal/memory/infrastructure/workers/llm_superseder_test.go internal/memory/infrastructure/workers/history_summarizer_test.go internal/memory/infrastructure/workers/supersede_worker_test.go
git commit -m "refactor(memory): flip workers completion to llmdomain

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: wiring 测试迁移 → llmdomain

**Files:**

- Test: `api/wiring/memory_test.go`（memport→llmdomain）

**Interfaces:**

- Consumes: `llmdomain` 的 `CompletionRequest`/`CompletionResponse`/`ResponseFormat`。
- Produces: 无（测试迁移，行为不变）。迁移后 `api/wiring` 是最后一个还写 `memport.Completion*` 的地方，Task 8 删除 completion.go 后全仓 `memport` 只剩 memory 领域类型引用。

- [ ] **Step 1: 迁移 memory_test.go**

`api/wiring/memory_test.go`：

- import 删除 `memport`（第 8 行）；`llmdomain` 已 import（第 7 行），保留。
- 第 66-68 行 `completionClientForWiringTest`：

```go
func (completionClientForWiringTest) Complete(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
 return &memport.CompletionResponse{}, nil
}
```

→

```go
func (completionClientForWiringTest) Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
 return &llmdomain.CompletionResponse{}, nil
}
```

- 5 处 `&memport.CompletionRequest{}`（第 78、95、107、129、145 行）→ `&llmdomain.CompletionRequest{}`。
- 第 130 行 `&memport.ResponseFormat{Type: "json_object"}` → `&llmdomain.ResponseFormat{Type: "json_object"}`。
- `nilCompletionClientForWiringTest`/`tenantCapturingClientForWiringTest`/`requestCapturingGatewayCompleter` 已是 llmdomain，不动。

- [ ] **Step 2: 验证通过**

Run: `go test ./api/wiring/...`
Expected: PASS（5 个 memory wiring 测试全绿，断言不变——纯透传后 `TestMemoryLLMAdapterForwardsResponseFormat` 仍验证 response_format 透传与 nil 保持）。

- [ ] **Step 3: Commit**

```bash
git add api/wiring/memory_test.go
git commit -m "refactor(wiring): migrate memory tests to llmdomain

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 8: 退役 memport completion.go + 全仓验证

**Files:**

- Delete: `internal/memory/domain/port/completion.go`
- Gate: 全仓编译 + 测试 + 质量门禁

**Interfaces:**

- Consumes: 前面全部任务。删除后 `internal/memory/domain/port` 不再 import `llmdomain`（端态干净，符合 spec §3「domain 仅 stdlib + pkg/constants」）。
- Produces: workstream ③ 完成态。

- [ ] **Step 1: 删除 completion.go**

Run: `git rm internal/memory/domain/port/completion.go`

- [ ] **Step 2: 确认零残留引用**

Run:

```bash
grep -rn 'memport\.\(Completer\|CompletionRequest\|CompletionResponse\|CompletionMessage\|ResponseFormat\)' --include='*.go' internal/ api/ pkg/ || true
grep -rn 'port\.\(Completer\|CompletionRequest\|CompletionResponse\|CompletionMessage\|ResponseFormat\)' --include='*.go' internal/memory/ api/ || true
```

Expected: 两条 grep 均空输出。`memport.` 剩余引用只允许 memory 领域类型（`MechanismBaselineResolver`/`LLMExtractor`/`LLMSuperseder`/`MemoryRepo`/`FactRepo`/`HistoryRepo`/`EmbedClient`/`ExtractedFact`/`SupersedeJudgment`/`ValidationError` 等）。

- [ ] **Step 3: 全仓编译 + 快速测试**

Run: `go vet ./... && go test -short ./...`
Expected: 全绿。若有编译错误，回查遗漏的 `memport.Completion*`/`port.Completion*` 引用（Step 2 grep 定位）。

- [ ] **Step 4: 质量门禁**

Run: `make code-quality && make check`
Expected: 通过（圈复杂度 ≤10 / 认知 ≤15 / 行数 ≤120 / 嵌套 ≤4；DTO 残留守卫无异常）。

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor(memory): retire memport completion mirror

Co-Authored-By: Claude <noreply@anthropic.com>"
```

- [ ] **Step 6: PR 前收尾（可选，合入前做）**

PR 前按 CLAUDE.md 跑 `go test -v -race -timeout 30s ./...` 与 `make risk-guardrails`。本 workstream 不涉及 DDL/凭据/消息协议，risk-guardrails 命中项为「外部依赖验证」类，检查 LLM 路径预算/有限重试/失败传播即可。

---

## Self-Review Notes（writing-plans 自检，非执行步骤）

1. **Spec 覆盖**：§4 每一条都有对应任务。builder（T1）、内核（T2）、`FieldError`/`ValidationError.Field()`（T3）、memport 镜像退役 + adapter 收敛（T4/T8）、消费方重构 extractor/enricher/superseder/history_summarizer（T5/T6）、evaluation judge 保持 inline（Global Constraints 显式说明，YAGNI）、行为数字进 constants（T1/T5/T6）。
2. **占位符扫描**：无 TBD/TODO；每个改动步骤都给了完整代码或精确到行号的替换。
3. **类型一致性**：`NewExtractRequest`/`NewSummarizeRequest`/`NewChatRequest` 签名在 T1 定义、T5/T6 消费处完全一致；`StructuredRetryLoop` 的 `attempt func(string) error` 契约在 T2 定义、T5 外壳消费一致；`llmdomain.ErrStructuredOutputFailed` 在 T2 定义、T5 测试迁移一致；`pipeline.CompleteStructured` 在 T5 换 llmdomain 签名、T6 superseder 消费一致；`MemoryEnrichLLMTemperature`（T5）/`MemorySupersedeJudgeMaxTokens`（T6）在常量 task 定义、同 task 消费。
4. **桥接正确性**：T4 别名使 T5/T6/T7 期间 workers/wiring 继续写 `memport.X` 仍编译；`CompletionResponse` 别名有损点（`.CompletionTokens`）全仓仅 2 处（enricher.go:517、wiring adapter），分别由 T4 Step 2/3 修复。
