# pkg/observability

提供 Zap 日志、Prometheus 指标和 OpenTelemetry trace provider、span 上下文与 HTTP handler。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/observability`

```mermaid
flowchart LR
  pkg["observability 包<br/>pkg/observability"]
  api["核心类型 / 接口 / 函数<br/>NewLogger；MetricsProvider；PrometheusMetrics；NoopMetrics；TraceConfig；InitOTelProvider；Tracer；SpanContext；PrometheusMetrics.GetHandler"]
  subgraph source[非测试源码]
    f0["logger.go"]
    f1["metric.go"]
    f2["prometheus.go"]
    f3["provider.go"]
    f4["trace.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  f4 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["github.com/google/uuid"]
    ex1["github.com/prometheus/client_golang/prometheus"]
    ex2["github.com/prometheus/client_golang/prometheus/promauto"]
    ex3["github.com/prometheus/client_golang/prometheus/promhttp"]
    ex4["go.opentelemetry.io/otel"]
    ex5["go.opentelemetry.io/otel/attribute"]
    ex6["go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"]
    ex7["go.opentelemetry.io/otel/propagation"]
    ex8["go.opentelemetry.io/otel/sdk/resource"]
    ex9["go.opentelemetry.io/otel/sdk/trace"]
    ex10["go.opentelemetry.io/otel/semconv/v1.21.0"]
    ex11["go.uber.org/zap"]
    ex12["go.uber.org/zap/zapcore"]
    ex13["net/http"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
  pkg -->|直接 import| ex3
  pkg -->|直接 import| ex4
  pkg -->|直接 import| ex5
  pkg -->|直接 import| ex6
  pkg -->|直接 import| ex7
  pkg -->|直接 import| ex8
  pkg -->|直接 import| ex9
  pkg -->|直接 import| ex10
  pkg -->|直接 import| ex11
  pkg -->|直接 import| ex12
  pkg -->|直接 import| ex13
  tests["测试汇总<br/>observability_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；Prometheus HTTP handler 由 `(*PrometheusMetrics).GetHandler` 返回，日志、指标与追踪能力分别由其余核心类型组织。当前包没有直接导入其他 stratum 项目包。关键外部依赖为：`github.com/google/uuid`、`github.com/prometheus/client_golang/prometheus`、`github.com/prometheus/client_golang/prometheus/promauto`、`github.com/prometheus/client_golang/prometheus/promhttp`、`go.opentelemetry.io/otel`、`go.opentelemetry.io/otel/attribute`、`go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc`、`go.opentelemetry.io/otel/propagation`、`go.opentelemetry.io/otel/sdk/resource`、`go.opentelemetry.io/otel/sdk/trace`、`go.opentelemetry.io/otel/semconv/v1.21.0`、`go.uber.org/zap`、`go.uber.org/zap/zapcore`、`net/http`。测试文件合并为一个节点：`observability_test.go`。
