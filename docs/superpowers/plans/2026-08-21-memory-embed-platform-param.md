# 记忆嵌入模型平台参数化与运营修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把记忆嵌入模型改为平台参数（未配置 fail-closed+告警）、下线租户迁移卡片、放开模型目录删除、修平台参数页加载/清空语义、补 extraction 日志与 trace、移除存量提示词兜底，并完成测试、PR、CI 与 CD 部署。

**Architecture:** 参数由 `internal/parameters` 注册表+`platform_settings` 承载（热更新）；`tenantEmbeddingModelResolver` 改为读平台参数；memory 各 worker 对空提示词显式报错；DLQ/抽取队列补 trace_id；前端按 registry schema 渲染嵌入模型选择器。

**Tech Stack:** Go 1.25（pgx/v5、NATS JetStream、Gin）、React 18 + AntD 5 + TypeScript、PostgreSQL 多租户 schema、Prometheus 告警规则、k3s CD（GitHub Actions）。

**执行环境**：worktree `/home/yang/go-projects/stratum-memory-embed-param`，分支 `feat/memory-embed-platform-param`。所有命令在该目录执行（`cd /home/yang/go-projects/stratum-memory-embed-param`）。Go 快速验证 `go vet && go test -short ./...`；前端 `make fe-lint && make fe-build`。

---

## Task 1: 参数注册表新增 `memory.embedding_model` 与 `ControlEmbeddingModel`

**Files:**

- Modify: `internal/parameters/domain/parameter.go:42-50`
- Modify: `internal/parameters/domain/registry.go:549`（`registerMemoryWorkerParams` 内 `memory.summary_model` 定义之后）
- Test: `internal/parameters/domain/registry_test.go:118-150`（追加断言）

- [ ] **Step 1: 写失败测试**

在 `registry_test.go` 新增：

```go
// TestRegistryMemoryEmbeddingModel 断言平台级记忆嵌入模型参数定义。
func TestRegistryMemoryEmbeddingModel(t *testing.T) {
 r := NewParametersRegistry()
 def, ok := r.Get("memory.embedding_model")
 if !ok {
  t.Fatal("memory.embedding_model not registered")
 }
 if def.Scope != ScopePlatform {
  t.Errorf("scope = %q, want platform", def.Scope)
 }
 if def.VisualHint.Control != ControlEmbeddingModel {
  t.Errorf("control = %q, want embedding_model", def.VisualHint.Control)
 }
 if def.Optimizable {
  t.Error("optimizable must be false")
 }
 if len(def.EvaluationKeys) != 0 {
  t.Errorf("evaluation keys = %v, want none", def.EvaluationKeys)
 }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/parameters/domain/ -run TestRegistryMemoryEmbeddingModel -count=1`
Expected: FAIL（`ControlEmbeddingModel` 未定义、key 未注册）。

- [ ] **Step 3: 实现**

`parameter.go` Control 枚举新增：

```go
 // ControlEmbeddingModel renders an embedding-model picker (capability=embedding).
 ControlEmbeddingModel Control = "embedding_model"
```

`registry.go` `registerMemoryWorkerParams` 内、`memory.summary_model` 定义后插入：

```go
  {
   Key: "memory.embedding_model", Scope: ScopePlatform, Category: "memory",
   DisplayName: "记忆嵌入模型",
   Description: "全局记忆嵌入模型（模型管理目录选择）；未设置时记忆写入 fail-closed 并告警",
   ValueType: TypeString, Default: "",
   VisualHint:  VisualHint{Control: ControlEmbeddingModel},
   Optimizable: false,
  },
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/parameters/domain/ -run TestRegistryMemoryEmbeddingModel -count=1`；随后 `go test ./internal/parameters/... -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/parameters/domain/parameter.go internal/parameters/domain/registry.go internal/parameters/domain/registry_test.go
git commit -m "feat(parameters): register memory.embedding_model platform parameter"
```

## Task 2: 前端嵌入模型控件（zod 枚举 + capability + ParameterControl）

**Files:**

- Modify: `web/src/modules/parameters/model/parameters.ts:8`
- Modify: `web/src/modules/parameters/components/ProviderModelSelect.tsx`
- Modify: `web/src/modules/parameters/components/ParameterControl.tsx:16-17,42-46`
- Test: `web/src/modules/parameters/components/__tests__/ParameterControl.test.tsx`（若存在；不存在则新建）

- [ ] **Step 1: 写失败测试**

新建 `web/src/modules/parameters/components/__tests__/ParameterControl.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ParameterControl } from '../ParameterControl';

vi.mock('@/modules/llm', () => ({
  llmApi: {
    listModels: vi.fn().mockResolvedValue([]),
    listProviders: vi.fn().mockResolvedValue([]),
  },
}));

describe('ParameterControl', () => {
  it('renders embedding model control for embedding_model hint', () => {
    render(
      <ParameterControl
        def={{
          key: 'memory.embedding_model',
          scope: 'platform',
          category: '记忆',
          display_name: '记忆嵌入模型',
          value_type: 'string',
          default: '',
          description: '',
          optimizable: false,
          sensitive: false,
          visual_hint: { control: 'embedding_model' },
        }}
      />,
    );
    expect(screen.getByRole('combobox')).toBeTruthy();
  });
});
```

