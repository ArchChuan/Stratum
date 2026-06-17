# Stratum Web — 架构指南

> 适用版本: React 18 · TypeScript · Vite 4 · Ant Design 5 · React Router 6 · TanStack Query · Zod

本指南是 `web/` 前端的架构契约。新增功能、调整目录、提交 PR 前必须读完。

---

## 顶层结构

```
web/
├── src/
│   ├── app/              # 应用骨架（不放业务）
│   │   ├── App.tsx          # 根组件
│   │   ├── providers.tsx    # ConfigProvider · QueryClient · BrowserRouter · AuthProvider
│   │   ├── router.tsx       # 顶层路由组装：聚合各模块 routes
│   │   └── layout/          # AppShell · UserMenu · menu.config
│   ├── modules/          # 业务模块（按领域切分，强隔离）
│   │   ├── iam/             # 认证 + 租户（横切关注点）
│   │   ├── agent/           # Agent + 对话 SSE（编排层，可依赖其他业务模块）
│   │   ├── skill/
│   │   ├── mcp/
│   │   ├── knowledge/
│   │   └── memory/
│   ├── shared/           # 跨模块复用（无业务语义）
│   │   ├── lib/             # extractErrorMessage · http(client) · setupApiInterceptors
│   │   └── ui/              # 通用 UI 组件（如 DangerPopconfirm）
│   ├── services/
│   │   └── client.ts        # 单例 axios + 401 refresh + 403 message 拦截器
│   ├── constants/        # 行为常量（超时/分页/默认值）
│   ├── test/setup.ts     # vitest 全局 setup（jsdom + testing-library）
│   ├── main.tsx          # 应用入口
│   └── index.css
├── ARCHITECTURE.md       # 本文件
├── .eslintrc.cjs         # 含模块隔离 + 旧路径禁用规则
├── tsconfig.json         # 严格模式 + path alias `@/*`
├── vite.config.ts
└── vitest.config.ts
```

---

## 模块内结构（统一契约）

每个 `src/modules/<domain>/` 必须遵循下列分层，自上而下单向依赖：

```
modules/<domain>/
├── model/        # Zod schema + TS 类型；纯定义，无副作用
├── api/          # 调用 axios client；只做请求 + 解构 + schema 校验
├── hooks/        # 业务 hook：组合 api + 状态 + UI 事件回调
├── components/   # 模块内复用组件（含 Provider）
├── pages/        # 路由页面组件（≤200 行；超出抽到 hooks/）
├── routes.tsx    # 该模块路由表（public + private 分组）
└── index.ts      # 模块对外契约：仅导出 routes / 跨模块共用 API / Provider / 类型
```

### 分层规则（ESLint 强制）

| 层 | 允许依赖 | 禁止依赖 |
|----|---------|---------|
| `model/` | zod | `api/`、`hooks/`、`components/`、`pages/` |
| `api/` | `@/services/client`、`./model` | `hooks/`、`components/`、`pages/` |
| `hooks/` | `./api`、`./model`、antd | `pages/` |
| `components/` | `./hooks`、`./api`、`./model` | `pages/` |
| `pages/` | 同模块所有层 + `@/modules/iam`（认证） | 其他业务模块 |

### 模块间依赖（ESLint 强制）

```
agent  ────►  skill / mcp / knowledge / memory   ✓ 允许（编排层）
iam    ────►  ✗ 禁止依赖任何业务模块
其他   ────►  ✗ 禁止互相依赖（封闭模块）
所有   ────►  iam（认证横切关注点） ✓ 允许
```

> 实现：`.eslintrc.cjs` 中 `import/no-restricted-paths`。违规直接 fail lint。

---

## 数据流

### 请求 → 响应

```
Page → useXxxPage() ──► xxxApi.method() ──► @/services/client ──► HTTP
                                              │
                                              ├─ 401 → /auth/refresh → 重试原请求
                                              └─ 403 → message.error(...)
```

- **唯一 axios 实例**: `src/services/client.ts`，`withCredentials: true`，`baseURL = VITE_API_BASE_URL`
- **Token 存储**: `MutableRefObject<string | null>` 存内存 + `httpOnly cookie`，**禁止 localStorage**
- **拦截器装配**: `AppShell` 在挂载时调 `setupApiInterceptors(tokenRef, onLogout)`，把 ref 与登出回调注入 client
- **401 dedup**: 模块级 `isRefreshing` + `pendingQueue` 保证并发请求只触发一次 refresh
- **schema 校验**: `api/*.api.ts` 的响应必须经过 `model/` 中 zod schema 解析（`.passthrough()` 容忍后端冗余字段）

### 认证

- `AuthProvider`（`src/modules/iam/components/AuthContext.tsx`）：组件挂载时调 `/auth/refresh` 恢复 session，并行加载 `me` + `tenant settings` + `tenant list`
- 模块级 `refreshPromise` 防止 React StrictMode 双触发
- `useAuth()` 暴露：`user · accessToken · tokenRef · tenants · login · logout · switchTenant`
- `PrivateRoute`（`src/modules/iam/components/PrivateRoute.tsx`）：路由级守卫，未登录跳 `/login`

---

## 路由组装

`src/app/router.tsx` 不直接写 `<Route>` 字面量；改为聚合各模块导出的路由数组：

```tsx
import { iamPublicRoutes, iamPrivateRoutes } from '@/modules/iam';
import { agentRoutes } from '@/modules/agent';
// ...
```

新增页面：在模块的 `routes.tsx` 添加，再在顶层路由按 `public` / `private` 分组合并。

---

## 命名约定

| 对象 | 形式 |
|------|------|
| 页面组件文件 | `XxxPage.tsx` |
| 业务 hook 文件 | `useXxxPage.ts` 或 `useXxx.ts` |
| API wrapper 文件 | `xxx.api.ts` |
| Zod schema | `xxxSchema`，对应类型 `Xxx` |
| Modal 状态 | `createOpen / editOpen` |
| Loading 状态 | `createLoading / deleteLoading` |
| 服务层方法 | 动词 + 实体名（`createWorkspace`） |

---

## 状态管理

- **服务端状态**: TanStack Query（`@tanstack/react-query`）
- **会话状态**: AuthContext（仅认证；不放业务数据）
- **页面状态**: 优先 `useState` + 自定义 hook；超过单页面共享时考虑提取 Context
- **流式状态**: `ChatStreamProvider`（agent 模块）封装 SSE

> **不引入 Redux / Zustand / Jotai**。如有特殊需求，先在 PR 描述说明并讨论。

---

## 错误处理

```tsx
import { extractErrorMessage } from '@/shared/lib';

try {
  await xxxApi.create(payload);
  message.success('已保存');
} catch (err) {
  message.error(extractErrorMessage(err, '保存失败'));
}
```

- 统一用 `extractErrorMessage(err, fallback)` 提取 `error → message → err.message → fallback`
- 403 在 axios 拦截器里全局 `message.error(...)`，调用方不重复提示
- 401 自动 refresh + 重试，调用方无感

---

## 常量与配置

- 行为常量集中在 `src/constants/index.ts`（命名 `*_MS / _SEC / _SIZE`）
- 环境变量 `import.meta.env.VITE_*`，类型在 `src/vite-env.d.ts` 声明
- **禁止**：在 `.env` 提交密钥；任何 token 写入 `localStorage`；`alert()` / `confirm()`

---

## 测试

- 框架：vitest + @testing-library/react + jsdom
- 用例位置：邻接 `__tests__/` 子目录（同层）
- 覆盖目标：
  - `model/`：schema 边界值（必填、可选默认、passthrough、null/数字字符串切换）
  - `shared/lib/`：纯函数全分支
  - `api/`：mock axios 实例，断言 URL/方法/payload，回放典型 success/error
- 当前覆盖：
  - `src/shared/lib/__tests__/errorMessage.test.ts`
  - `src/modules/iam/model/__tests__/auth.test.ts`
- 运行：`npm test` / `npm run test -- --watch`

---

## 开发约定

- 用户可见字符串全部中文
- **禁止** `console.log` 提交（lint 规则强制；`console.warn / .error` 允许）
- 跨页面复用逻辑：抽 hook，不在 `pages/` 互相 import
- `useEffect` 异步 effect 加 `let cancelled = false` 清理
- 二次确认弹窗用 `Modal.confirm` / `DangerPopconfirm`，禁用原生 `alert / confirm`
- Token 不入 localStorage —— 用 `httpOnly cookie` + 内存 ref

---

## 命令速查

```bash
npm run dev          # vite dev server
npm run build        # 生产构建
npm run typecheck    # tsc --noEmit
npm run lint         # eslint --max-warnings 0
npm test             # vitest run
```

CI 必须全绿才合并。

---

## 添加新模块的清单

1. `src/modules/<new>/` 建目录与 6 个标准子层
2. 在 `model/` 写 zod schema + 类型
3. 在 `api/` 写 wrapper：`import api from '@/services/client'`
4. 在 `hooks/` 抽业务编排
5. 在 `pages/` 写视图（≤200 行）
6. 在 `routes.tsx` 暴露路由数组
7. 在 `index.ts` 仅导出对外契约（routes、跨模块需要的 API/类型/Provider）
8. 在 `src/app/router.tsx` 聚合
9. 写测试（model 边界、纯函数、关键 hook）
10. 更新本文件（如引入新跨模块模式）

---

## 红线总结

| 红线 | 自动检测 |
|------|---------|
| `from '@/contexts/*'` / `'@/hooks/*'` / `'@/pages/*'` / `'@/components/*'` / `'@/utils/*'` | ✓ ESLint |
| `from '@/services/api'` 等旧 barrel | ✓ ESLint |
| 跨业务模块互相 import（除 agent → 其他、所有 → iam） | ✓ ESLint |
| `console.log` 提交 | ✓ ESLint |
| `model/` 反向依赖 `api/` | ✓ ESLint |
| 漏 schema 校验 / 漏中文 / 漏 fallback message | 人工 review |
