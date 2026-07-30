# 统一模型配置收口设计

## Status

Approved by delegated recommendation | 2026-07-29

## Problem

模型管理重构已经引入 tenant schema 中的 `providers`、`models` 和运行时 `ModelRegistry`，但旧的
`public.tenants.settings.llm_api_keys` 仍被租户设置页、IAM service、平台诊断和 E2E 夹具读写。
Agent 创建页和知识库创建页还分别使用硬编码模型常量。因此模型配置存在两个事实源，页面展示、运行时解析和
验收脚本可能对“可用模型”得出不同结论。

## Goals

1. 租户设置只管理租户基本信息，不再展示、接收或返回模型凭据。
2. `providers` 与 `models` 是模型配置唯一事实源，运行时统一通过 `ModelRegistry` 解析。
3. 所有用户模型下拉从认证租户的模型目录读取，并按 `chat` 或 `embedding` 能力过滤。
4. 可用模型必须同时满足模型启用、provider 启用且 provider 协议支持对应能力。
5. 彻底删除所有现存 `settings.llm_api_keys`，并同步更新测试和验收合同。

## Non-Goals

- 不改变 Agent 和知识库保存的模型标识格式。
- 不引入模型自动 fallback、跨 provider 路由或全局共享模型。
- 不迁移旧 JSON 密钥到 provider 表；模型管理已经是正式入口，旧密钥直接清除。

## Architecture

### Single source of truth

```text
模型管理 UI
  -> /admin/providers, /admin/models
  -> tenant providers/models tables
  -> ModelRegistry
  -> /models catalogue + Agent/Knowledge runtime resolution + diagnostics
```

租户设置链路不再依赖 AES key 或模型缓存。`GET /tenant/settings` 会主动过滤遗留的
`llm_api_keys`，以便迁移执行前滚动部署时也不会泄露或继续暴露旧字段；`PATCH /tenant/settings`
拒绝写入该保留字段，防止旧客户端重新创建数据。

### Available model catalogue

保留认证用户可访问的 `GET /models`，返回：

```json
{"models":["chat-model"],"embedding_models":["embed-model"]}
```

前端新增 llm 模块内的共享 catalogue API/hook。Agent 表单使用 `models`，知识库表单使用
`embedding_models`，系统助手继续使用同一 registry-backed 后端目录。空目录显示空下拉，不使用硬编码 fallback。
编辑已有资源时，若当前模型已不可用，保留一个“当前不可用”选项以允许查看和改选，但新建不能默认选择它。

### Provider eligibility

`ModelRegistry` 列目录时逐个校验 provider：provider 必须存在、`Enabled=true`，并且对应协议 map
包含 chat 或 embedding 实现。解析具体模型时应用相同门禁，避免“下拉可选但运行失败”。provider 查询失败向上返回，禁止静默放行。

### Data removal

新增 public migration：

```sql
UPDATE public.tenants
SET settings = COALESCE(settings, '{}'::jsonb) - 'llm_api_keys'
WHERE COALESCE(settings, '{}'::jsonb) ? 'llm_api_keys';
```

该迁移不可逆；down migration 不恢复秘密。应用层在迁移前后均过滤旧字段，并拒绝再次写入。

## Error Handling

- 模型目录加载失败：前端展示不自动消失的错误消息，下拉保持空值。
- 模型目录为空：不提供默认模型，表单必填校验阻止提交。
- 旧客户端提交 `llm_api_keys`：返回 `ErrInvalidSettings` 对应的 400，不静默接受。
- provider/model 查询失败：诊断与运行时返回明确错误，不退回旧租户 settings。

## Verification

- IAM service/API 测试证明旧字段不返回、不能写入，名称和其他 settings 仍可更新。
- migration 顺序测试证明所有 tenant JSONB 中的 key 被移除且其他键保留。
- ModelRegistry 测试证明 disabled provider、缺失 provider 和不支持协议的模型不出现在目录且不能解析。
- React 测试证明 Agent/Knowledge 下拉使用 `/models` 结果，无硬编码默认值，旧模型仅在编辑时标为不可用。
- 更新 stateful E2E、evaluation bootstrap 和远程验证 SQL，使 provider/model 表成为配置证据。
- 运行专项测试、`stratum-verify go-test`、前端 lint/build、risk guardrails、系统 E2E 和 attestation check。
