# llmgateway 三个缺口：rerank 一等协议 / think 流式 / 任务策略

日期：2026-08-14
状态：待实施（spec 已批准）
范围：`internal/llmgateway`、`internal/memory`、`internal/evaluation`、`internal/agent`、`internal/knowledge`、`api/http`、`web/src`

## 1. 背景与目标

方案 A 的直觉（共享公共层 + 任务策略 + rerank 独立协议）正确，但"继承 adapter"的落法在本仓库会与既有架构冲突。现状代码已经把两轴解耦：**传输编排**（fallback/能力门控/重试/指标/token 统计）集中在 `Gateway`，**协议**（wire 格式）按 provider kind 分 `ChatProtocol`/`EmbedProtocol`，任务差异只是 `CompletionRequest` 构造。因此不引入类继承，改为：共享 `Gateway` 不动，任务差异落到 application 层 request 构造器，rerank 提升为一等协议。

三个具体缺口：

1. **rerank 一等协议**：rerank 已有独立实现（`knowledge/infrastructure/rerank/cohere.go`），但凭据走平台 config.yaml，不进模型目录，无法跨 context 复用。提升为 llmgateway 一等协议，凭据迁到 catalog provider 行。
2. **think 流式解析**：`ReasoningEffort` 能发出去，但 `reasoning_content`/thinking blocks 无人接收，前端拿不到思考过程。补齐协议层解析，SSE 流式推给前端。
3. **任务策略去重**：memory/evaluation 各 worker inline 拼 `CompletionRequest`。收敛为 llmgateway application 的统一策略包，prompt 仍留在消费方。

## 2. 现状（代码证据）

| 事实 | 位置 |
|---|---|
| 传输编排集中点 | `internal/llmgateway/infrastructure/gateway.go`（fallback 链、`applyCapabilityGate`、metrics、token 统计） |
| 协议接口 | `internal/llmgateway/infrastructure/protocol.go`：`ChatProtocol`/`EmbedProtocol` |
| `CompletionRequest` 已有全部旋钮 | `internal/llmgateway/domain/llm.go:65`（Temperature/MaxTokens/Tools/ToolChoice/ResponseFormat/ReasoningEffort/NoPrimaryRetry/MaxCandidates） |
| rerank 独立实现 | `internal/knowledge/infrastructure/rerank/cohere.go` + `internal/knowledge/domain/port/reranker.go` |
| rerank 目录能力已存在 | `internal/llmgateway/domain/model.go:14` `CapRerank`；`provider.go:29` `ProviderCohere`；`model_registry.go:509` `supports()` 已把 CapRerank 门控到 cohere |
| Anthropic 请求侧 thinking budget 已存在 | `internal/llmgateway/infrastructure/anthropic.go`（`thinkingForBudget`/`effortBudget`）；响应侧 thinking 未解码 |
| 结构化抽取编排已部分存在 | `internal/memory/infrastructure/pipeline/structured_output.go`（`CompleteStructured` 带错重试） |
| SSE 框架 | `api/http/handler/sse_writer.go`（命名事件）+ 前端 `web/src/services/client.ts` `consumeSSE`（按 `event:` 分派） |
| agent 流式回调链 | `internal/agent/application/graph/react_llm.go`（`TokenStream`） |

## 3. 架构不变式

- `Gateway` 保持传输编排职责，不复制、不拆类继承。
- 任务差异 = request 构造 + 结构化解析：request 构造落 `llmdomain` 领域构造层，结构化重试内核拆「非泛型内核（llmdomain）+ 泛型外壳（消费方）」。
- 策略禁止落 `llmgateway/application`：CLAUDE.md 禁消费方 import 兄弟 context 的 application；`CompleteStructured` 是泛型函数，Go 泛型方法无法放入接口，经 wiring ACL 透传不可行。`llmdomain`（domain）不在此禁列，是唯一可跨 context import 的位置。
- 分层：`llmgateway/{domain, application, infrastructure}`；domain 仅依赖 stdlib + `pkg/constants`；application 不 import pgx/Redis/NATS/Gin。
- prompt 字符串归属消费方 context（memory 指令、evaluation rubric），llmgateway 只持有结构默认值。
- 行为数字进 `pkg/constants/`，禁止内联。

