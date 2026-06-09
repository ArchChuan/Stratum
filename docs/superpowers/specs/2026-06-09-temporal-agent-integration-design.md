# Temporal + ClawHermes Agent Integration Design

**日期**: 2026-06-09
**范围**: ReAct Agent Phase 1 — Temporal Workflow 编排 + 统一能力门面
**分支**: 基于 `feat/tenant-suspend-enforcement`

---

## 1. 背景与目标

### 现状

`internal/agent/agent.go` 中 `BaseAgent.Execute()` 是空壳：ReAct 分支仅累积假 Thought，不调用 LLM，不执行工具。六种 AgentType 全部为 stub。

`internal/llmgateway/` 无 tool calling 支持：`CompletionRequest` 无 `Tools` 字段，`CompletionResponse` 无 `ToolCalls` 字段，Qwen/ZhiPu 客户端只解析 `choices[0].message.content`。

### 目标

以最小侵入量，在现有架构上叠加四层：

```
接入层 → 编排层(Temporal) → 统一能力门面(CapabilityGateway) → LLM/Skill/MCP 适配器
```

Phase 1 交付：可运行的 ReAct Agent，支持 native function calling，工具来自全部 Skill 类型（LLM Skill、MCP Skill、Code Skill）。

---

## 2. 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│  接入层   api/handler/agent_handler.go                          │
│  POST /agents/:id/execute  →  AgentService.Execute()           │
└─────────────────────────┬───────────────────────────────────────┘
                           │
┌─────────────────────────▼───────────────────────────────────────┐
│  编排层   internal/agent/workflow/                              │
│  ReActWorkflow(input, agentCfg, tenantID)                       │
│  · 持久化循环，Temporal 保证 crash-safe replay                  │
│  · 每步调用 ExecuteCapabilityActivity(CapabilityRequest)        │
└─────────────────────────┬───────────────────────────────────────┘
                           │ 单一 Activity 入口
┌─────────────────────────▼───────────────────────────────────────┐
│  统一能力门面   internal/capgateway/                            │
│  CapabilityGateway.Route(ctx, CapabilityRequest)                │
│  中间件栈: TenantScope → CircuitBreaker → Audit → Route        │
└──────┬──────────────────┬──────────────────┬─────────────────────┘
       │                  │                  │
┌──────▼──────┐  ┌────────▼───────┐  ┌──────▼────────────────┐
│ LLM Adapter │  │ Skill Adapter  │  │ MCP Adapter           │
│ llmgateway  │  │ skillgateway   │  │ mcp/skill_adapter     │
│ .Complete() │  │ .Execute()     │  │ → skillgateway        │
│ (扩展 tool  │  │ (零修改)       │  │ (已接入，零修改)      │
│  calling)   │  │                │  │                       │
└─────────────┘  └────────────────┘  └───────────────────────┘
```

### 层职责

| 层 | 包路径 | 职责 | 改动量 |
|---|---|---|---|
| 接入层 | `api/handler/` | 解析请求，调 AgentService | 零修改 |
| 编排层 | `internal/agent/workflow/` | ReAct 循环、历史管理、停止条件 | **新增** |
| 能力门面 | `internal/capgateway/` | 统一路由 + 中间件 | **新增** |
| LLM Adapter | `internal/llmgateway/` | chat + tool calling | **扩展** |
| Skill Adapter | `internal/skillgateway/` | 原子执行、熔断、审计 | 零修改 |
| MCP Adapter | `internal/mcp/skill_adapter.go` | MCP→Skill 桥接 | 零修改 |

---

## 3. 统一能力门面协议

### CapabilityRequest / CapabilityResponse

```go
// internal/capgateway/types.go

type CapabilityType string

const (
    CapLLM   CapabilityType = "llm"
    CapSkill CapabilityType = "skill"  // 含 MCP（skillID 前缀 "mcp:"）
)

type CapabilityRequest struct {
    TraceID  string
    TenantID string
    Type     CapabilityType

    // Type == "llm" 时填充
    LLM *LLMCapRequest

    // Type == "skill" 或 "mcp" 时填充
    Skill *SkillCapRequest

    Timeout  time.Duration
}

type LLMCapRequest struct {
    Model       string
    Messages    []LLMMessage       // 完整对话历史
    Tools       []ToolDefinition   // 供 LLM 选择的工具（来自 CapabilityGateway.ListTools）
    Temperature float32
    MaxTokens   int
}

type SkillCapRequest struct {
    SkillID string  // "mcp:<serverID>:<toolName>" 或普通 skillID
    Input   any
}

type CapabilityResponse struct {
    TraceID  string
    Type     CapabilityType
    Duration time.Duration

    // LLM 响应
    Content   string       // 最终文本（无 tool_calls 时）
    ToolCalls []ToolCall   // LLM 要求调用的工具列表
    Usage     TokenUsage

    // Skill 响应
    Output any
}