- [ ] **Step 2: 运行确认失败**

Run: `npm --prefix web test -- parameters/components/__tests__/ParameterControl.test.tsx`
Expected: FAIL（zod 枚举不接受 `embedding_model` / case 未实现）。

- [ ] **Step 3: 实现**

`parameters.ts`：

```ts
control: z.enum(['slider', 'select', 'toggle', 'textarea', 'number', 'model', 'embedding_model']),
```

`ProviderModelSelect.tsx`：

```tsx
interface ProviderModelSelectProps {
  value?: string;
  onChange?: (value: string) => void;
  placeholder?: string;
  capability?: 'chat' | 'embedding';
}

export const ProviderModelSelect = ({
  value,
  onChange,
  placeholder = '未设置（使用定义默认）',
  capability = 'chat',
}: ProviderModelSelectProps) => {
  // ...现有实现，useEffect 内改为：
  // llmApi.listModels({ capability })
```

`ParameterControl.tsx` `case 'model':` 后新增：

```tsx
    case 'embedding_model':
      // 嵌入模型目录选择器（provider 分组）；存储值 = 模型名。
      return <ProviderModelSelect capability="embedding" />;
```

- [ ] **Step 4: 运行确认通过**

Run: `npm --prefix web test -- parameters/components/__tests__/ParameterControl.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/parameters/model/parameters.ts web/src/modules/parameters/components/ProviderModelSelect.tsx web/src/modules/parameters/components/ParameterControl.tsx web/src/modules/parameters/components/__tests__/ParameterControl.test.tsx
git commit -m "feat(web): embedding model control for platform parameters"
```

## Task 3: 参数写时校验（embedding 目录）

**Files:**

- Modify: `api/wiring/parameters.go:126-141,168-197`
- Test: `api/wiring/parameters_test.go`（若存在；不存在则新建 `TestValidateEmbeddingModelInDirectory`）

- [ ] **Step 1: 写失败测试**

在 `api/wiring/parameters_test.go` 新增（注入 registry + 假 `modelRegistryOrNil` 返回含 embedding-3 的目录；用 `injectModelDirectoryValidation` 后走 `def.ValidateFn`）：

```go
func TestValidateEmbeddingModelInDirectory(t *testing.T) {
 c := &Container{
  Parameters: &Parameters{Registry: domain.NewParametersRegistry()},
  LLMGateway: &LLMGateway{Registry: fakeEmbeddingRegistry(t, []string{"embedding-3"})},
 }
 c.injectModelDirectoryValidation()
 def, _ := c.Parameters.Registry.Get("memory.embedding_model")
 if def == nil || def.ValidateFn == nil {
  t.Fatal("memory.embedding_model ValidateFn not injected")
 }
 if err := def.ValidateFn("embedding-3"); err != nil {
  t.Fatalf("embedding-3 should pass: %v", err)
 }
 if err := def.ValidateFn("glm-4-flash"); err == nil {
  t.Fatal("chat model should be rejected for embedding param")
 }
 if err := def.ValidateFn(""); err != nil {
  t.Fatalf("empty unset sentinel should pass: %v", err)
 }
}
```

`fakeEmbeddingRegistry` 实现 `ListEmbeddingModelsByTenant(ctx) ([]string, error)` 返回注入列表。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./api/wiring/ -run TestValidateEmbeddingModelInDirectory -count=1`
Expected: FAIL（ValidateFn 未注入或走 chat 目录）。

- [ ] **Step 3: 实现**

`parameters.go` 新增：

```go
// memoryWorkerEmbeddingModelKeys 是平台级嵌入模型选择器；写时校验走
// embedding 能力目录（与 chat 校验分离，防止嵌入模型被 chat 目录拒绝）。
var memoryWorkerEmbeddingModelKeys = []string{"memory.embedding_model"}

func (c *Container) injectModelDirectoryValidation() {
 for _, key := range memoryWorkerModelKeys {
  def, ok := c.Parameters.Registry.Get(key)
  if !ok {
   continue
  }
  def.ValidateFn = c.validateModelInDirectory(key)
 }
 for _, key := range memoryWorkerEmbeddingModelKeys {
  def, ok := c.Parameters.Registry.Get(key)
  if !ok {
   continue
  }
  def.ValidateFn = c.validateEmbeddingModelInDirectory(key)
 }
}

