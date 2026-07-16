# Agent Development Rules

## Agent Types

| 类型 | 常量 | 适用场景 |
|------|------|---------|
| ReAct | `domain.ReActAgent` | 观察-推理-行动循环（主要生产类型）|
| CoT | `domain.CoTAgent` | 多步推理链 |
| Planning | `domain.PlanningAgent` | 任务分解与规划 |
| Tool Calling | `domain.ToolCallingAgent` | 结构化工具调用 |
| RAG | `domain.RAGAgent` | 检索增强生成 |
| Swarm | `domain.SwarmAgent` | 多智能体协同 |

> `application` 包通过 `type AgentType = domain.AgentType` 以及同名常量保持向后兼容，两种引用方式均可。

## AgentConfig 字段

```go
// 位于 internal/agent/domain/agent.go
type AgentConfig struct {
    ID                             string
    Name                           string
    Type                           AgentType
    Description                    string
    SystemPrompt                   string
    LLMModel                       string            // e.g. "gpt-4o", "claude-3-5-sonnet"
    EmbedModel                     string            // 租户级 embedding 模型
    MaxIterations                  int               // 默认 10，防无限循环
    AllowedSkills                  []string          // skill UUID 列表
    MCPServerIDs                   []string          // MCP 服务器 ID 列表
    Capabilities                   []AgentCapability
    KnowledgeWorkspaceIDs          []string          // RAG workspace UUID 列表
    KnowledgeWorkspaceNames        []string
    KnowledgeWorkspaceDescriptions []string
    MaxContextTokens               int
    MemoryScope                    string
    StuckThreshold                 int               // ReAct 卡死后触发 Plan-Execute 的 LLM-only 轮数阈值（PlanningAgent）
    CheckpointEnabled              bool              // 开启断点持久化（PlanningAgent）
}
```

> `Persona` 字段已不存在于 AgentConfig — 角色设定通过 `SystemPrompt` 表达，不再作为独立字段。

## Creating and Registering an Agent

```go
cfg := &domain.AgentConfig{
    ID:            uuid.NewString(),
    Name:          "My Agent",
    Type:          domain.ReActAgent,
    SystemPrompt:  "你是一个助手...",
    LLMModel:      "gpt-4o",
    MaxIterations: 10,
    AllowedSkills: []string{"skill-uuid-1"},
}
// Registry 由 api/wiring/agent.go 在启动时构建并注入
err := registry.Register(ctx, agent.NewBaseAgent(cfg, logger))
```

> `Registry.Register` 需要 ctx 中携带 TenantContext（由 `middleware.InjectTenantContext()` 注入），写入对应租户 schema 的 `agents` 表。

## Execution Options

```go
result, err := a.Execute(ctx, "用户输入",
    agent.WithMaxSteps(10),
    agent.WithTimeout(30*time.Second),
    agent.WithTemperature(0.7),
    agent.WithStream(true),
    agent.WithTokenCallback(func(token string) { /* 流式输出 */ }),
    agent.WithExtraTools(toolDefs),          // 注入 skill/MCP 工具
    agent.WithSkillToolIndex(skillIndex),    // toolName → skillUUID 映射
    agent.WithHistoryWindow(20),             // 对话历史窗口（条数）
)
```

`AgentResult` 字段：`Output` / `Thoughts` / `ToolCalls` / `Steps` / `TokensUsed` / `Duration` / `Error`

## ReAct Loop & Built-in Tools

ReAct 状态机位于 `internal/agent/application/graph/react.go`，`ReActState` 关键字段：

```go
type ReActState struct {
    TenantID        string
    AvailableTools  []port.ToolDefinition
    SkillToolIndex  map[string]port.SkillToolRef  // toolName → {SkillID, VersionID}
    Messages        []port.LLMMessage
    RAGSearchFn     func(...)
    RecallMemoryFn  func(...)
    StuckThreshold  int
    PlanTriggered   bool
    CheckpointEnabled bool
    // ...
}
```

内置工具（直接在 switch 分支处理，不经过 CapabilityGateway）：

| 工具名 | 触发条件 | 功能 |
|--------|---------|------|
| `stratum_search_knowledge` | `AgentConfig.KnowledgeWorkspaceIDs` 非空 | RAG 知识库检索 |
| `stratum_recall_memory` | `RecallMemoryFn` 已注入 | 长期记忆召回 |
| `stratum_continue_reasoning` | 始终注入 | 占位工具，触发 PlanningAgent 继续推理循环 |