type ToolDefinition struct {
    Name        string
    Description string
    InputSchema map[string]any  // JSON Schema object
}

type ToolCall struct {
    ID       string  // LLM 返回的 call ID，回传时用于 tool role message
    Name     string
    Arguments map[string]any
}

type LLMMessage struct {
    Role       string  // system / user / assistant / tool
    Content    string
    ToolCallID string  // tool role 时填充
    ToolCalls  []ToolCall  // assistant 返回 tool_calls 时填充
}

type TokenUsage struct {
    Prompt     int
    Completion int
    Total      int
}
```

### 路由规则

```
req.Type == CapLLM   → LLMAdapter.Complete(ctx, req.LLM)
req.Type == CapSkill → skillgateway.Execute(ctx, SkillRequest{
                            SkillID: req.Skill.SkillID,
                            Input:   req.Skill.Input,
                        })
```

MCP 工具已通过 `MCPSkillProvider` 注册进 `skillgateway`，skillID 前缀 `mcp:` 自动路由到 MCP Adapter，门面层无需感知差异。

### 工具列表发现

`CapabilityGateway.ListTools(ctx, tenantID) []ToolDefinition`

从 `orchestrator`（Skill 注册表，持久化于 PostgreSQL tenant schema）查询当前 agent 可用的所有 Skill，转换为 `ToolDefinition`。LLM Adapter 在每次 LLM 调用前获取此列表并注入请求。

---

## 4. 编排层：ReAct Workflow

### Temporal 组件划分

| 组件 | 类型 | 说明 |
|---|---|---|
| `ReActWorkflow` | Workflow | 确定性循环，replay-safe |
| `ExecuteCapabilityActivity` | Activity | 单一能力调用（LLM 或 Skill），所有副作用在此 |

所有 LLM 调用和工具调用均为 Activity，保证 crash 后从最后成功步骤恢复。

### Workflow 伪代码

```go
// internal/agent/workflow/react_workflow.go

func ReActWorkflow(ctx workflow.Context, req ReActRequest) (*ReActResult, error) {
    messages := buildInitialMessages(req.AgentCfg, req.Input)
    var allToolCalls []ToolCall

    for i := 0; i < req.AgentCfg.MaxIterations; i++ {
        // Step 1: LLM 调用
        llmResp, err := executeActivity[CapabilityResponse](ctx, ExecuteCapabilityActivity,
            CapabilityRequest{
                Type:     CapLLM,
                TenantID: req.TenantID,
                LLM: &LLMCapRequest{
                    Model:    req.AgentCfg.LLMModel,
                    Messages: messages,
                    Tools:    req.AvailableTools,
                },
            })
        if err != nil {
            return nil, err
        }

        // Step 2: 无 tool_calls → 最终回答
        if len(llmResp.ToolCalls) == 0 {
            return &ReActResult{Output: llmResp.Content, ToolCalls: allToolCalls}, nil
        }

        // Step 3: 执行工具，收集结果
        messages = append(messages, LLMMessage{
            Role: "assistant", ToolCalls: llmResp.ToolCalls,
        })

        for _, tc := range llmResp.ToolCalls {
            toolResp, err := executeActivity[CapabilityResponse](ctx, ExecuteCapabilityActivity,
                CapabilityRequest{
                    Type:     CapSkill,
                    TenantID: req.TenantID,
                    Skill:    &SkillCapRequest{SkillID: tc.Name, Input: tc.Arguments},
                })
            result := formatToolResult(tc, toolResp, err)
            messages = append(messages, result)
            allToolCalls = append(allToolCalls, tc)
        }
    }

    return nil, fmt.Errorf("max iterations reached: %d", req.AgentCfg.MaxIterations)
}
```

### Activity 实现

```go
// internal/agent/workflow/activities.go

// Activities 通过闭包捕获依赖（Temporal Go SDK 标准注入方式）
type ActivityDeps struct {
    CapGateway capgateway.CapabilityGateway
}

func (d *ActivityDeps) ExecuteCapabilityActivity(ctx context.Context, req CapabilityRequest) (CapabilityResponse, error) {
    return d.CapGateway.Route(ctx, req)
}
```

Activity 超时配置：

- LLM 调用：60s（与现有 `qwen.go`/`zhipu.go` HTTP 超时一致）
- 工具调用：30s（与 `skillgateway` 默认一致）
- 重试：最多 3 次，指数退避 base 100ms（瞬态错误）

---

## 5. LLMGateway 扩展

### 需新增字段

```go
// internal/llmgateway/gateway.go

