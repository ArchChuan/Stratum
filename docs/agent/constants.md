# 常量规范（业务/配置数字）

> 魔法数字（timeout / TTL / pageSize / topK / chunkSize / poolSize / retries）**禁止内联**。
> 纯 UI 样式数字（spacing、border-radius 等）不在此范围。

## 后端（Go）

| 作用域 | 存放位置 | 命名 |
|--------|----------|------|
| 跨包共享（超时/TTL/分页/重试/Pool） | `pkg/constants/<domain>.go` | `Default*` / `Max*` / `Min*` / `*Timeout` / `*TTL` |
| 包内共享（≥2 个文件使用） | `internal/<pkg>/defaults.go` | 同上，包级 unexported 即可 |
| 单文件内使用 | 原文件 `const` 块 | 同上 |

规则：

- `pkg/constants/` 禁止 import `internal/`（单向依赖）
- 禁止在函数签名 / 结构体字面量中直接写魔法数字

Agent 上下文相关常量位于 `pkg/constants/agent.go`：

| 常量 | 当前值 | 用途 |
|------|--------|------|
| `DefaultAgentContextTokens` | 8000 | Agent 未设置 `MaxContextTokens` 时的上下文预算 |
| `DefaultInitHistoryWindow` | 20 | `BuildInitMessages` 的兜底历史窗口 |
| `DefaultContextHistoryWindow` | 50 | `BuildContextMessages` 的直接调用兜底窗口 |
| `MemoryBudgetRatio` | 0.3 | memory context 最多占剩余预算的比例 |

## 前端（TypeScript / TSX）

所有行为常量集中在 `web/src/constants/index.ts`，按前缀分组：

```js
// API / 网络
API_DEFAULT_TIMEOUT_MS   AGENT_EXEC_TIMEOUT_MS
// 分页
DEFAULT_PAGE_SIZE   COMPACT_PAGE_SIZE   PAGE_SIZE_OPTIONS
// MCP
MCP_DEFAULT_TIMEOUT_SEC   MCP_MAX_TIMEOUT_SEC
// Skill
SKILL_DEFAULT_TEMPERATURE   SKILL_DEFAULT_MAX_TOKENS   SKILL_DEFAULT_TIMEOUT_SEC
// Evaluation
EVALUATION_JOB_POLL_INTERVAL_MS   EVALUATION_JOB_MAX_WAIT_MS
// Memory
MEMORY_SEARCH_LIMIT
```

规则：

- 所有页面通过 `import { ... } from '../constants'` 引用，禁止页面内直接硬编码上述数字
- 常量名全大写下划线，值加单位后缀（`_MS` / `_SEC` / `_SIZE`）
