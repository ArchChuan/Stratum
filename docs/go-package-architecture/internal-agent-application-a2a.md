# internal/agent/application/a2a

该包提供内存态的 Agent-to-Agent 协作协议，包括消息传输、能力发现、任务协商、协作会话和多种执行计划编排策略。

完整导入路径：`github.com/byteBuilderX/stratum/internal/agent/application/a2a`

```mermaid
flowchart LR
  protocol["protocol.go<br/>A2AProtocol · ProtocolConfig<br/>Start · Stop · CreateClient"]
  client["client.go<br/>A2AClient · CollaborationSession<br/>ProposeTask · Broadcast · RegisterHandler"]
  handler["handler.go<br/>ProtocolHandler<br/>收件箱/发件箱与消息分派"]
  message["message.go<br/>Message · MessageType<br/>AgentIdentity · Capability"]
  discovery["discovery.go<br/>DiscoveryService · PeerInfo<br/>注册 · 订阅 · 选择 peer"]
  negotiation["negotiation.go<br/>NegotiationService<br/>TaskOffer · Negotiation"]
  orchestration["orchestrator.go<br/>Orchestrator · ExecutionPlan<br/>顺序/并行/层级/流水线/群体策略"]
  errors["error.go<br/>A2AError · ErrorType"]
  ext["zap · google/uuid"]
  tests["测试<br/>当前包无测试文件"]
  protocol --> client
  protocol --> handler
  protocol --> discovery
  protocol --> negotiation
  protocol --> orchestration
  client --> message
  client --> discovery
  client --> negotiation
  client --> orchestration
  handler --> message
  discovery --> message
  negotiation --> message
  orchestration --> message
  client --> errors
  protocol --> ext
  client --> ext
  tests -.测试汇总.-> protocol
```

## 说明

`A2AProtocol` 管理客户端、发现、协商、编排和协议处理器的生命周期。`A2AClient` 是主要操作入口，构造 `Message` 并调用各服务；`ProtocolHandler` 负责异步收发和按消息类型分派。该包没有项目内直接依赖，状态保存在进程内并用锁保护。