## 4. Workstream ③ 任务策略（落点：llmdomain 领域层）

新增 `internal/llmgateway/domain/` 领域构造层（domain 仅依赖 stdlib + `pkg/constants`，不 import zap）：

- `NewChatRequest(model, msgs, tools, effort) *CompletionRequest`：原样透传。agent 现行链路可选接入，本次不强制。
- `NewSummarizeRequest(model, instructions, items, maxTokens) *CompletionRequest`：单轮 user，`Temperature=0.2`（`pkg/constants`），锁 `MaxTokens`（参数传入），无 tools，`NoPrimaryRetry=true`（压缩路径语义：时间片内主模型一次失败直接降级候选）。
- `NewExtractRequest(model, system, user, temperature, maxTokens) *CompletionRequest`：`ResponseFormat=json_object`，由 gateway `applyCapabilityGate` 按模型能力清空 fail-closed；temp/maxTokens 显式参数，消费方传各自常量。
- `StructuredRetryLoop(ctx, client Completer, req, maxRetries, attempt) (string, error)`：非泛型重试内核（stdlib，不 import zap）。循环克隆 req、设 `response_format`、append correction（system role）、带错重试、attempts 统计；全部失败返回带白名单摘要（字段名）的 error。`FieldError` 接口：消费方 `ValidationError` 实现 `Field() string`，白名单降级日志。
- 消费方保留薄泛型 `CompleteStructured[T]` 外壳（调内核），JSON 解析、逐条校验、部分成功语义（≥1 通过返回子集）留本地。
- 消费方重构：`llm_extractor`/`enricher`/`llm_superseder`/`history_summarizer`/`evaluation judge` 改走 builder，删除 inline 构造。prompt 字符串经构造参数注入。
- `memport` 传输镜像（`CompletionRequest`/`Completer`/`ResponseFormat`/`CompletionResponse`）退役；`memoryLLMAdapter` 收敛为仅 tenantID 注入。`port.ValidationError`/`ExtractedFact`/`SupersedeJudgment`/`LLMExtractor`/`LLMSuperseder` 保留（memory 领域类型）。

决策要点：

- Extract 的 `ResponseFormat` 由 gateway 能力门控兜底（不支持 json_object 的模型清空字段），请求侧可无条件设置。
- `StructuredRetryLoop` 内核上移 llmdomain，`memory/pipeline` 不再持有重试循环；移动测试随迁。
- 行为数字进 `pkg/constants`：`TaskSummarizeTemperature`（0.2）入 `pkg/constants/llmgateway.go`；extract/summarize 的 maxTokens、enrich temp 沿用/新增 `pkg/constants/memory.go` 常量。

## 5. Workstream ② think 流式解析

### 5.1 domain

- `domain.CompletionResponse` 增加 `Thinking string`（openai_compat 累加 `reasoning_content`；Anthropic 累加 thinking blocks 为单字符串，YAGNI 不引入块数组）。
- 新增 `domain.StreamDelta{ Text string; Thinking string }`。

### 5.2 协议接口改动（主要机械成本）

- `ChatProtocol.CompleteStream` 回调签名：`onToken func(string)` → `onStream func(StreamDelta)`。
- 波及：`protocol.go` 接口、`gateway.invokeStream`、openai_compat/anthropic/ollama（ollama 无脑传空 Thinking）、agent graph `TokenStream` 回调链、memory completers。
- 不引入 V2 平行接口；诚实改签名。

### 5.3 openai_compat

- 非流式：`openAICompletionResp.Message` 加 `ReasoningContent string json:"reasoning_content"` → 填 `CompletionResponse.Thinking`。
- 流式：`openAIStreamChunk.Delta` 加 `ReasoningContent string json:"reasoning_content"`，`applyStreamChunk` 累加进 Thinking 并随 `StreamDelta.Thinking` 转发。

### 5.4 anthropic

- 请求侧 thinking budget 已存在，只补响应侧。
- 非流式：`content[]` 中 `type:"thinking"` 块解码进 `Thinking`。
- 流式：SSE `content_block_delta`（`thinking_delta`）累加进 `Thinking` 并转发。

### 5.5 SSE 出口 + 前端