外部 skill 工具：`SkillToolIndex` 以 `toolName`（来自 `tool_contract.toolName`）为键，default 分支通过 `CapabilityGateway.Route` 以 `CapSkill` 类型路由，携带 `SkillID` 和 `VersionID`。旧版本回退路径使用 `tenant_{tenantID}_{skill_name}` 格式（`buildExtraTools` 无 `SkillToolResolver` 时触发）。

## CapabilityGateway 路由

```
CapabilityGateway.Route(req)
  ├── req.Type == CapLLM  → LLMAdapter  → llmgateway.Gateway
  └── req.Type == CapSkill → SkillAdapter → skillgateway.DefaultGateway
                                              └── atomicEngine.execute
                                                    └── DBSkillAdapter (SQL WHERE id=$1)
```

实现位于 `internal/agent/infrastructure/capability/`。

## Memory Integration

Memory 通过三个独立 port 注入 BaseAgent，由 `api/wiring/agent.go` 的 `buildAgent` 完成装配：

```go
// MemoryInjector: 注入对话历史摘要 + 实体 + 长期记忆到 system prompt
type MemoryInjector interface {
    BuildContext(ctx context.Context, ic InjectionContext) (string, error)
}

// RecallMemoryFn: recall_memory 工具的实现函数
type RecallMemoryFn func(ctx context.Context, tenantID, userID, agentID string, input map[string]any) (string, error)

// MemorySearcher: 语义记忆检索（供应用层直接调用）
type MemorySearcher interface {
    Search(ctx context.Context, req *memdomain.MemorySearchRequest) ([]*memdomain.MemorySearchResult, error)
}
```

> 不再直接在 BaseAgent 上挂载 `MemoryManager`。三层记忆策略的选择（ConversationWindow / Summary / Buffer）由 memory 上下文的 `MemoryService` 处理，agent 侧只关心接口。

## Skill Tool Naming Convention

传给模型的工具名默认来自 `tool_contract.toolName`（skill 版本发布时定义的合约名）。

- `buildExtraTools`（`internal/agent/application/agent_service.go`）在注入了 `SkillToolResolver` 且 `AllowedSkills` 非空时，调用 `ResolveTools` 获取每个 skill 已发布版本的 `ToolContract.ToolName` 作为 `ToolDefinition.Name`，同时产出 `SkillToolIndex`（toolName → {SkillID, VersionID}）
- 无 `SkillToolResolver`（或旧配置）时回退到 `tenant_{tenantID}_{skill_name}` 格式
- 执行阶段 `react.go` 从 `SkillToolIndex` 反查 `SkillToolRef`，携带 `SkillID` 和 `VersionID` 通过 `CapabilityGateway.Route`（`CapSkill`）路由到具体 skill 版本
- MCP 工具名由 MCP 服务器自己定义，不应用此前缀

## A2A Protocol

多智能体协作实现于 `internal/agent/application/a2a/`：

| 组件 | 文件 | 作用 |
|------|------|------|
| Protocol | `protocol.go` | 心跳、超时、重试、消息收发、指标统计 |
| Client | `client.go` | Agent 注册、能力公告、发现/协商/协作发起 |
| Discovery | `discovery.go` | Peer 注册表，能力匹配查询 |
| Negotiation | `negotiation.go` | 协作条件协商 |
| Orchestrator | `orchestrator.go` | 创建执行计划，分配步骤 |
| Message | `message.go` | 消息格式，Inbox/Outbox 异步处理 |

五种协作策略：`StrategySequential` / `StrategyParallel` / `StrategyHierarchical` / `StrategyPipeline` / `StrategySwarm`

## Rules

1. Agent ID 全局唯一；重复注册同一 ID 会覆盖原记录（返回 `ErrNameConflict`）
2. `Execute` 内部持有 `sync.Mutex`，同一 Agent 不支持并发执行
3. `MaxIterations` 防无限循环，默认值在 `pkg/constants/agent.go`
4. `Reset()` 清空内存和状态，谨慎使用
5. 不要在 handler 中持有 Agent 实例；每次请求通过 `Registry.Get(ctx, id)` 获取
6. Skill 工具注册后不可与内置工具（`stratum_*`）重名；`mergeTools` 会静默丢弃同名 extra 工具