// validateEmbeddingModelInDirectory 校验模型名存在于 enabled embedding 目录；
// 空串 = 未设置哨兵放行（fail-closed 由运行期解析负责）。
func (c *Container) validateEmbeddingModelInDirectory(key string) func(any) error {
 return func(value any) error {
  model, ok := value.(string)
  if !ok {
   return fmt.Errorf("%s: expected string model name", key)
  }
  if model == "" {
   return nil
  }
  reg := c.modelRegistryOrNil()
  if reg == nil {
   return fmt.Errorf("%s: model directory unavailable", key)
  }
  ctx, cancel := context.WithTimeout(context.Background(), constants.PlatformModelValidationTimeout)
  defer cancel()
  models, err := reg.ListEmbeddingModelsByTenant(ctx)
  if err != nil {
   return fmt.Errorf("%s: validate embedding model: %w", key, err)
  }
  for _, m := range models {
   if m == model {
    return nil
   }
  }
  return fmt.Errorf("%s: model %q not in enabled embedding model directory", key, model)
 }
}
```

确认 `llmgateway.ModelRegistry.ListEmbeddingModelsByTenant(ctx)` 存在（`internal/llmgateway/infrastructure/model_registry.go:670`）；若签名不同以实际为准（返回 `([]string, error)`）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./api/wiring/ -run TestValidateEmbeddingModelInDirectory -count=1 && go test ./api/wiring/ -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add api/wiring/parameters.go api/wiring/parameters_test.go
git commit -m "feat(parameters): validate memory.embedding_model against embedding directory"
```

## Task 4: resolver 改读平台参数 + 删除 seed

**Files:**

- Modify: `api/wiring/embedding_model.go`（整文件重写 resolver/seed）
- Modify: `api/wiring/llmgateway.go:38-45,138-145`
- Modify: `api/wiring/wiring.go:89-90`（移除 `embedding-seed` step）
- Modify: `api/wiring/memory.go:200-215`（移除 `seedMemoryEmbeddingModels` 相关；若仅 wiring.go 引用则删函数）
- Test: `api/wiring/embedding_model_test.go`、`api/wiring/knowledge_embed_resolver_test.go:106`

- [ ] **Step 1: 重写 resolver 测试（先红）**

`embedding_model_test.go`：删除 `TestIsMemoryEmbeddingModelConfigured`、`TestSeedMemoryEmbeddingModelsBackfillsOnlyMissingKeys`；`TestResolveMemoryEmbeddingModel*` 改为注入假 parameters service（`Resolver()` 返回固定 map：`memory.embedding_model → embedding-3`），覆盖三态：命中 / 未设置（fail-closed）/ 目录不可解析。`knowledge_embed_resolver_test.go:106` 的 `newTestTenantEmbeddingResolver(map[string]any{...}, registry)` helper 改为 `newTestTenantEmbeddingResolver(platformValues map[string]any, registry)`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./api/wiring/ -run 'TestResolveMemoryEmbeddingModel|TestIsMemoryEmbeddingModelConfigured|TestSeedMemoryEmbeddingModels' -count=1`
Expected: FAIL（接口不匹配/编译失败）。

- [ ] **Step 3: 实现**

`embedding_model.go` 核心：

```go
var errMemoryEmbeddingNotConfigured = errors.New(
 "memory embedding: platform parameter memory.embedding_model not configured; please set it in the platform settings page")

type tenantEmbeddingModelResolver struct {
 params   func() *parametersapp.Service // lazy：llmgateway 早于 parameters 构建
 registry *llmgateway.ModelRegistry
 logger   *zap.Logger
}

func newTenantEmbeddingModelResolver(
 params func() *parametersapp.Service,
 registry *llmgateway.ModelRegistry,
 logger *zap.Logger,
) *tenantEmbeddingModelResolver {
 return &tenantEmbeddingModelResolver{params: params, registry: registry, logger: logger}
}