type CompletionRequest struct {
    Model       string    `json:"model"`
    Messages    []Message `json:"messages"`
    Temperature float32   `json:"temperature,omitempty"`
    MaxTokens   int       `json:"max_tokens,omitempty"`
    TopP        float32   `json:"top_p,omitempty"`
    // 新增
    Tools      []Tool `json:"tools,omitempty"`
    ToolChoice string `json:"tool_choice,omitempty"` // "auto" | "none" | "required"
}

type Message struct {
    Role       string     `json:"role"`
    Content    string     `json:"content,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`  // assistant role
    ToolCallID string     `json:"tool_call_id,omitempty"` // tool role
}

type Tool struct {
    Type     string       `json:"type"` // "function"
    Function ToolFunction `json:"function"`
}

type ToolFunction struct {
    Name        string         `json:"name"`
    Description string         `json:"description"`
    Parameters  map[string]any `json:"parameters"` // JSON Schema
}

type CompletionResponse struct {
    Content   string     `json:"content"`
    Model     string     `json:"model"`
    ToolCalls []ToolCall `json:"tool_calls,omitempty"` // 新增
    Usage     struct {
        PromptTokens     int `json:"prompt_tokens"`
        CompletionTokens int `json:"completion_tokens"`
        TotalTokens      int `json:"total_tokens"`
    } `json:"usage"`
}

type ToolCall struct {
    ID       string `json:"id"`
    Type     string `json:"type"` // "function"
    Function struct {
        Name      string `json:"name"`
        Arguments string `json:"arguments"` // JSON string，需调用方 unmarshal
    } `json:"function"`
}
```

### Provider 适配（Qwen / ZhiPu）

两者均遵循 OpenAI function calling 协议，序列化/反序列化对称扩展：

- 请求：`tools` 数组 → 原样序列化至 HTTP body
- 响应：`choices[0].message.tool_calls` → 反序列化填入 `CompletionResponse.ToolCalls`
- 当 `finish_reason == "tool_calls"` 时，`Content` 为空，以 `ToolCalls` 为准

---

## 6. Temporal 部署策略

### 本地开发（Docker Compose）

```yaml
# docker-compose.yml 新增
temporal:
  image: temporalio/auto-setup:1.24
  ports:
    - "7233:7233"
  environment:
    - DB=postgres12
    - DB_PORT=5432
    - POSTGRES_USER=temporal
    - POSTGRES_PWD=temporal
    - POSTGRES_SEEDS=postgres

temporal-ui:
  image: temporalio/ui:2.26
  ports:
    - "8088:8080"
  environment:
    - TEMPORAL_ADDRESS=temporal:7233
```

Worker 随应用启动：`cmd/server/main.go` 的 Harness 注册 `TemporalWorkerComponent`，顺序启动，逆序停止。

### K8s / 生产

- Temporal Server 独立 Helm Chart（`temporal/temporal-helm-charts`），与应用 Chart 分离部署
- Worker 作为 ClawHermes Deployment 的一部分运行（同进程），无需独立 Pod
- Namespace 隔离：`temporal-system` vs `clawhermes`
- CI/CD：Temporal Server 版本固定于 `values.yaml`，通过 GitOps 管理

### 配置

```yaml
# config/config.yaml 新增
temporal:
  host_port: "localhost:7233"  # dev; prod 用 k8s service DNS
  namespace: "clawhermes"
  task_queue: "agent-react"
  worker_max_concurrent_activities: 20
  worker_max_concurrent_workflows: 100
```

---

## 7. 新增目录结构

```
internal/
  agent/
    workflow/
      react_workflow.go     # ReActWorkflow 定义
      activities.go         # ExecuteCapabilityActivity
      worker.go             # Temporal Worker 注册
      types.go              # ReActRequest / ReActResult
  capgateway/
    gateway.go              # CapabilityGateway 接口 + DefaultCapabilityGateway
    types.go                # CapabilityRequest / CapabilityResponse / ToolDefinition
    llm_adapter.go          # LLM Adapter → llmgateway
    skill_adapter.go        # Skill Adapter → skillgateway（含 MCP）
    middleware.go           # TenantScope / CircuitBreaker passthrough / Audit
```

---

## 8. 不在本次范围内

- CoT / Planning / RAG / Swarm agent 类型
- Streaming 响应
- Agent 间 A2A 协作（已有 `internal/agent/a2a/`，不修改）
- Memory 深度集成（`MemoryManager` 接口已存在，Phase 2 接入）
- 多 Worker 横向扩展（Phase 1 单进程）

---

## 9. 成功标准

1. `POST /agents/:id/execute` 能完成至少一次 tool_call 的 ReAct 循环并返回最终答案
2. Temporal UI 可见 Workflow 执行历史和每个 Activity 的输入/输出
3. 进程 crash 重启后，未完成的 Workflow 自动恢复
4. `go test -race ./internal/capgateway/... ./internal/agent/workflow/... ./internal/llmgateway/...` 全绿，覆盖率 ≥80%
5. `go vet ./...` 无错误
