# Agent Development Rules

## Agent Types

| 类型 | 常量 | 适用场景 |
|------|------|---------|
| ReAct | `agent.ReActAgent` | 观察-推理-行动循环 |
| CoT | `agent.CoTAgent` | 多步推理链 |
| Planning | `agent.PlanningAgent` | 任务分解与规划 |
| Tool Calling | `agent.ToolCallingAgent` | 结构化工具调用 |
| RAG | `agent.RAGAgent` | 检索增强生成 |
| Swarm | `agent.SwarmAgent` | 多智能体协同 |

## Creating an Agent

```go
config := &agent.AgentConfig{
    ID:            "unique-id",       // 全局唯一
    Name:          "Display Name",
    Type:          agent.ReActAgent,
    Description:   "Description",
    Persona:       "角色设定",
    SystemPrompt:  "系统提示词",
    LLMModel:      "gpt-4",
    MaxIterations: 10,                // 防止无限循环，默认 10
}
a := agent.NewBaseAgent(config, logger)
// 持久化到当前租户 PostgreSQL schema
registry.Register(ctx, a)
```

> `Registry.Register` 需要 `ctx` 中携带 `TenantContext`，写入对应租户 schema 的 `agents` 表。

## Execution Options

```go
result, err := a.Execute(ctx, "用户输入",
    agent.WithMaxSteps(10),
    agent.WithMemory(true),
    agent.WithTemperature(0.7),
    agent.WithTimeout(30*time.Second),
)
```

`AgentResult` 字段：`Output` / `Thoughts` / `ToolCalls` / `Steps` / `TokensUsed` / `Duration` / `Error`

## Memory Integration

通过 `MemoryManager` 注入，执行时自动检索相关记忆并写入结果：

```go
memManager := memory.NewMemoryManager(config, logger, vectorMemory, entityMemory, persistence, pool)
// BaseAgent 直接赋值字段
baseAgent.MemoryManager = memManager
baseAgent.SessionContext = &memory.SessionContext{SessionID: sessionID}
```

三层记忆策略由 `MemoryConfig` 控制：

- `ShortTermWindowSize > 0` → `ConversationWindowMemory`
- `EnableSummary = true` → `ConversationSummaryMemory`
- 否则 → `ConversationBufferMemory`

## A2A Protocol

多智能体协作实现于 `internal/agent/a2a/`，组件说明：

| 组件 | 文件 | 作用 |
|------|------|------|
| Protocol | `protocol.go` | 心跳、超时、重试等协议配置，消息收发与指标统计 |
| Client | `client.go` | Agent 注册、能力公告、发现/协商/协作发起 |
| Discovery | `discovery.go` | Peer 注册表，能力匹配查询 |
| Negotiation | `negotiation.go` | 协作条件协商 |
| Orchestrator | `orchestrator.go` | 创建执行计划，分配步骤 |
| Message | `message.go` | 消息格式，Inbox/Outbox 异步处理 |

**支持的 5 种协作策略：**

| 策略 | 常量 | 说明 |
|------|------|------|
| 顺序 | `StrategySequential` | 参与者依次执行 |
| 并行 | `StrategyParallel` | 参与者同时执行 |
| 层级 | `StrategyHierarchical` | 主控 Agent 指挥子 Agent |
| 流水线 | `StrategyPipeline` | 上一步输出作为下一步输入 |
| 群体 | `StrategySwarm` | 去中心化协同 |

```go
orch := a2a.NewOrchestrator(logger)
plan, err := orch.CreatePlan(ctx,
    collaborationID,
    "任务描述",
    a2a.StrategyParallel,
    participants,  // []AgentIdentity
)
```

## Rules

1. Agent ID 必须全局唯一；重复注册同一 ID 会覆盖原有记录
2. `Execute` 内部持有锁，同一 Agent 不支持并发执行
3. `MaxIterations` 防止无限循环，默认 10
4. `Reset()` 会清空内存和状态，谨慎使用
5. 不要在 handler 中直接存储 Agent 实例；通过 Registry 按 ID 查询
