# LLM 集成指南

## 架构概述

LLM 访问通过 `internal/llmgateway/` bounded context 统一抽象。核心特点：

- **租户级 API Key 管理**：每个租户独立配置自己的 LLM provider 和 API key，存储在 `tenants.settings` 的 `api_keys` 字段（加密存储），不通过全局环境变量注入。
- **`LLMGateway` 接口**：屏蔽 OpenAI / Anthropic / Qwen / Zhipu / Ollama 差异，业务层只依赖 `port.LLMGateway`。
- **`TenantGatewayCache`**：热路径复用已初始化的 gateway，避免每次请求重建 HTTP 客户端。
- **`llmgateway.WithCompleter(ctx, completer)`**：通过 context 注入当前租户的 completer，无需在业务函数签名中传递。

---

## Provider 支持

| Provider | 类型 | 说明 |
|---|---|---|
| Qwen（阿里云）| OpenAI 兼容 | 国内首选，`qwen-turbo` 用于记忆提取 |
| Zhipu（智谱）| 原生 | GLM 系列 |
| OpenAI | 原生 | GPT-4 / GPT-3.5-turbo |
| Anthropic | 原生 | Claude 系列 |
| Ollama | 本地 | 私有化部署，`OLLAMA_ENDPOINT` 配置 |

---

## 租户 API Key 配置（运行时）

API key 通过管理接口配置，不需要重启服务：

```bash
# 为租户设置 LLM provider API key
curl -X PUT http://localhost:8080/admin/tenants/{tenantID}/settings \
  -H "Authorization: Bearer <admin_token>" \
  -H "Content-Type: application/json" \
  -d '{
    "llm_provider": "qwen",
    "llm_model": "qwen-turbo",
    "llm_api_key": "<your-api-key>"
  }'
```

key 在写入前由 platform 层加密，读取时自动解密后注入 gateway client。

---

## 本地开发（env 覆盖）

本地开发可在 `.env` 中为 `tenant_default` 预置 key，供开发期快速启动：

```env
DEFAULT_LLM_PROVIDER=qwen
DEFAULT_LLM_MODEL=qwen-turbo
DEFAULT_LLM_API_KEY=sk-xxxxxxxx
OLLAMA_ENDPOINT=http://localhost:11434
```

`.env.example` 中包含所有可用字段，不要提交真实 key。

---

## 代码使用

### Handler / Application 层

业务层通过 context 获取 completer，不直接操作 gateway 实例：

```go
// application/agent_service.go
func (s *AgentService) Execute(ctx context.Context, ...) {
    completer := llmgateway.CompleterFromContext(ctx)
    result, err := completer.Complete(ctx, messages, opts)
}
```

### Gateway 初始化（Wiring）

`api/wiring/llmgateway.go` 在 `BuildContainer` 时装配：

```go
gatewayCache := llmgateway.NewTenantGatewayCache(cfg, logger)
container.GatewayCache = gatewayCache
```

Auth middleware 在验证 JWT 后，从 `TenantGatewayCache` 取出对应租户的 gateway，
并通过 `llmgateway.WithCompleter(ctx, completer)` 注入到 context。

---

## 超时配置

| 场景 | 常量 | 值 |
|---|---|---|
| LLM 非流式请求 | `LLMRequestTimeout` | 60s |
| LLM 流式（空闲看门狗） | `LLMStreamIdleTimeout` | 30s/token 间隔 |
| 流式 response header | `ResponseHeaderTimeout` | 30s |
| Agent 执行总超时 | `AgentExecTimeout` | 90s |

流式请求**禁止**使用 flat deadline，改用 transport `ResponseHeaderTimeout` + 空闲看门狗组合。
超时常量均定义在 `pkg/constants/timeouts.go`。

---

## Token 计费

Agent 执行结束后，`agent.go` 将 `graph.TotalTokens` 映射到 `Result.TokensUsed`，
由调用方（handler）写入 ledger 或日志，不在 gateway 层计费。

---

## 故障排查

### LLM 请求失败

```bash
# 查看 gateway 日志（事件：llm.complete）
grep "llm.complete" stratum.log | tail -20

# 检查 provider 配置
curl http://localhost:8080/admin/tenants/{tenantID}/settings \
  -H "Authorization: Bearer <admin_token>"
```

### 流式响应截断

检查 `LLMStreamIdleTimeout`（`pkg/constants/timeouts.go`）与 provider 的实际生成速度是否匹配；
增大该值而不是调整 flat deadline。

### Memory 提取 JSON 截断

memory 流水线的 LLM 提取使用 `MemoryExtractLLMMaxTokens = 4096`（`pkg/constants/memory.go`）；
若仍截断，增大该常量并重新部署。LLMExtractor 内置 `recoverTruncatedArray` 降级兜底。
