# internal/skill/infrastructure/gateway

提供统一 Skill 调用网关、Provider 注册路由、超时重试、熔断、审计指标以及顺序/条件/并行 Pipeline 编排。

- 完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/infrastructure/gateway`

```mermaid
flowchart LR
  pkg["gateway 包<br/>internal/skill/infrastructure/gateway"]
  api["核心类型 / 接口 / 函数<br/>SkillGateway；DefaultGateway；SkillProvider；ProviderRegistry；atomicEngine；CircuitBreakerManager；Pipeline/PipelineBuilder；SkillRequest/SkillResponse；SkillError"]
  subgraph source[非测试源码]
    f0["atomic.go"]
    f1["audit.go"]
    f2["circuit_breaker.go"]
    f3["gateway.go"]
    f4["pipeline.go"]
    f5["pipeline_builder.go"]
    f6["provider.go"]
    f7["types.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  f4 -->|声明或实现| api
  f5 -->|声明或实现| api
  f6 -->|声明或实现| api
  f7 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["pkg/observability"]
  end
  pkg -->|直接 import| pd0
  subgraph external[关键外部依赖]
    ex0["github.com/google/uuid"]
    ex1["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  tests["测试汇总<br/>atomic_test.go, circuit_breaker_test.go, pipeline_test.go, provider_test.go, testhelper_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`pkg/observability`。 关键外部依赖为：`github.com/google/uuid`、`go.uber.org/zap`。 测试文件合并为一个节点：`atomic_test.go`、`circuit_breaker_test.go`、`pipeline_test.go`、`provider_test.go`、`testhelper_test.go`。
