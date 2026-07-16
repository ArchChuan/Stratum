# internal/agent/infrastructure/capability

该包实现 Agent capability 端口的路由网关，并分别适配 LLM gateway 与技能执行网关。

完整导入路径：`github.com/byteBuilderX/stratum/internal/agent/infrastructure/capability`

```mermaid
flowchart LR
  gateway["gateway.go<br/>DefaultCapabilityGateway<br/>按 CapabilityType 路由"]
  llm["llm_adapter.go<br/>LLMCompleter · LLMAdapter<br/>请求/响应与 tool call 转换"]
  skill["skill_adapter.go<br/>skillGateway · SkillAdapter"]
  ports["internal/agent/domain/port"]
  llmdomain["internal/llmgateway/domain"]
  constants["pkg/constants"]
  ext["zap"]
  tests["测试<br/>gateway_test.go · llm_adapter_test.go · skill_adapter_test.go"]
  gateway --> llm
  gateway --> skill
  gateway -.实现.-> ports
  llm -.实现 Adapter.-> ports
  skill -.实现 Adapter.-> ports
  llm --> llmdomain
  llm --> constants
  gateway --> ext
  tests -.覆盖路由与两类适配转换.-> gateway
```

## 说明

`DefaultCapabilityGateway.Route` 根据明确的 capability 类型选择注入的 adapter。`LLMAdapter` 把 Agent 端口模型转换为 llmgateway 领域请求并还原响应；`SkillAdapter` 把技能请求委托给其最小 `skillGateway` 接口。
