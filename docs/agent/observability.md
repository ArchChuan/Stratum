# Observability Development Rules

## Tracing (OpenTelemetry)

### Initialization

`cmd/server/main.go` 调用 `internal/platform/runtime.InitTracingFromEnv`。
仅当 `OTEL_EXPORTER_OTLP_ENDPOINT` 非空时初始化 OTLP TracerProvider；
`OTEL_SERVICE_NAME` 可覆盖默认 service name。初始化失败只记录 Warn，并关闭 tracing。

```go
shutdown := platformruntime.InitTracingFromEnv(logger)
if shutdown != nil {
    defer shutdown(ctx)
}
```

### Creating Spans

```go
tracer := observability.NewTracer(logger)
ctx, span := tracer.StartSpan(ctx, "agent.execute")
defer span.End()

span.SetAttributes(attribute.String("agent_id", id))
span.RecordError(err)
span.SetStatus(codes.Error, err.Error())
```

### Rules

- 每个 Handler 方法通过 `middleware/trace.go` (`TraceMiddleware`) 自动获得 Span
- internal 层关键操作手动创建子 Span
- Span 命名格式：`{component}.{operation}`，例如 `agent.execute`、`memory.search`。Skill 当前不直接执行，不应使用 `skill.execute` 暗示存在独立执行路径。

## Metrics (Prometheus)

### 内置指标

**HTTP**（`api/middleware/metrics.go` + `prometheus.go`）

```
http_requests_total{method, path, status}
http_request_duration_seconds{method, path}
http_requests_in_flight
```

**Skill**（collector 已注册，当前生产路径尚未接入记录调用）

```
skill_executions_total{skill_id, skill_type, status}
skill_execution_duration_seconds{skill_id}
skill_circuit_breaker_state{skill_id}           // 0=closed 1=open 2=half_open
```

**Agent**（`pkg/observability/prometheus.go`）

```
agent_executions_total{agent_id, agent_type, status}
agent_execution_duration_seconds{agent_id, agent_type}
agent_step_count{agent_id, agent_type}
```

**LLM**（`internal/llmgateway/`）

```
llm_requests_total{model, provider, status}
llm_request_duration_seconds{model, provider}
llm_token_usage_total{model, type}              // type: prompt|completion
llm_token_count{model, type}
llm_first_token_latency_seconds{model, provider}
```

**Knowledge**（`internal/knowledge/`）

```
knowledge_queries_total{query_type, status}
knowledge_query_duration_seconds{query_type}
knowledge_ingest_total{status}
knowledge_ingest_duration_seconds
knowledge_ingest_in_flight
```

**Memory Pipeline**（`internal/memory/infrastructure/pipeline/metrics.go`）

```
memory_outbox_pending                           // Gauge: 待处理 outbox 条数
memory_outbox_published_total{tenant_id, status}// Counter
memory_embed_duration_seconds                   // Histogram
memory_embed_total{tenant_id, status}           // Counter
memory_enrich_duration_seconds                  // Histogram
memory_enrich_total{tenant_id, status}          // Counter
memory_summary_triggered_total                  // Counter: 触发摘要次数
memory_dlq_total{tenant_id, stage}              // Counter: 进入 DLQ 次数
memory_entities_extracted_total                 // Counter: 实体抽取总数
```

### Circuit Breaker 状态说明

`skill_circuit_breaker_state` 取值：

- `0` = Closed（正常放行）
- `1` = Open（熔断，全部拒绝）
- `2` = HalfOpen（探测恢复中，只放一个请求）

### Adding New Metrics

在 `pkg/observability/prometheus.go` 中添加，遵循命名约定：

- Counter：`{domain}_{action}_total`
- Histogram：`{domain}_{action}_seconds`
- Gauge：`{domain}_{state}`

同时在 `observability.MetricsProvider` 接口中添加对应方法，并在 `PrometheusMetrics` 和 `NoopMetrics` 中实现。

Memory pipeline 专属指标在 `pipeline/metrics.go` 中单独注册（`RegisterMetrics`），不经过 `MetricsProvider` 接口。

## Logging (Zap)

### 初始化

```go
// pkg/observability/logger.go
logger := observability.NewLogger(env) // production → JSON, 其他 → console+color
```

固定字段 `app` / `env` / `host` 在初始化时注入。

### 字段分层

| 层 | 字段 | 注入位置 |
|----|------|---------|
| 链路 | `request_id` `trace_id` `tenant_id` `user_id` | TraceMiddleware per-request |
| LLM | `model` `provider` `prompt_tokens` `completion_tokens` `latency_ms` | `llm.complete` 事件 |
| ReAct | `trace_id` `tenant_id` `model` `step` `tokens` `tool_name` `latency_ms` | `react.llm` / `react.tool` 事件 |
| 访问 | `method` `path` `status` `latency_ms` `client_ip` `ua` | TraceMiddleware after |
| Memory | `tenant_id` `status` `duration` | pipeline workers |

### Usage Standards

```go
logger.Info("operation completed",
    zap.String("agent_id", id),
    zap.Duration("elapsed", d),
    zap.String("request_id", reqID),
)
logger.Error("operation failed",
    zap.Error(err),
    zap.String("skill_id", skillID),
)
```

### 级别规则

| 级别 | 场景 |
|------|------|
| DEBUG | 开发调试，production 不输出 |
| INFO | 正常业务路径（HTTP < 400，LLM 成功，ReAct step，Pipeline 处理成功） |
| WARN | 可预期异常（HTTP 4xx，重试中，连接失败但不阻断启动） |
| ERROR | 需处理异常（HTTP 5xx，外部调用失败，DLQ 溢出）；自动附加 stacktrace |

**安全红线**：禁止记录 `password / token / api_key / PII`；禁止打印原始 HTTP response body

**禁止** 使用 `fmt.Sprintf` 构造日志消息，使用结构化字段。

## Local Access

| 服务 | 地址 | 说明 |
|------|------|------|
| Prometheus | <http://localhost:9090> | 指标查询 |
| Grafana | <http://localhost:3000> | 仪表板（admin/admin）|
| Jaeger UI | <http://localhost:16686> | 链路追踪 |
| Metrics 端点 | <http://localhost:8080/metrics> | Prometheus scrape |

Grafana 数据源：`grafana/datasources/prometheus.yaml`，仪表板：`grafana/dashboards/`。
