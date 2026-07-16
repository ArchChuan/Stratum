# internal/llmgateway/infrastructure

该包实现统一 LLM 网关、OpenAI 兼容 HTTP 客户端、Qwen/Zhipu 构造、静态模型目录与租户网关 TTL 缓存。

完整导入路径：`github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure`

```mermaid
flowchart TB
  config["config.go<br/>包声明（无类型或函数）"]
  gateway["gateway.go<br/>ModelProvider / Gateway / LLMClient / StreamingLLMClient<br/>RegisterClient·Complete·CompleteStream·CreateEmbeddings"]
  compat["openai_compat.go<br/>OpenAICompatClient<br/>HTTP completion、stream、embedding"]
  qwen["qwen.go<br/>QwenClient 构造与默认端点"]
  zhipu["zhipu.go<br/>ZhipuClient 构造与默认端点"]
  catalog["static_catalog.go<br/>StaticModelCatalog"]
  cache["tenant_cache.go<br/>TenantGatewayCache<br/>Get·Set·Invalidate（TTL 存储）"]
  domain["internal/llmgateway/domain"]
  constants["pkg/constants"]
  obs["pkg/observability"]
  reqctx["pkg/reqctx"]
  otel["外部：OpenTelemetry<br/>otel / attribute / trace"]
  zap["外部：zap"]
  http["外部 LLM OpenAI-compatible API"]
  tests["测试汇总<br/>gateway、qwen、zhipu、tenant_cache 测试"]
  qwen --> compat
  zhipu --> compat
  compat -.实现.-> gateway
  gateway --> domain
  compat --> constants
  compat --> obs
  cache --> gateway
  gateway --> reqctx
  gateway --> otel
  gateway --> zap
  compat --> zap
  compat --> http
  pkg -.-> tests
```

`ModelProvider` 定义在 `gateway.go`；`Gateway` 按提供商注册客户端并统一执行 `Complete`、`CompleteStream` 与 `CreateEmbeddings`，从 `reqctx` 读取追踪/租户信息，并用 OpenTelemetry 创建 span、记录 attribute/trace 信息。`Gateway` 与 `OpenAICompatClient` 都使用 zap 记录请求、响应和错误。Qwen/Zhipu 提供默认端点构造；`TenantGatewayCache` 仅用 `Get`、`Set`、`Invalidate` 保存带 TTL 的 Gateway 与 API key 副本，不负责读取设置或创建网关。
