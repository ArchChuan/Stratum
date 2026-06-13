# Observability Development Rules

## Tracing (OpenTelemetry)

### Initialization

TracerProvider 在 `cmd/server/main.go` 初始化，通过 context 传播。

```go
cfg := &observability.TraceConfig{
    ServiceName:    "stratum-ai",
    ServiceVersion: "1.0.0",
    Environment:    "production",
    ExporterType:   "otlp",         // otlp | jaeger | stdout | log | none
    SamplingRatio:  1.0,
    OTLPEndpoint:   "localhost:4317",
}
tp, err := observability.InitTracer(cfg, logger)
defer tp.Shutdown(ctx)
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

- 每个 Handler 方法通过 `middleware/trace.go` 自动获得 Span
- internal 层关键操作手动创建子 Span
- Span 命名格式：`{component}.{operation}`，例如 `agent.execute`、`memory.search`、`skill.execute`

## Metrics (Prometheus)

### 完整内置指标

```
# HTTP
http_requests_total{method, path, status}
http_request_duration_seconds{method, path}

# Skill
skill_executions_total{skill_id, status}        // status: success|error|timeout|circuit_open
skill_execution_duration_seconds{skill_id}
skill_circuit_breaker_state{skill_id}           // 0=closed 1=open 2=half_open

# Agent
agent_executions_total{agent_id, type, status}
agent_execution_duration_seconds{agent_id}

# LLM
llm_requests_total{model, provider, status}
llm_request_duration_seconds{model, provider}
llm_token_usage_total{model, token_type}        // token_type: prompt|completion
llm_token_histogram{model, token_type}

# Knowledge
knowledge_queries_total{type, status}

# Hermes
hermes_events_total{type, status}
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

## Logging (Zap)

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

### Rules

- 禁止用 `fmt.Sprintf` 构造日志消息，使用结构化字段
- ERROR 级别：需要人工介入
- WARN 级别：可自动恢复但需关注（如熔断器状态变更、外部依赖连接失败）
- INFO 级别：关键业务事件（组件启停、Agent 注册/执行、Skill 熔断）
- DEBUG 级别：开发调试信息

### 必填上下文字段（尽量携带）

`request_id` / `user_id` / `tenant_id` / `operation` / `timestamp`

禁止记录：密码 / Token / PII / API Key

## Local Access

| 服务 | 地址 | 说明 |
|------|------|------|
| Prometheus | <http://localhost:9090> | 指标查询 |
| Grafana | <http://localhost:3000> | 仪表板（admin/admin）|
| Jaeger UI | <http://localhost:16686> | 链路追踪 |
| Metrics 端点 | <http://localhost:8080/metrics> | Prometheus scrape |

Grafana 数据源配置见 `grafana/datasources/prometheus.yaml`，仪表板配置见 `grafana/dashboards/`。
