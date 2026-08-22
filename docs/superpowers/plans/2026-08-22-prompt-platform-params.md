# 提示词平台参数化：压缩提示词 / 全局系统提示词 / 内置助手提示词落库

> 日期：2026-08-22
> 范围：三处提示词迁移到平台参数或 DB 存储，移除代码固定值与环境变量兜底，未配置 fail-closed。

## 1. 目标与决策

| # | 决策 | 结论 |
|---|---|---|
| D1 | 压缩配置 | 压缩三值（提示词/温度/模型）全部迁平台级（`agent.compaction_prompt/_temperature/_model`）；删除 `CompactionDefaultPrompt` 代码常量与 per-agent 字段/表单/DTO/落库路径；压缩器内部统一从平台 resolver 读取（唯一来源，所有 agent 含内置助手一致）。prompt 未配置即失败，temperature 0 = 默认常量，model 空 = 网关默认 |
| D2 | 全局系统提示词 | 删除 `GLOBAL_AGENT_SYSTEM_PROMPT` 环境变量读取；新增平台参数 `agent.system_prompt`（追加到所有 agent 的 system prompt，含内置平台助手）；执行时解析，未配置即失败 |
| D3 | 内置平台助手提示词 | 删除 `systemAssistantPrompt`/`systemAssistantPromptV3` 代码常量与 `SystemAssistantProfile.SystemPrompt`；提示词存 `agents.system_prompt` DB 字段（seed + 存量租户幂等回填），`ComposeSystemAssistantProfile` 不再用代码覆盖 |

## 2. 改动面

### 2.1 参数注册表（backend）

- `internal/parameters/domain/registry.go`：
  - `agent.compaction_prompt` 移出 `registerAgentParams`（ScopeResource），新增平台级注册（Category agent、ControlTextarea、Default ""、未配置即失败）。
  - `agent.compaction_temperature`/`agent.compaction_model` 同步由 ScopeResource 迁到平台级注册（temperature 0 = 默认常量，model 空 = 网关默认）。
  - 新增 `agent.system_prompt`（ScopePlatform、ControlTextarea、Default ""、未配置即失败）。
  - `promptEvalKeys` 中的 `compaction_prompt` gate-only 保留（evaluation 兼容）。
- `internal/parameters/application/service.go`：`PromptDefaults()` 返回空 map（无内置模板）。

### 2.2 压缩提示词运行时解析（backend）

- `internal/agent/domain/port`：新增 `PlatformPromptResolver`（`ResolvePlatform(ctx, key) (any, bool, error)`）。
- `internal/agent/application/agent_service.go`：
  - deps 增加 `PlatformPromptResolver`；`resolveCompactionParameters` 删除（压缩三值全部由 compactor 内部解析）。
  - `assembleOptions` 只传 gateway/logger/输出预算给工厂。
  - 删除 per-agent 压缩三值 DTO/参数/校验/落库路径。
- `internal/agent/infrastructure/capability/history_compactor.go`：删除空 prompt 回退常量；构造签名简化为 `(gw, logger, compactionMaxTokens, promptResolver)`，压缩三值全部在 `CompactHistory` 内从平台 resolver 解析。
- `pkg/constants/agent.go`：删除 `CompactionDefaultPrompt`。
- 删除 `AgentConfig.CompactionPrompt/CompactionTemperature/CompactionModel` 与 repo put/unpack、handler DTO 字段。

### 2.3 全局系统提示词（backend）

- `config/config.go`：删除 `GlobalAgentSystemPrompt` 与 env 读取。
- `internal/agent/application/registry.go`：删除 `globalSystemSuffix`/`SetGlobalSystemSuffix`/hydrate 注入；新增 `platformPromptResolver` 注入。
- `internal/agent/application/agent.go`：`BaseAgent` 增加 `GlobalSystemPromptResolver`；`Execute` 对所有 agent 一视同仁执行时解析（错误/空 → fail-closed），并入 snapshot 的 globalSuffix。
- `api/wiring/agent.go`：删除 env 注入；改为注入平台参数 resolver（复用 `platformParamResolver`）。
- helm/`.github/workflows`/docs：删除 `GLOBAL_AGENT_SYSTEM_PROMPT` 引用。

### 2.4 内置助手提示词落 DB（backend）

- `pkg/storage/postgres/tenant_schema.sql`：seed `system_prompt` 写 v3 提示词；新增存量租户回填 `UPDATE ... WHERE system_key='stratum.platform_assistant' AND BTRIM(COALESCE(system_prompt,''))=''`。
- `internal/agent/application/system_assistant_profile.go`：删除两个提示词常量；profile map 去掉 `SystemPrompt`。
- `internal/agent/domain/system_assistant.go`：`SystemAssistantProfile` 去掉 `SystemPrompt` 字段。
- `ComposeSystemAssistantProfile`：保留 DB 的 `cfg.SystemPrompt`，不再代码覆盖。

### 2.5 前端

- `AgentFormSections.tsx`：删除压缩提示词字段与 `PromptDefaultViewer` 引用；再删除压缩温度/模型字段——agent 表单不再暴露任何压缩配置项（与内置助手一致）。
- `useEditAgentPage.ts`、`agent.ts`：删除 `compaction_prompt`/`compaction_temperature`/`compaction_model`。
- `PromptDefaultViewer.tsx` 及其测试：无消费方后删除。
- 平台参数页自动渲染 `agent.compaction_prompt` / `agent.compaction_temperature` / `agent.compaction_model` / `agent.system_prompt`（schema-driven）。

## 3. 验证

- 单测：registry 迁移（压缩三值平台级）、prompt-defaults 空、compaction 三值平台解析（fail-closed/default）、global suffix 解析、系统助手 DB prompt。
- `go vet && go test -short ./...`；`make code-quality`；`make fe-lint && make fe-build`；契约 golden 无 diff。
- E2E 前提：`scripts/e2e/system-stateful.sh` 在 prepare_database 阶段预置
  `agent.system_prompt` / `agent.compaction_prompt`（fail-closed 前提下测试环境
  与生产一致，未配置即 agent 执行/压缩失败）。
- 系统验收走 `stratum-e2e-development` skill：压缩提示词未配置 → 压缩失败可观测；配置后生效；全局提示词注入 agent；系统助手提示词来自 DB。
