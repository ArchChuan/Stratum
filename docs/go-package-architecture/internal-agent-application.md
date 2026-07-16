# internal/agent/application

该包编排 Agent 的注册、配置 CRUD、同步/流式执行、上下文预算、会话与执行追踪持久化，并把领域端口、执行图和可观测能力组合为应用用例。

完整导入路径：`github.com/byteBuilderX/stratum/internal/agent/application`

```mermaid
flowchart LR
  svc["agent_service.go<br/>AgentService / AgentServiceDeps<br/>Create · Update · Execute · ExecuteStream"]
  runtime["agent.go<br/>Agent 接口 · BaseAgent<br/>ExecutionConfig · Execute"]
  registry["registry.go<br/>Registry<br/>hydrate · Register · Get · Update"]
  budget["context_budget.go<br/>BuildContextMessages"]
  ledger["token_ledger.go<br/>TokenLedger · UsageSummary"]
  stores["chat_store.go · execution_store.go<br/>ChatStore / ExecutionStore / ToolTraceStore<br/>TraceEventStore / CheckpointStore 别名"]
  graphPkg["internal/agent/application/graph"]
  domain["internal/agent/domain"]
  ports["internal/agent/domain/port"]
  pkg["pkg/constants · observability · reqctx · tokenutil"]
  ext["OpenTelemetry · zap · google/uuid"]
  tests["测试<br/>export_test.go · agent_service_test.go · react_agent_test.go"]
  svc --> registry
  svc --> runtime
  runtime --> graphPkg
  runtime --> budget
  runtime --> ledger
  runtime --> stores
  svc --> ports
  registry --> ports
  runtime --> ports
  svc --> domain
  runtime --> domain
  ledger --> pkg
  runtime --> pkg
  ledger --> ext
  runtime --> ext
  tests -.覆盖应用编排与 ReAct 执行.-> svc
```

## 说明

`AgentService` 是面向调用方的用例门面，`Registry` 从 `AgentRepo` 装载并水合 `BaseAgent`。`BaseAgent.Execute` 组装 graph 包的 ReAct/Plan-Execute 图，并通过 capability、memory、chat、trace 等消费者侧端口访问外部能力；上下文裁剪与 token 记账分别由独立文件承担。