- agent 执行流新增 `thinking` 命名事件（`{delta: string}`），在 content 之前/并行推送。
- 前端聊天页：新增折叠面板"思考过程"，`consumeSSE` 按 `event:` 分派 `thinking` 增量。常量与命名遵循 `web/src/constants/` 与 frontend 规范。

## 6. Workstream ① rerank 一等协议（目录凭据）

### 6.1 domain

- llmgateway domain 定义规范类型 `RerankRequest{Query, Documents, Model, TopN}`、`RerankResult{Index, Score}`（对齐 knowledge port 现有结构）。
- knowledge 保留本地 port 镜像（既有 cross-context 模式：`knowledge/domain/port/reranker.go` 已存在），wiring 适配，禁止 knowledge import llmgateway infrastructure。

### 6.2 infrastructure

- `protocol.go` 加 `RerankProtocol{ Rerank(ctx, cfg ProviderConfig, req *RerankRequest) (*RerankResponse, error) }`。
- `ModelRegistry` 加 `rerankProtos map[ProviderKind]RerankProtocol` + `ResolveRerank(ctx, modelName)`，复用 5 级解析链（① 精确 → ② provider default → ③ recommended → ⑤ fail-closed，无 embedding 兜底）。缓存键 `"rerank:"+modelName`。
- `CohereReranker` 从 `knowledge/infrastructure/rerank/` 迁入 llmgateway infrastructure 作为协议适配器：凭据改读 `ProviderConfig`（cohere provider 行 baseURL/APIKey），删除 config.yaml 版本与 `knowledge/infrastructure/rerank/` 目录。
- 无 fallback 链（单 provider）。

### 6.3 wiring + 迁移

- `api/wiring/`：注册 cohere rerank 协议；knowledge RAG 的 `Reranker` port 由 llmgateway `ResolveRerank` 适配实现。
- 存量配置迁移：config.yaml 的 rerank baseURL/apiKey/model → catalog cohere provider 行（一次性脚本，复用 `cmd/fix-provider-keys` 模式；spec 落地时确认 cohere provider 行是否已存在）。

## 7. 实施顺序

**③ → ② → ①**：

1. ③ 任务策略（纯重构，风险最低，打地基）
2. ② think（用户价值最高，接口签名改动波及面明确）
3. ① rerank（带迁移 + wiring，收尾）

每个 workstream 独立可合入（各自 commit + 独立验证）。

## 8. 测试与成功标准

| Workstream | 测试 |
|---|---|
| ③ | llmdomain request builder 单测（temp/max_tokens/tools/NoPrimaryRetry/json 断言）；`StructuredRetryLoop` 内核单测（带错重试/白名单摘要）；memory worker 既有测试全绿（行为契约不变） |
| ② | openai_compat 非流式/流式 `reasoning_content` 契约测试；anthropic thinking 块测试（非流式 + SSE delta）；SSE `thinking` 事件测试；前端渲染（headless） |
| ① | `ResolveRerank` 解析链测试；cohere 协议适配器测试（迁移现有 `cohere_test.go`）；knowledge RAG rerank 接线集成测试 |

通用门禁：`make code-quality`（圈复杂度 ≤10 / 认知 ≤15 / 行数 ≤120 / 嵌套 ≤4）；`go vet && go test -short ./...`；PR 前 `go test -v -race -timeout 30s ./...`；API 兼容由 `api/http/contract_test.go` 守护（think 若改 DTO/proto 走 `make proto-gen`）。

## 9. 风险与迁移

- **接口签名改动（②）**：`CompleteStream` 回调签名波及 agent graph 与 memory completers，属机械改动但面广；用 `StreamDelta` 一次性收敛，避免 V2 平行接口永久残留。
- **rerank 凭据迁移（①）**：config.yaml → catalog provider 行；cohere provider 行缺失时需先建行。破坏性迁移单独验证。
- **循环内核提取（③）**：`StructuredRetryLoop` 的带错重试/降级摘要语义必须随迁不丢失，测试随迁；部分成功语义（≥1 通过返回子集）保留在消费方外壳，行为契约不变。

## 10. 范围外

- Agent 是否采用 `NewChatRequest`（可选，不强推，本次不接入）。
- Anthropic thinking 多块结构化展示（YAGNI，单字符串）。
- rerank fallback 链（单 provider，不做）。
- 其他 provider 的 rerank 端点（本次仅 cohere）。
