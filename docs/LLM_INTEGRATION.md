# LLM 集成指南

## 当前架构

LLM 访问由 `internal/llmgateway/` bounded context 统一处理：

- `Gateway` 根据模型名通过 `ModelRegistry` 解析 provider，并统一非流式、流式和 embedding 调用。
- `ModelRegistry` 缓存 tenant-scoped provider/model 解析结果。
- `api/wiring/tenant_resolver.go` 从统一模型目录装配 Agent 运行时能力。
- `llmgateway.WithCompleter` / `CompleterFromContext` 用于把当前请求的 completer 传入需要 LLM 的应用流程。

业务层不应直接创建 provider HTTP client，也不应从全局环境变量读取 provider API key。

## 已实现 Provider

| Provider | 实现 | 模型识别 |
|---|---|---|
| Qwen | OpenAI-compatible client | `qwen-*`、`text-embedding-v3*` |
| Zhipu | OpenAI-compatible client | `glm-*`、`embedding-*` |

`internal/llmgateway/domain.ProviderKind` 中仍有 `openai`、`anthropic`、`ollama` 类型常量，但当前 `Gateway` 没有为它们注册运行时 client，不能据此宣称这些 provider 已可用。

模型目录由 tenant schema 中的 `providers` 与 `models` 表提供；认证接口为：

```text
GET /models
```

## 模型配置

租户管理员通过“模型管理”配置 provider、API key 和可用模型：

```text
GET/POST/PUT/DELETE /admin/providers
POST                /admin/providers/:id/discover
GET/PUT/PATCH/DELETE /admin/models
```

这些路由需要 JWT、tenant context、管理员权限和 active tenant。租户设置不再接收或返回模型配置。

## Agent 调用链

```text
HTTP request
  -> AgentService 解析 tenant capability
  -> ModelRegistry / resolver
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

Knowledge 与 Memory 通过 embedding client 生成向量。知识库创建时只能选择模型管理中已启用、具备 embedding 能力且 provider 可用的模型。向量维度必须与 Milvus collection schema 一致。

## 故障排查

- `GET /models`：确认服务公开的模型目录。
- `GET /models`：确认当前 tenant 的 chat 与 embedding 可用目录。
- 查看结构化 `llm.complete` 日志事件，只检查 provider、model、status、latency 和 token 统计。
- 流式中断时检查 `LLMStreamIdleTimeout`、provider 首字节延迟和反向代理缓冲设置。
- embedding 失败时同时核对模型归属、向量维度和 Milvus collection schema。

快速操作流程见 [QUICKSTART_LLM.md](QUICKSTART_LLM.md)。
