# Token-Aware Context Truncation Design

**日期**: 2026-06-14
**项目**: stratum
**标签**: #agent #context #truncation #memory

## 背景

当前 `BuildInitMessages` 按消息条数（`HistoryWindow=20`）截断历史，无法处理：

- 单条消息内容超长（贴了大段代码、工具返回大段文本）
- Memory injector 注入的 summary/entities 过长
- 多来源叠加导致 messages 总 token 超出模型上限

解决方案：引入 per-agent 可配置的 token 预算，按优先级对四个上下文槽位做截断。

## 范围定义

`max_context_tokens` 限制的是**注入 LLM 的 `messages` 数组总估算 token 数**，包含：

- `messages[0]`：system（systemPromptBase + memoryCtx）
- `messages[1..]`：历史对话轮次（user/assistant）
- `messages[-1]`：本轮 currentInput（user）

**不包含**：`tools[]` 字段（skill/MCP schema），以及 LLM 的输出 token。

## 数据模型变更

### DB Schema

```sql
-- internal/migration/sql/009_agent_context_tokens.up.sql
ALTER TABLE agents ADD COLUMN max_context_tokens INTEGER NOT NULL DEFAULT 8000;

-- internal/migration/sql/009_agent_context_tokens.down.sql
ALTER TABLE agents DROP COLUMN max_context_tokens;
```

默认值 8000：覆盖主流模型（GPT-4/Claude/Qwen），在输入和输出之间留足余量。

### Go 结构体

```go
// internal/agent/agent.go — AgentConfig
type AgentConfig struct {
    // ... 现有字段 ...
    MaxContextTokens int // per-agent token budget for messages array; 0 → default 8000
}
```

### API DTO

```go
// api/handler/agent_handler.go
type CreateAgentRequest struct {
    // ... 现有字段 ...
    MaxContextTokens int `json:"maxContextTokens"`
}

type AgentResponse struct {
    // ... 现有字段 ...
    MaxContextTokens int `json:"maxContextTokens"`
}
```

## 核心截断逻辑

### Token 估算

```go
// internal/agent/context_budget.go
func estimateTokens(s string) int {
    return len([]rune(s)) / 3 // CJK 混合场景保守估算，误差 ±20%
}
```

不引入 tiktoken 等外部依赖（跨 provider 不通用）。

### BuildContextMessages

替换现有 `BuildInitMessages`，签名：

```go
func BuildContextMessages(
    systemPromptBase string,  // agent 自身系统提示
    memoryCtx        string,  // MemoryInjector.BuildContext 输出
    history          []*ChatMessage,
    currentInput     string,
    maxTokens        int,     // AgentConfig.MaxContextTokens；≤0 使用默认值 8000
    historyWindow    int,     // 消息条数上限（二级守卫）；≤0 使用默认值 50
) []capgateway.LLMMessage
```

截断顺序（优先级从高到低）：

1. **currentInput**：几乎不会触发，但若超出整体预算则截尾 + `[truncated]`
2. **systemPromptBase**：保留至少 200 token 的最低保证，超出预算截尾 + `[truncated]`
3. **memoryCtx**：用剩余预算填充，不足时截尾，可以降为 0
4. **history**：先按 `historyWindow` 条数截断（丢弃最旧），再按剩余 token 从最旧整条丢弃；最后如果最旧一条仍超出预算则截尾

处理流程：

```
budget = maxTokens
budget -= estimateTokens(currentInput)   // 保留 currentInput
budget -= estimateTokens(systemPromptBase) // 保留 systemPromptBase（含最低保证逻辑）
memoryCtx = truncateTo(memoryCtx, budget*0.3) // memoryCtx 最多占剩余 30%
budget -= estimateTokens(memoryCtx)
history = trimHistory(history, budget, historyWindow)
```

合并 system 内容：`systemFull = memoryCtx + "\n" + systemPromptBase`（memoryCtx 为空时不加换行）

### agent.go 调用侧变更

```go
// 删除原有合并行
// systemPrompt = memCtx + "\n" + systemPrompt  ← 删除

maxTokens := a.AgentConfig.MaxContextTokens
if maxTokens <= 0 {
    maxTokens = constants.DefaultAgentContextTokens // 8000
}
initMessages := BuildContextMessages(systemPrompt, memCtx, history, input, maxTokens, cfg.HistoryWindow)
// input 已在 BuildContextMessages 内部作为最后一条 user message 追加，无需再 append
```

## 缓存策略

`AgentRegistry` 已将 agent 对象缓存在内存中（`sync.Map`）。agent 创建/更新时 registry 同步更新内存对象，`Execute` 时直接读取 `a.GetConfig().MaxContextTokens`，无需额外缓存层。

## 前端变更

`CreateAgentPage.jsx` 和 `EditAgentPage.jsx` 跟随 `maxIterations` 模式：

```jsx
// 表单字段
<Form.Item label="上下文 Token 预算" name="maxContextTokens" rules={[{ required: true }]}>
  <InputNumber min={1000} max={128000} step={1000} style={{ width: '100%' }} />
</Form.Item>

// initialValues
{ maxIterations: 5, maxContextTokens: 8000, allowedSkills: [] }

// EditAgentPage setFieldsValue
maxContextTokens: a.maxContextTokens,
```

## 常量

```go
// pkg/constants/agent.go（或现有文件）
const (
    DefaultAgentContextTokens = 8000
    MinSystemPromptTokens     = 200  // systemPromptBase 最低保证
)
```

## 变更文件清单

| 文件 | 变更类型 |
|------|----------|
| `internal/migration/sql/009_agent_context_tokens.up.sql` | 新建 |
| `internal/migration/sql/009_agent_context_tokens.down.sql` | 新建 |
| `pkg/tenantdb/tenant_schema.sql` | 加字段 |
| `internal/agent/agent.go` | `AgentConfig` 加字段，`Execute` 调用改用新函数 |
| `internal/agent/context_budget.go` | 新建，`BuildContextMessages` + `estimateTokens` |
| `internal/agent/registry.go` | INSERT/SELECT/UPDATE 加字段 |
| `api/handler/agent_handler.go` | DTO + AgentConfig 构造加字段 |
| `pkg/constants/agent.go`（或现有） | 加两个常量 |
| `web/src/pages/CreateAgentPage.jsx` | 加表单字段 |
| `web/src/pages/EditAgentPage.jsx` | 加表单字段 |

## 测试要点

- `BuildContextMessages`：表驱动测试，覆盖各槽位超限、全部超限、全部为空等边界用例
- `estimateTokens`：纯函数，验证 CJK/英文/混合字符串的估算结果
- 回归：现有 34 个 agent 包测试需全部通过（`-race`）
