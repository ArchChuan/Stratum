# 快速开始：调用大模型

Stratum 的 LLM 调用通过 tenant-scoped LLM Gateway 完成。当前运行时 provider 是 Qwen 和 Zhipu，都使用 OpenAI-compatible 接口适配。

## 1. 启动服务

```bash
make infra-up
make infra-wait
make run
```

前端可通过 `make fe-dev` 在 3002 端口启动。

## 2. 配置租户模型

1. 登录并选择 tenant。
2. 在租户设置中配置 provider API key 和默认模型。
3. 必要时设置 embedding model，用于 Knowledge / Memory。

API key 应使用平台的加密配置。不要把真实 key 放入文档、前端 `.env` 或版本库。

```text
GET   /models
GET   /tenant/settings
PATCH /tenant/settings
PATCH /tenant/embed-model
```

`/models` 无需认证；tenant settings 路由需要 JWT 和 tenant context，写入操作还要求 active tenant。

## 3. 创建并执行 Agent

Agent 创建/修改/删除需要 tenant admin；普通 member 可查看和执行。

```text
POST /agents
POST /agents/:id/execute
POST /agents/:id/execute/stream
```

流式接口使用 SSE，前端通过 `fetch` + `ReadableStream` 解析 token，不走 axios。请求需要 bearer JWT、tenant context 和 active tenant。

## 4. 使用 Skill 能力

当前 Skill 是版本化 capability package，不再通过旧的 `/skills/:id/execute` 路由执行 `type: llm` Skill。

- 草稿测试：`POST /skills/test-draft`
- 已保存 Skill 测试：`POST /skills/:id/test`
- 发布：`POST /skills/:id/publish`
- Agent 通过 `AllowedSkills` 引用已发布版本的 tool contract

## 5. 超时与观测

- Agent 外层执行超时、ReAct LLM/tool 子操作超时由 `pkg/constants` 集中管理。
- 流式 LLM 使用响应头、stream idle 和外层执行超时的组合策略。
- `/metrics` 暴露 LLM 请求、耗时和 token 指标。
- 日志不得记录 API key 或原始响应体。

更多架构与租户缓存说明见 [LLM_INTEGRATION.md](LLM_INTEGRATION.md)。
