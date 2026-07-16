# LLM 集成指南

## 当前架构

LLM 访问由 `internal/llmgateway/` bounded context 统一处理：

- `Gateway` 根据模型名选择 provider，并统一非流式、流式和 embedding 调用。
- `TenantGatewayCache` 缓存 tenant-scoped gateway 与 API key 快照。
- `api/wiring/tenant_resolver.go` 从租户 settings 解析 provider 配置并装配 Agent 运行时能力。
- `llmgateway.WithCompleter` / `CompleterFromContext` 用于把当前请求的 completer 传入需要 LLM 的应用流程。

业务层不应直接创建 provider HTTP client，也不应从全局环境变量读取 provider API key。

## 已实现 Provider

| Provider | 实现 | 模型识别 |
|---|---|---|
| Qwen | OpenAI-compatible client | `qwen-*`、`text-embedding-v3*` |
| Zhipu | OpenAI-compatible client | `glm-*`、`embedding-*` |

`internal/llmgateway/domain.ProviderKind` 中仍有 `openai`、`anthropic`、`ollama` 类型常量，但当前 `Gateway` 没有为它们注册运行时 client，不能据此宣称这些 provider 已可用。

模型目录由 `internal/llmgateway/infrastructure/static_catalog.go` 提供；公开接口为：

```text
GET /models
```

## 租户配置

当前用户通过 tenant settings 配置 API key 和默认模型：

```text
GET   /tenant/settings
PATCH /tenant/settings
PATCH /tenant/embed-model
```

这些路由需要 JWT 和 tenant context；写操作还要求 active tenant。具体字段以 `api/http/dto`、`TenantService` 和前端 tenant settings 表单为准。

API key 存储在租户 settings 的 `llm_api_keys` 映射中，并通过平台加密组件处理。日志、错误响应和文档都不得输出原始 key。

## Agent 调用链

```text
HTTP request
  -> AgentService 解析 tenant capability
  -> TenantGatewayCache / resolver
  -> BaseAgent + ReAct graph
  -> CapabilityGateway (CapLLM)
  -> llmgateway.Gateway
  -> Qwen or Zhipu OpenAI-compatible client
```

Agent 可使用普通执行或 SSE 流式执行：

```text
POST /agents/:id/execute
POST /agents/:id/execute/stream
```

两者都需要 member 权限、active tenant，并受 tenant/user 维度的 LLM 执行限流保护。

## 超时与流式约束

超时常量集中在 `pkg/constants/timeouts.go`。流式请求使用 response-header timeout 与 token idle watchdog，不使用覆盖整个生成过程的单一短 deadline。Agent 总执行时间由 `AgentExecTimeout` 限制。

SSE handler 通过 token callback 持续写出 `data:` 帧；客户端断开会取消执行。具体时序见 [Agent 会话完整流程](agent/agent-chat-flow.md)。

## Embedding

Knowledge 与 Memory 通过 embedding client 生成向量。租户可用 `PATCH /tenant/embed-model` 设置 embedding model；模型必须能被当前 Qwen/Zhipu provider 识别。向量维度必须与 Milvus collection schema 一致。

## 故障排查

- `GET /models`：确认服务公开的模型目录。
- `GET /tenant/settings`：确认当前 tenant 已设置默认模型和对应 provider key；不要在终端录屏或日志中展示原始值。
- 查看结构化 `llm.complete` 日志事件，只检查 provider、model、status、latency 和 token 统计。
- 流式中断时检查 `LLMStreamIdleTimeout`、provider 首字节延迟和反向代理缓冲设置。
- embedding 失败时同时核对模型归属、向量维度和 Milvus collection schema。

快速操作流程见 [QUICKSTART_LLM.md](QUICKSTART_LLM.md)。
