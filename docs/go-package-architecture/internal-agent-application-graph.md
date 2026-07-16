# internal/agent/application/graph

该包实现通用状态图运行器，以及 Agent 的 ReAct 和 Plan-Execute 两套确定性执行图、节点重试与 checkpoint 写入逻辑。

完整导入路径：`github.com/byteBuilderX/stratum/internal/agent/application/graph`

```mermaid
flowchart LR
  core["graph.go<br/>StateGraph / CompiledGraph<br/>NodeFunc · EdgeFunc · Invoke"]
  react["react.go<br/>ReActState · TokenRecorder<br/>BuildReActGraph · LLM/Tool 节点"]
  plan["plan_execute.go<br/>PlanCheckpointWriter<br/>BuildPlanExecuteGraph · wave 编排"]
  retry["retry.go<br/>RetryConfig · RetryFn"]
  domain["internal/agent/domain<br/>PlanStep · StepResult · trace/checkpoint 模型"]
  ports["internal/agent/domain/port<br/>CapabilityGateway · LLMMessage · tools"]
  constants["pkg/constants"]
  ext["OpenTelemetry · zap"]
  tests["测试<br/>export_test.go · graph_test.go · plan_execute_test.go<br/>react_test.go · retry_test.go"]
  react --> core
  plan --> core
  react --> retry
  plan --> retry
  react --> ports
  plan --> ports
  react --> domain
  plan --> domain
  react --> constants
  plan --> constants
  react --> ext
  plan --> ext
  tests -.覆盖图编译、执行、重试与两类 Agent 图.-> core
```

## 说明

`StateGraph` 保存命名节点、普通边和条件边，`Compile` 后由 `CompiledGraph.Invoke` 驱动状态前进。ReAct 图在 LLM 与工具节点间循环；Plan-Execute 图生成计划、按依赖波次执行步骤、反思并综合结果，同时可通过 `PlanCheckpointWriter` 落 checkpoint。
