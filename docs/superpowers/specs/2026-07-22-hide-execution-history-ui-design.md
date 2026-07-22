# 隐藏执行历史前端入口设计

## 目标

由于执行历史接口当前不可用，暂时从前端隐藏所有用户可见的执行历史入口，避免用户进入空白/错误页面或看到失败提示；后端 API、数据模型和可复用组件暂不删除。

## 当前入口

- Agent 菜单在 `web/src/app/layout/menu.config.tsx` 注册 `/history` 与“执行历史”。
- Agent 路由在 `web/src/modules/agent/routes.tsx` 注册 `/history` 并渲染 `ExecutionHistoryPage`。
- Dashboard 在 `web/src/modules/dashboard/pages/DashboardPage.tsx` 展示“近期执行”统计卡片和 `RecentExecutionsTable`。
- Dashboard Hook 在 `web/src/modules/dashboard/hooks/useDashboardPage.ts` 调用 `agentApi.executions` 获取近期执行数据。

## 设计

### Agent 导航与路由

删除菜单中的 `/history` 项，并删除 Agent 路由中的 `/history` 路由注册。保留 `ExecutionHistoryPage`、`ExecutionHistoryTable`、`useExecutionHistory` 和 `agentApi.executions`，不影响后续恢复功能。

直接访问 `/history` 时由现有应用兜底路由处理，不新增专门的重定向或错误页面，避免为临时隐藏增加新的产品行为。

### Dashboard

删除 Dashboard 的“近期执行”统计卡片和 `RecentExecutionsTable` 区块，同时从 `useDashboardPage` 删除 `agentApi.executions` 请求、执行历史状态和相关计数。Dashboard 保留其他统计和数据请求，布局在无执行历史项时保持稳定。

### 测试

- 更新菜单/路由测试，确认 `/history` 不再出现在用户导航和 Agent 路由配置中。
- 更新 Dashboard Hook/Page 测试，确认不调用执行历史 API、不渲染“近期执行”或执行历史表格，同时其他 Dashboard 数据仍正常展示。
- 保留执行历史组件自身测试，因为组件和 API 没有删除。

## 非目标

- 不删除后端执行历史 API、数据库表、前端 API client、Hook 或执行历史组件文件。
- 不修改执行历史接口的错误处理或后端实现。
- 不新增“功能暂不可用”提示、重定向页面或权限规则。

## 验收标准

1. 登录后的侧边栏不显示“执行历史”。
2. Agent 路由配置不再注册 `/history`。
3. Dashboard 不显示“近期执行”统计卡片或执行历史表格。
4. Dashboard 不发起执行历史 API 请求。
5. 其他 Dashboard 内容与已有导航不受影响。
6. 前端相关单测、lint、build 通过；真实浏览器访问 Dashboard 和 Agent 导航确认两个入口均不可见。