func (r *tenantEmbeddingModelResolver) ResolveMemoryEmbeddingModel(ctx context.Context, _ string) (string, error) {
 if r == nil || r.params == nil || r.registry == nil {
  return "", errMemoryEmbeddingNotConfigured
 }
 svc := r.params()
 if svc == nil || svc.Resolver() == nil {
  return "", errMemoryEmbeddingNotConfigured
 }
 raw, ok, err := svc.Resolver().Resolve(ctx, "memory.embedding_model", nil)
 if err != nil {
  return "", fmt.Errorf("memory embedding: resolve platform parameter: %w", err)
 }
 model, _ := raw.(string)
 if !ok || strings.TrimSpace(model) == "" {
  return "", errMemoryEmbeddingNotConfigured
 }
 if _, _, err := r.registry.ResolveEmbeddingExact(ctx, model); err != nil {
  return "", fmt.Errorf("memory embedding: resolve model %q: %w", model, err)
 }
 return model, nil
}
```

删除 `IsMemoryEmbeddingModelConfigured`、`seedMemoryEmbeddingModels`、`seedTenantEmbeddingModels`；`llmgateway.go` 构造改：

```go
TenantEmbeddingResolver: newTenantEmbeddingModelResolver(func() *parametersapp.Service {
 if c.Parameters == nil {
  return nil
 }
 return c.Parameters.Service
}, registry, c.Logger),
```

`wiring.go` 移除 `{"embedding-seed", c.seedMemoryEmbeddingModels}` step；`memory.go` 若含 seed 调用一并删除。更新 `user_memory_handler.go:30`、`memory_migration_service.go:29` 注释。

- [ ] **Step 4: 运行确认通过**

Run: `go vet ./api/... && go test ./api/wiring/ ./api/http/handler/ -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add api/wiring/embedding_model.go api/wiring/llmgateway.go api/wiring/wiring.go api/wiring/memory.go api/wiring/embedding_model_test.go api/wiring/knowledge_embed_resolver_test.go api/http/handler/user_memory_handler.go internal/memory/application/memory_migration_service.go
git commit -m "feat(memory): resolve embedding model from platform parameter, drop tenant seed"
```

## Task 5: DLQ envelope 携带 trace_id

**Files:**

- Modify: `internal/memory/infrastructure/pipeline/dead_letter.go:18-33`
- Modify: `internal/memory/infrastructure/pipeline/embedder.go`（各 `deadLetterDetails{...}` 补 `TraceID`）
- Modify: `internal/memory/infrastructure/pipeline/enricher.go`（若有 DLQ 构造点）
- Test: `internal/memory/infrastructure/pipeline/dead_letter_test.go`、`embedder_test.go`

- [ ] **Step 1: 写失败测试**

`dead_letter_test.go` 断言 `DeadLetterEvent` JSON 含 `trace_id`：

```go
func TestDeadLetterEventIncludesTraceID(t *testing.T) {
 ev := DeadLetterEvent{MessageID: "m1", TenantID: "t1", Stage: "embed", ErrorCode: "embed_service_unavailable", TraceID: "abc123"}
 raw, err := json.Marshal(ev)
 if err != nil {
  t.Fatal(err)
 }
 if !bytes.Contains(raw, []byte(`"trace_id":"abc123"`)) {
  t.Fatalf("missing trace_id in envelope: %s", raw)
 }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/infrastructure/pipeline/ -run TestDeadLetterEventIncludesTraceID -count=1`
Expected: FAIL。

- [ ] **Step 3: 实现**

```go
type DeadLetterEvent struct {
 MessageID      string    `json:"message_id,omitempty"`
 TenantID       string    `json:"tenant_id"`
 Stage          string    `json:"stage"`
 ErrorCode      string    `json:"error_code"`
 TraceID        string    `json:"trace_id,omitempty"`
 OriginalStream string    `json:"original_stream,omitempty"`
 // ... 其余字段不变
}

type deadLetterDetails struct {
 Stage     string
 TenantID  string
 MessageID string
 ErrorCode string
 TraceID   string
}
```

`embedder.go` 所有 `deadLetterDetails{...}` 补 `TraceID: ev.TraceID`（`deadLetterWithoutEmbedder` 的 `ev` 参数、`unmarshal` 失败分支 ev 为空串、`embedding_failed`、`vector_store_unavailable`、`vector_upsert_failed`、`marshal_enriched_failed`）；`deadLetterWithHeartbeat` 组装事件时 `TraceID: details.TraceID`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/memory/infrastructure/pipeline/ -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/pipeline/dead_letter.go internal/memory/infrastructure/pipeline/embedder.go internal/memory/infrastructure/pipeline/dead_letter_test.go
git commit -m "feat(memory): carry trace_id in DLQ envelope"
```

## Task 6: 抽取队列 trace_id（tenant DDL + port + 透传 + 日志）

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql:1357-1371`
- Modify: `internal/memory/domain/port/extraction_queue.go:9-23`
- Modify: `internal/memory/infrastructure/persistence/extraction_queue.go`（Enqueue/Dequeue 增列）
- Modify: `internal/memory/infrastructure/workers/extraction_worker.go`（失败/完成日志带 trace_id）
- Modify: Redis buffer scanner enqueue 调用点（`internal/memory/infrastructure/persistence/buffer_scanner.go` 或等价文件）
- Test: `internal/memory/infrastructure/persistence/extraction_queue_test.go`

- [ ] **Step 1: 写失败测试**

`extraction_queue_test.go`：Enqueue 后 Dequeue 断言 `task.TraceID` 回读一致；MarkFailed 后行 `trace_id` 保留。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/infrastructure/persistence/ -run ExtractionQueue -count=1`
Expected: FAIL（列不存在/字段未透传）。

- [ ] **Step 3: 实现**

`tenant_schema.sql`（`conversation_id` ALTER 之后追加）：

```sql
ALTER TABLE memory_extraction_queue ADD COLUMN IF NOT EXISTS trace_id TEXT;
```

`ExtractionTask` 增加 `TraceID string`。`Enqueue` INSERT 增加列 `trace_id` 与 `NULLIF($7,'')`（参数序号按实际调整）；`Dequeue` RETURNING 与 Scan 增加 `trace_id`（`*string` → 空串）。扫描器 enqueue 时从缓冲消息透传 trace_id（无则生成 `uuid.NewString()`）。

`extraction_worker.go` `processTask` 日志（start/extract_failed/task_completed）补 `zap.String("trace_id", task.TraceID)`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/memory/... -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql internal/memory/domain/port/extraction_queue.go internal/memory/infrastructure/persistence/extraction_queue.go internal/memory/infrastructure/workers/extraction_worker.go
git commit -m "feat(memory): thread trace_id through extraction queue"
```

## Task 7: extraction worker 日志补 err + error_msg 截断

**Files:**

- Modify: `internal/memory/infrastructure/workers/extraction_worker.go:115-121`
- Modify: `internal/memory/infrastructure/persistence/extraction_queue.go:145-153`（`safeExtractionErrorCode` → 截断函数）
- Test: `internal/memory/infrastructure/persistence/extraction_queue_test.go`、`internal/memory/infrastructure/workers/extraction_worker_test.go`

- [ ] **Step 1: 写失败测试**

断言 `MarkFailed(ctx, tid, id, at, strings.Repeat("x", 300))` 后 `error_msg` 长度 ≤200。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/infrastructure/persistence/ -run MarkFailed -count=1`
Expected: FAIL（当前被归一化为 `extraction_failed`）。

- [ ] **Step 3: 实现**

```go
// truncateExtractionError 保留可诊断的底层错误文本（≤200 字符），
// 供队列侧自诊断；不记录 PII/原始响应体。
func truncateExtractionError(value string) string {
 runes := []rune(strings.TrimSpace(value))
 if len(runes) > 200 {
  runes = runes[:200]
 }
 return string(runes)
}
```

`MarkFailed` 内 `errMsg = truncateExtractionError(errMsg)`；worker `extract_failed` 分支：

```go
  w.logger.Warn("memory.extraction_worker.extract_failed",
   zap.Int64("task_id", task.ID),
   zap.String("trace_id", task.TraceID),
   zap.String("error_code", "extraction_failed"),
   zap.Error(err))
  if markErr := w.queue.MarkFailed(ctx, task.TenantID, task.ID, task.UpdatedAt, err.Error()); markErr != nil {
   w.logger.Error("memory.extraction_worker.mark_failed_failed", zap.Int64("task_id", task.ID), zap.Error(markErr))
  }
```

panic 分支传 `fmt.Sprintf("extraction_panic: %v", r)`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/memory/infrastructure/... -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/workers/extraction_worker.go internal/memory/infrastructure/persistence/extraction_queue.go
git commit -m "fix(memory): log err and keep diagnostic text in extraction queue"
```

## Task 8: 提示词兜底移除（后端）

**Files:**

- Modify: `pkg/constants/memory.go:228-285`（删五个 `Memory*DefaultPrompt`）
- Modify: `internal/parameters/application/service.go:102-118`（`PromptDefaults()` 删 memory 键）
- Modify: `internal/memory/infrastructure/pipeline/enricher_prompt.go`（去 constants 回退）
- Modify: `internal/memory/infrastructure/pipeline/enricher.go:383-400,515-530`（空模板报错）
- Modify: `internal/memory/infrastructure/workers/llm_superseder.go:60-66`、`history_summarizer.go:55-62`
- Modify: `internal/memory/infrastructure/pipeline/llm_extractor.go:16-110`（完整提示词 + 占位符）
- Modify: `internal/parameters/domain/registry.go:545-621`（注释/Description 更新）
- Test: 各 worker 测试 + `parameter_handler_test.go:78-121`

- [ ] **Step 1: 写失败测试**

- `enricher_test.go`：`memory.enrich_prompt` 解析为空 → `callEnrichLLM` 返回含 `enrich_prompt` 的错误，不调用 LLM。
- `llm_extractor_test.go`：`memory.extraction_prompt` 未配置 → `ExtractFacts` 返回错误；配置含 `{user_id}/{agent_id}/{max_facts}` → 渲染替换后发送。
- `llm_superseder_test.go` / `history_summarizer_test.go`：空 prompt → 错误。
- `parameter_handler_test.go:78-121`：`TestPromptDefaults_returnsAllWhitelistedTemplates` 收敛为仅断言 `agent.compaction_prompt` 非空，删除 `constants.MemoryExtractionDefaultPrompt` import 与五个 memory 键断言。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/memory/... ./api/http/handler/ -count=1`
Expected: FAIL（编译失败/行为不符）。

- [ ] **Step 3: 实现**

`enricher_prompt.go`：

```go
func formatEnrichmentPrompt(tmpl, role, content string) string {
 return fmt.Sprintf(tmpl, role, content) // 调用方保证 tmpl 非空
}

func formatSummaryPrompt(tmpl, conversation string) string {
 return fmt.Sprintf(tmpl, conversation)
}
```

`enricher.go` `callEnrichLLM`：

```go
 promptTmpl := w.resolvePlatformString(ctx, "memory.enrich_prompt", "")
 if strings.TrimSpace(promptTmpl) == "" {
  return nil, fmt.Errorf("memory enrich: memory.enrich_prompt not configured (fail-closed)")
 }
 prompt := formatEnrichmentPrompt(promptTmpl, role, content)
```

`maybeTriggerSummary` 空模板：`w.logger.Error("memory.summary.skip_prompt_not_configured", ...)` + 计数指标后 return（不阻塞 enrich）。

`llm_superseder.go` / `history_summarizer.go`：`resolvePlatformString(..., "")` 后 `strings.TrimSpace(prompt) == ""` → `return nil, fmt.Errorf("..._prompt not configured (fail-closed)")`。

`llm_extractor.go`：删除 `extractionIdentityPrompt`；`extractionPrompt` 改为：

```go
// extractionPrompt 解析完整系统提示词（memory.extraction_prompt，resource scope），
// 渲染 {user_id}/{agent_id}/{max_facts} 占位符；空模板 → 错误（fail-closed）。
func (e *LLMExtractor) extractionPrompt(ctx context.Context, agentID, userID string, maxFacts int) (string, error) {
 if e.resolver == nil {
  return "", fmt.Errorf("memory extraction: memory.extraction_prompt not configured (fail-closed)")
 }
 v, ok, err := e.resolver.Resolve(ctx, e.tenantID, agentID, "memory.extraction_prompt")
 if err != nil {
  return "", fmt.Errorf("memory extraction: resolve prompt: %w", err)
 }
 s, ok := v.(string)
 if !ok || strings.TrimSpace(s) == "" {
  return "", fmt.Errorf("memory extraction: memory.extraction_prompt not configured (fail-closed)")
 }
 s = strings.ReplaceAll(s, "{user_id}", userID)
 s = strings.ReplaceAll(s, "{agent_id}", agentID)
 s = strings.ReplaceAll(s, "{max_facts}", strconv.Itoa(maxFacts))
 return s, nil
}
```

`ExtractFacts` 调用处适配 `(prompt, err)` 并返回错误。`registry.go` 五个 prompt 的 Description 中"空表示默认模板"改为"未配置即失败（fail-closed）"；`registerMemoryWorkerParams` 注释"Defaults stay at the current const fallback"更新。

- [ ] **Step 4: 运行确认通过**

Run: `go vet ./... && go test ./internal/memory/... ./internal/parameters/... ./api/http/handler/ -count=1`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/constants/memory.go internal/parameters/application/service.go internal/parameters/domain/registry.go internal/memory/infrastructure/pipeline/ internal/memory/infrastructure/workers/ internal/memory/infrastructure/workers/llm_superseder.go internal/memory/infrastructure/workers/history_summarizer.go api/http/handler/parameter_handler_test.go
git commit -m "feat(memory): remove hardcoded prompt fallbacks, fail closed when unset"
```

## Task 9: 前端提示词展示清理

**Files:**

- Modify: `web/src/modules/parameters/pages/PlatformSettingsPage.tsx:16-22`（`PROMPT_DEFAULT_KEYS` 移除 memory 键）
- Modify: `web/src/modules/agent/components/AgentMemoryConfig.tsx:55-70`（去 viewer，改必填文案）
- Test: `web/src/modules/agent/components/AgentFormSections.test.tsx:429`、`AgentMemoryConfig.test.tsx:31-37`

- [ ] **Step 1: 写失败测试/更新断言**

`AgentMemoryConfig.test.tsx`：断言文案为"必填：完整系统提示词，支持 {user_id}/{agent_id}/{max_facts} 占位符"且不渲染"查看默认提示词"；`AgentFormSections.test.tsx` 移除两个"查看默认提示词"按钮断言。

- [ ] **Step 2: 运行确认失败**

Run: `npm --prefix web test -- agent/components/__tests__/AgentMemoryConfig.test.tsx agent/components/AgentFormSections.test.tsx`
Expected: FAIL。

- [ ] **Step 3: 实现**

`PlatformSettingsPage.tsx`：`PROMPT_DEFAULT_KEYS` 改为空集合并删除 `PromptDefaultViewer` 分支；`AgentMemoryConfig.tsx`：删除 `PromptDefaultViewer` 引用，`memory.extraction_prompt` 字段 tooltip/extra 改为必填说明。

- [ ] **Step 4: 运行确认通过**

Run: `npm --prefix web test -- agent/components/__tests__/AgentMemoryConfig.test.tsx agent/components/AgentFormSections.test.tsx`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/parameters/pages/PlatformSettingsPage.tsx web/src/modules/agent/components/AgentMemoryConfig.tsx web/src/modules/agent/components/AgentFormSections.test.tsx web/src/modules/agent/components/__tests__/AgentMemoryConfig.test.tsx
git commit -m "feat(web): remove prompt default viewers, extraction prompt is required"
```

## Task 10: 平台参数页加载态 + 清空语义

**Files:**

- Modify: `web/src/modules/parameters/pages/PlatformSettingsPage.tsx:63-72,96-113,131-160`
- Test: `web/src/modules/parameters/pages/__tests__/PlatformSettingsPage.test.tsx`

- [ ] **Step 1: 写失败测试**

新增用例：`loading=true` 时仅渲染 Skeleton（`document.querySelector('.ant-skeleton')` 存在，且无"保存平台参数"按钮）；`memory.embedding_model` 清空后提交包含 `memory.embedding_model: ''`。

- [ ] **Step 2: 运行确认失败**

Run: `npm --prefix web test -- parameters/pages/__tests__/PlatformSettingsPage.test.tsx`
Expected: FAIL。

- [ ] **Step 3: 实现**

```tsx
  if (loading) {
    return (
      <div style={{ maxWidth: 960, margin: '0 auto', padding: '24px 16px' }}>
        <Skeleton active paragraph={{ rows: 8 }} />
      </div>
    );
  }
```

`onFinish`：`embedding_model` 控件的空值（`''`）直接进 patch（跳过 `v === def.default` 的 skip 分支）；`PlatformFieldItem` 对 `control === 'embedding_model'` 且 unset 时 hint 显示"未设置（记忆写入将失败并告警）"。

- [ ] **Step 4: 运行确认通过**

Run: `npm --prefix web test -- parameters/pages/__tests__/PlatformSettingsPage.test.tsx && make fe-lint && make fe-build`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/parameters/pages/PlatformSettingsPage.tsx web/src/modules/parameters/pages/__tests__/PlatformSettingsPage.test.tsx
git commit -m "fix(web): platform settings loading state and clear-to-unset semantics"
```

## Task 11: 迁移卡片下线（前端）

**Files:**

- Modify: `web/src/modules/iam/pages/tenant/SettingsPage.tsx:6,74`
- Delete: `web/src/modules/iam/components/MemoryMigrationCard.tsx`、`web/src/modules/iam/hooks/useMemoryMigration.ts`、`web/src/modules/iam/api/memory-migration.api.ts` 及对应测试
- Modify: `web/src/constants/index.ts:99`（删 `MEMORY_MIGRATION_POLL_MS`）
- Modify: `web/src/modules/iam/pages/tenant/SettingsPage.test.tsx:15-16`

- [ ] **Step 1: 删除引用并更新测试**

移除 `SettingsPage.tsx` 的 `MemoryMigrationCard` import 与 `<MemoryMigrationCard />`；`SettingsPage.test.tsx` 删除 `vi.mock('../../components/MemoryMigrationCard')`。

- [ ] **Step 2: 运行确认**

Run: `npm --prefix web test -- iam/pages/tenant/SettingsPage.test.tsx && make fe-lint`
Expected: PASS。

- [ ] **Step 3: 删除文件并确认无残留引用**

`rg -n "MemoryMigration|memoryMigration|MEMORY_MIGRATION_POLL_MS" web/src` 应为空（除已删除文件）；`git rm` 删除 5 个前端文件。

- [ ] **Step 4: Commit**

```bash
git add -A web/src
git commit -m "feat(web): remove tenant memory migration card"
```

## Task 12: 迁移后端下线 + 告警退役

**Files:**

- Modify: `api/http/router.go:284,610-620`
- Delete: `api/http/handler/memory_migration_handler.go`、`api/http/handler/memory_migration_handler_test.go`
- Modify: `api/wiring/memory.go:181-215`（删 `buildMemoryMigration` 调用与定义）、`api/wiring/memory.go:453-470`（`appendMigrationWorker`）
- Modify: `monitoring/local/rules/stratum-ai.yml`（删 `StratumMemoryMigrationStalled`）
- Test: `api/http/contract_test.go` golden 核对

- [ ] **Step 1: 移除路由/构造/告警**

删除 `router.go` 中 `registerMemoryMigrationRoutes(...)` 调用、函数定义与 `MemoryMigrationHandler` 构造；`wiring/memory.go` 删除 `buildMemoryMigration(mem, db)` 调用与函数、`appendMigrationWorker` 及调用；`git rm` handler 与其测试；`stratum-ai.yml` 删除 migration 告警块。

- [ ] **Step 2: 契约核对**

`rg -n "memory/migrations" api/http/testdata/contracts api/http/contract_test.go`；若 golden 含迁移端点，删除对应 golden 条目并重新生成。

- [ ] **Step 3: 运行确认**

Run: `go vet ./api/... && go test ./api/... -count=1 && make code-quality`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add -A api monitoring
git commit -m "feat(memory): remove tenant memory migration backend and alert"
```

## Task 13: 模型目录放开删除

**Files:**

- Modify: `internal/llmgateway/infrastructure/model_repo.go:411-420`
- Modify: `internal/llmgateway/application/model_mgmt_service.go:329-333`
- Modify: `web/src/modules/llm/pages/ModelListPage.tsx:86-110`
- Test: `internal/llmgateway/infrastructure/model_repo_test.go:264-290`

- [ ] **Step 1: 改测试（先红）**

`TestPgModelRepo_DeleteProviderManaged` 改为 `TestPgModelRepo_DeleteProviderManagedAllowed`：Create 后 `Delete` 成功、`Get` 报 not found。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/llmgateway/infrastructure/ -run TestPgModelRepo_DeleteProviderManaged -count=1`
Expected: FAIL。

- [ ] **Step 3: 实现**

`model_repo.go`：

```go
// Delete removes a model by ID regardless of provider-managed flag.
// 删除后该厂商再次“发现模型”会按 upsertSyncModel 重新插入。
func (r *PgModelRepo) Delete(ctx context.Context, id string) error {
 tag, err := r.pool.Exec(ctx, `DELETE FROM public.models WHERE id=$1`, id)
 if err != nil {
  return fmt.Errorf("delete model: %w", err)
 }
 if tag.RowsAffected() == 0 {
  return fmt.Errorf("model not found: %s", id)
 }
 return nil
}
```

`ModelListPage.tsx` 确认框文案追加："该模型由厂商发现管理，删除后再次执行发现模型可能重新加入目录；若为默认嵌入模型或被参数引用，相关解析将失败，请先调整配置。"

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/llmgateway/... -count=1 && make fe-lint`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/infrastructure/model_repo.go internal/llmgateway/infrastructure/model_repo_test.go internal/llmgateway/application/model_mgmt_service.go web/src/modules/llm/pages/ModelListPage.tsx
git commit -m "feat(llmgateway): allow deleting provider-managed models"
```

## Task 14: 全量质量门禁 + E2E 验收（stratum-e2e-development）

- [ ] **Step 1: 后端全量**

Run: `bash scripts/quality/risk-regression-guard.sh --explain && go vet ./... && go test -short ./... && make code-quality && make risk-guardrails`
Expected: 全绿。

- [ ] **Step 2: 前端全量**

Run: `make fe-lint && make fe-build && npm --prefix web test`
Expected: 全绿。

- [ ] **Step 3: 本地 E2E（headless）**

按 `stratum-e2e-development` skill 启动本地后端+前端，验证：

1. `/admin/settings` 刷新无前置展示页（页面级 Skeleton → 配置表单）。
2. 未配置 `memory.embedding_model`：发消息 → `memory_embed_unavailable_total` 增加、DLQ 出现 `embed_service_unavailable`。
3. 平台页配置 `memory.embedding_model=embedding-3` 与 `memory.enrich_prompt`（本地手工配置含 `%s/%s` 模板）→ 新消息走通 outbox→embed→enrich→`memory_entities` 增长。
4. 模型目录删除一个 provider-managed 模型成功并刷新读回。
5. 租户设置页无迁移卡片；`/tenant/memory/migrations*` 返回 404。
6. 构造 extraction 未配置提示词任务：worker 日志含 `err` 与 `trace_id`，队列 `error_msg` 为截断错误。
7. 平台页清空 `memory.embedding_model` 提交成功（回退 fail-closed）。

- [ ] **Step 4: 清理**

删除 `tmp-` 前缀临时脚本/Playwright spec；停止自启动进程。

## Task 15: 提交 → PR → CI → 合并 → CD 部署

- [ ] **Step 1: 提交并推送**

```bash
git push -u origin feat/memory-embed-platform-param
gh pr create --base main --title "[feat](memory): memory embedding model platform parameter and ops fixes" --body "What/Why/HowToTest..."
```

- [ ] **Step 2: 等待 CI 全绿**

`gh pr checks --watch`。若 base 落后 `origin/main`：`git fetch origin main && git merge origin/main`，本地验证后 push。

- [ ] **Step 3: 合并并确认 CD 部署**

`gh pr merge --merge`（CI 合并门禁通过后）；等待 GitHub Actions CD workflow 部署到远端 k3s。

- [ ] **Step 4: 部署验证（只读）**

`kubectl -n stratum get pods -l app.kubernetes.io/name=stratum`；`kubectl logs` 确认无 `memory.embed.resolve_failed` 之外的启动错误；平台参数页/远端 API 抽查：`/admin/parameters` 含 `memory.embedding_model`；模型删除接口可删 provider-managed 模型；`/tenant/memory/migrations` 返回 404。

- [ ] **Step 5: 清理 worktree**

`git worktree remove /home/yang/go-projects/stratum-memory-embed-param`（合并后）。
