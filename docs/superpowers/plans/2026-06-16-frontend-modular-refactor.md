# Stratum 前端 Modular Monolith + 轻量 DDD 重构

## Context

### 现状问题

`web/src/` 5495 行 JS/JSX，50 个源文件，6 个业务域：

| 问题 | 证据 |
|---|---|
| `components/` 几乎空 | 唯一文件 `PrivateRoute.jsx`（40 行），跨页面零复用 |
| `Popconfirm` / `Skeleton` / `Empty` 模式重复 | 8 / 8 / 5 个页面各自手写一份 |
| `App.jsx` 356 行 | 路由表 + 顶部导航 + 用户菜单 + 创建租户 Modal + 路径高亮全混 |
| hook 绑死 page | `useAgentsListPage` 而非 `useAgentList`，无法跨页面复用 |
| 部分 page 直接调 service | `MCPServersPage` / `SkillsListPage` / `DashboardPage` / `ExecutionHistoryPage` 绕过 hook 抽象 |
| `services/index.js` barrel 打平 namespace | 全局名 `getAllAgents()` 而非 `agentApi.list()`，丢失边界 |
| 零 TypeScript / 零类型边界 | 后端 Go 强类型，前端响应字段拼错运行时才报错 |
| 零测试 | 无 `*.test.*` / `vitest` 配置 |

### 目标

对齐后端 `internal/<context>/` 结构，前端按 6 个 module 切分，每个 module 内 5 个标准子目录。借鉴 Hexagonal 的"分层 + 单向依赖"思想，但不强制 ports/adapters 仪式感。**原则：架构清晰但不过度工程**。

### 不目标

- 不重写业务逻辑，仅搬代码 + 抽提 + 加类型
- 不换 UI 库（保留 Ant Design 5）
- 不换路由库（保留 React Router 6）
- 不引入 micro-frontend / monorepo
- 不动后端

---

## 目标架构

```
web/src/
├─ app/                       # 应用骨架
│  ├─ App.tsx                 # ≤30 行，仅 Provider 嵌套 + Router
│  ├─ router.tsx              # 集中路由表
│  ├─ providers.tsx           # AuthProvider / ChatStreamProvider / QueryClient
│  └─ layout/
│     ├─ AppShell.tsx         # Sider + Header（从 App.jsx 拆出）
│     ├─ UserMenu.tsx         # 头像下拉 + 切租户/创建租户
│     └─ menu.config.ts       # 侧边栏数据
├─ shared/
│  ├─ ui/                     # 跨域 UI 原子（解决重复）
│  │  ├─ DangerPopconfirm.tsx
│  │  ├─ ListSkeleton.tsx
│  │  ├─ EmptyHint.tsx
│  │  ├─ ResourceListPage.tsx # 列表+创建按钮+空态+骨架 模板
│  │  └─ FormPage.tsx         # 创建/编辑表单 模板
│  ├─ lib/
│  │  ├─ http.ts              # axios 实例 + 拦截器（替代 services/client.js）
│  │  ├─ errorMessage.ts      # 统一 error → message.error
│  │  └─ formatters.ts        # 时间 / 字节 / 数字
│  └─ hooks/
│     ├─ useDebounce.ts
│     └─ usePagination.ts
└─ modules/
   ├─ agent/
   │  ├─ api/                 # 网关层（Hexagonal 思想：infrastructure adapter）
   │  │  ├─ agent.api.ts      # CRUD + execute
   │  │  └─ dto.ts            # 后端 DTO 类型
   │  ├─ model/               # 业务实体（Hexagonal 思想：domain）
   │  │  └─ agent.ts          # type Agent + zod schema + 常量
   │  ├─ hooks/               # 应用层（Hexagonal 思想：application）
   │  │  ├─ useAgentList.ts
   │  │  ├─ useAgent.ts
   │  │  └─ useAgentMutations.ts
   │  ├─ components/          # 域内复用 UI
   │  │  ├─ AgentCard.tsx
   │  │  └─ AgentExecDrawer.tsx
   │  ├─ pages/               # 表现层（Hexagonal 思想：presentation）
   │  │  ├─ AgentsListPage.tsx
   │  │  ├─ CreateAgentPage.tsx
   │  │  ├─ EditAgentPage.tsx
   │  │  └─ AgentChatPage.tsx
   │  ├─ routes.ts            # 模块路由 config
   │  └─ index.ts             # public API: pages + routes
   ├─ skill/                  # ...同形（含 ExecutionHistoryPage）
   ├─ mcp/                    # ...
   ├─ knowledge/              # ...
   ├─ memory/                 # ...
   └─ iam/                    # auth + tenant + admin 合并
      ├─ api/
      ├─ model/
      ├─ hooks/
      ├─ components/
      └─ pages/
         ├─ auth/             # LoginPage, CallbackPage, OnboardingPage
         ├─ tenant/           # MembersPage, SettingsPage
         ├─ admin/            # TenantsListPage
         └─ DashboardPage.tsx
```

### 后端 ↔ 前端 module 对照

| 后端 `internal/<x>/` | 前端 `modules/<x>/` | 备注 |
|---|---|---|
| `agent/` | `agent/` | 含 chat |
| `skill/` + `skillgateway/` | `skill/` | 含 execution history |
| `mcp/` | `mcp/` | |
| `knowledge/` | `knowledge/` | |
| `memory/` | `memory/` | |
| `auth/` + `iam-like` + `admin/` | `iam/` | tenant + admin 一起 |
| `llmgateway/` | — | 前端不需要镜像 |

### 分层规则（强制）

| 层 | 可以 import | 禁止 import |
|---|---|---|
| `modules/<x>/api/` | `shared/lib/`、`<x>/model/` | 其他 module、antd、react |
| `modules/<x>/model/` | 仅 zod / dayjs 等纯库 | 任何 react/antd/axios |
| `modules/<x>/hooks/` | `<x>/api/`、`<x>/model/`、`shared/`、TanStack Query | 其他 module、antd UI 组件 |
| `modules/<x>/components/` | `<x>/model/`、`<x>/hooks/`、`shared/ui/`、antd | 其他 module |
| `modules/<x>/pages/` | 同上 + `app/` 类型 | 其他 module 的 pages/components |
| `modules/<x>/routes.ts` | `<x>/pages/` | — |
| `app/router.tsx` | 各 module 的 `routes.ts` | 各 module 的 pages 直接路径 |
| `shared/*` | 框架级库 | 任何 module |

跨模块协作（如 agent 列表里展示绑定的 skill 数）：通过后端聚合接口，**不**直接 import 跨模块。

---

## 技术选型

| 决策 | 选项 | 理由 |
|---|---|---|
| 语言 | TypeScript（`allowJs: true` 渐进） | Hexagonal 的边界靠类型守住；后端是强类型 |
| TS 严格度 | 阶段 1：`strict: false` + `noImplicitAny: false`；阶段 5：开 `strict: true` | 一次性 strict 会卡住迁移 |
| 服务端状态 | TanStack Query v5 | 删除现有 useEffect+useState 取数模式；自动缓存/重试/失效 |
| 客户端状态 | Zustand v4 | Auth / ChatStream Context 平迁过来；TS 友好；无 Provider |
| Schema 校验 | zod v3 | DTO ↔ model 转换 + 表单校验 |
| 表单 | 保留 antd `Form` | 不引入 react-hook-form，避免双套表单系统 |
| 路由 | 保留 react-router 6 | 改成 data router + module 提供 routes config |
| 测试 | vitest + @testing-library/react + msw | 阶段 1 立即引入 |
| Lint 边界 | `eslint-plugin-import` `no-restricted-paths` | 编译期约束分层规则 |

---

## 文件改动模式（不全列）

### 模式 1：service → module/api

```
services/agents.js  →  modules/agent/api/agent.api.ts
services/skills.js  →  modules/skill/api/skill.api.ts
services/mcp.js     →  modules/mcp/api/mcp.api.ts
services/knowledge.js → modules/knowledge/api/knowledge.api.ts
services/memory.js  →  modules/memory/api/memory.api.ts
services/auth.js    →  modules/iam/api/auth.api.ts
services/tenant.js  →  modules/iam/api/tenant.api.ts
services/conversations.js → modules/agent/api/conversation.api.ts
services/client.js  →  shared/lib/http.ts
services/api.js     →  删除（barrel）
services/index.js   →  删除（barrel）
```

每个 `*.api.ts` 模板：

```ts
import { http } from '@/shared/lib/http';
import type { Agent, AgentDTO } from '../model/agent';
import { agentSchema } from '../model/agent';

export const agentApi = {
  list: () => http.get<{ agents: AgentDTO[] }>('/agents')
    .then(r => r.data.agents.map(d => agentSchema.parse(d))),
  get: (id: string) => http.get<{ agent: AgentDTO }>(`/agents/${id}`)
    .then(r => agentSchema.parse(r.data.agent)),
  create: (input: CreateAgentInput) => http.post<{ agent: AgentDTO }>('/agents', input)
    .then(r => agentSchema.parse(r.data.agent)),
  update: (id: string, patch: UpdateAgentInput) => http.put(`/agents/${id}`, patch),
  delete: (id: string) => http.delete(`/agents/${id}`),
  execute: (id: string, input: ExecInput) => http.post(`/agents/${id}/execute`, input, {
    timeout: AGENT_EXEC_TIMEOUT_MS,
  }),
};
```

### 模式 2：page-bound hook → domain hook

```
hooks/useAgentsListPage.js → modules/agent/hooks/useAgentList.ts
                            + modules/agent/hooks/useAgentMutations.ts
hooks/useChatPage.js       → modules/agent/hooks/useChat.ts
hooks/useCreateAgentPage.js → 合并到 useAgentMutations.ts
hooks/useEditAgentPage.js  → 合并到 useAgentMutations.ts + useAgent.ts
... 其他 9 个 hook 同样按域归类
```

每个 hook 模板（用 React Query）：

```ts
export const agentKeys = {
  all: ['agents'] as const,
  list: () => [...agentKeys.all, 'list'] as const,
  detail: (id: string) => [...agentKeys.all, 'detail', id] as const,
};

export function useAgentList() {
  return useQuery({
    queryKey: agentKeys.list(),
    queryFn: () => agentApi.list(),
  });
}

export function useAgentMutations() {
  const qc = useQueryClient();
  const remove = useMutation({
    mutationFn: agentApi.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: agentKeys.all }),
  });
  return { remove, /* create, update, execute */ };
}
```

### 模式 3：page 内联 UI → shared/ui 抽提

| 重复模式 | 出现处（行号在原文件） | 抽到 |
|---|---|---|
| `Popconfirm` 删除按钮 | 8 个页面 | `shared/ui/DangerPopconfirm.tsx` |
| `Skeleton` 列表骨架 | 8 个页面 | `shared/ui/ListSkeleton.tsx` |
| `Empty` 空态 | 5 个页面 | `shared/ui/EmptyHint.tsx` |
| 列表+创建+搜索 | AgentsList / SkillsList / KnowledgePage / MCPServersPage | `shared/ui/ResourceListPage.tsx` |
| 创建/编辑表单页骨架 | CreateAgent / EditAgent / CreateSkill / EditSkill / CreateMCP | `shared/ui/FormPage.tsx` |

### 模式 4：App.jsx 拆解

| 来源 | 去处 |
|---|---|
| 路由表 (Routes/Route) | `app/router.tsx` |
| Provider 嵌套 | `app/providers.tsx` |
| Layout (Sider+Header) | `app/layout/AppShell.tsx` |
| 用户头像下拉+切租户+创建租户 Modal | `app/layout/UserMenu.tsx` |
| 侧边栏菜单数据 | `app/layout/menu.config.ts` |
| 路径高亮逻辑 | `app/layout/AppShell.tsx` 内一个小 hook |

### 模式 5：Context → Zustand store

```
contexts/AuthContext.jsx → modules/iam/model/authStore.ts
contexts/ChatStreamContext.jsx → modules/agent/model/chatStreamStore.ts
```

`useAuth()` / `useChatStream()` API 形状保持不变（向后兼容）。

---

## 阶段计划

每阶段独立 PR，独立可发布，main 始终绿。

### Stage 0：基础设施（1 天）

**目标**：TS + vitest + ESLint 边界 + TanStack Query + Zustand 装好，零业务改动。

**改动**：

- 添加 `tsconfig.json` + `tsconfig.node.json`，`allowJs: true` `strict: false`
- `vite.config.js` → `vite.config.ts`
- `package.json` 加依赖：`typescript`、`@tanstack/react-query`、`@tanstack/react-query-devtools`、`zustand`、`zod`、`vitest`、`@testing-library/react`、`@testing-library/jest-dom`、`msw`、`jsdom`
- `package.json` 加 script：`"test": "vitest"`、`"typecheck": "tsc --noEmit"`
- `eslint-plugin-import` + `no-restricted-paths` 规则（先警告，stage 5 改报错）
- `main.jsx` 包一层 `QueryClientProvider`
- `vitest.config.ts` 写好

**验证**：

- `npm run build` 通过
- `npm run typecheck` 通过（allowJs 模式）
- `npm test` 跑空套件通过
- 启动 dev server，所有页面正常

**回滚**：单独 revert 这个 PR，业务零影响。

---

### Stage 1：shared/ 落地 + App.jsx 拆解（1 天）

**目标**：把 `shared/ui/` `shared/lib/` `shared/hooks/` 建起来；App.jsx 拆成 4 个文件。

**改动**：

- `shared/lib/http.ts` ← `services/client.js`
- `shared/lib/errorMessage.ts` 新增（统一 `err.response?.data?.error || '操作失败'` 模板）
- `shared/ui/{DangerPopconfirm,ListSkeleton,EmptyHint,ResourceListPage,FormPage}.tsx` 新增
- 这 5 个组件每个写 vitest 单测
- App.jsx 拆为 `app/{App,router,providers}.tsx` + `app/layout/{AppShell,UserMenu,menu.config}.tsx`
- main.jsx 改入口

**不动**：所有 `pages/` `hooks/` `services/` 保持原样。

**验证**：

- 跑 dev server 手测：登录、切租户、创建租户、所有侧边栏路由能跳转、路径高亮正常
- 单元测试通过
- TypeCheck 通过

**回滚**：revert PR，恢复 App.jsx。

---

### Stage 2：iam module pilot（2 天）

**目标**：用 `iam` 跑通完整模板，验证可行性。**iam 选作 pilot 是因为它路由独立、跨模块依赖最少**。

**改动**：

- 创建 `modules/iam/` 目录树
- `services/{auth,tenant}.js` → `modules/iam/api/{auth,tenant}.api.ts`，加 zod schema 和返回类型
- `contexts/AuthContext.jsx` → `modules/iam/model/authStore.ts`（Zustand）
- `hooks/useAuth.js` → `modules/iam/hooks/useAuth.ts`
- `pages/auth/{Login,Callback,Onboarding}Page.jsx` → `modules/iam/pages/auth/`
- `pages/tenant/{Members,Settings}Page.jsx` → `modules/iam/pages/tenant/`
- `pages/admin/TenantsListPage.jsx` → `modules/iam/pages/admin/`
- `pages/DashboardPage.jsx` → `modules/iam/pages/DashboardPage.tsx`（这是登录后第一屏，归 iam）
- `modules/iam/routes.ts`：导出 `<Route>` 数组
- `app/router.tsx`：从 module 拼装 routes
- `components/PrivateRoute.jsx` → `modules/iam/components/PrivateRoute.tsx`

**测试**：

- `authStore` 单测（login/logout/refreshTenant/setUser）
- `auth.api.ts` 用 msw 测一个 happy path
- E2E 手测：登录 → callback → 进 dashboard → 切租户 → 邀请成员 → 退出

**验证**：

- 全部 iam 路由可达
- TypeCheck 严格度局部开 `"strict": true`（仅 `modules/iam/**`），证明类型边界站得住

**回滚**：revert PR；`pages/auth/` `pages/tenant/` `pages/admin/` 自动回到原位。

---

### Stage 3：剩余 5 module 迁移（5-7 天，可分多个 PR）

按依赖少→多 顺序，**每个 module 一个独立 PR**：

#### 3.1 memory module（1 天）

`pages/MemoryPage.jsx` + `hooks/useMemoryPage.js` + `services/memory.js` → `modules/memory/`

- API 4 个：list / get / search / delete
- hook: `useMemoryList`、`useMemorySearch`、`useMemoryMutations`
- page: `MemoryPage.tsx`

#### 3.2 mcp module（1 天）

`pages/{MCPServersPage,CreateMCPPage}.jsx` + `hooks/{useMCPServersPage,useCreateMCPPage}.js` + `services/mcp.js` → `modules/mcp/`

- 关键：CreateMCPPage 是当前最大页面（264 行），用 `FormPage` 模板抽形

#### 3.3 knowledge module（1 天）

`pages/{KnowledgePage,KnowledgeDetailPage}.jsx` + `hooks/{useKnowledgePage,useKnowledgeDetailPage}.js` + `services/knowledge.js` → `modules/knowledge/`

#### 3.4 skill module（1-2 天）

`pages/{SkillsListPage,CreateSkillPage,EditSkillPage,ExecutionHistoryPage}.jsx` + 对应 hooks + `services/skills.js` → `modules/skill/`

- ExecutionHistoryPage 归 skill 还是 agent？归 skill，因为 execution 是 skill 维度。如果产品上属于 agent，再调整。

#### 3.5 agent module（2-3 天，最复杂）

`pages/{AgentsListPage,CreateAgentPage,EditAgentPage,AgentChatPage}.jsx` + 对应 hooks + `services/{agents,conversations}.js` + `contexts/ChatStreamContext.jsx` → `modules/agent/`

- ChatStream Context → Zustand store，重点保 SSE 流式渲染语义
- `useChatPage`（220 行最大 hook）拆为 `useAgentChat` + `useChatStream`（store-driven）
- `AgentChatPage`（222 行）改用 `useAgentChat`，page 应该 ≤120 行

每个 PR 验证：

- 该 module 的页面所有交互手测通过
- 单元测试覆盖 api（msw mock 后端）+ hook（renderHook + queryClient）+ store（直接调）
- `npm run build && npm run typecheck && npm test` 全绿

---

### Stage 4：清理旧代码 + 锁紧规则（1 天）

**目标**：删除 `pages/` `hooks/` `services/` `contexts/` `components/` 老目录；ESLint 边界从 warn 改 error；strict 改 true。

**改动**：

- `git rm -r web/src/{pages,hooks,services,contexts,components}`（应该全部已经搬完）
- `eslint.config.js` `no-restricted-paths` 升 error
- `tsconfig.json` `"strict": true` `"noImplicitAny": true`
- 修剩余的类型错误（预计零星）

**验证**：

- 全量 `npm run lint && npm run typecheck && npm run build && npm test`
- 启动 dev，按一遍所有路由 happy path

---

### Stage 5：补测 + 文档（1 天）

- 关键 hook（useAgentChat / useChatStream）补测
- shared/ui 全部组件补测
- 写 `web/ARCHITECTURE.md` 描述目录约定 + 4 条强制规则
- 更新根 `CLAUDE.md` 前端规范段落，加入 module 边界

**验收**：

- 测试覆盖率：模块 api/hook/model 三层 ≥70%（不强求 page）
- 文档可独立读懂

---

## 验证计划

| 阶段 | 自动化 | 手动 |
|---|---|---|
| 0 | build / typecheck / test 空套件绿 | 启动 dev，所有路由可达 |
| 1 | shared/ui 单测；build/lint/test 绿 | 顶部导航、切租户、菜单高亮 |
| 2 | iam 三层单测 ≥60%；build 绿 | 登录全流程、切租户、邀请成员、admin 列表 |
| 3.x | 该 module 三层单测；msw mock 后端 | 该 module 全部页面 CRUD |
| 4 | 全量 lint/typecheck/build/test 绿 | 全站 happy path 走一遍 |
| 5 | 覆盖率达标；文档评审 | 新人能按 ARCHITECTURE.md 加一个 module |

每个 PR 需附手测清单截图（侧边栏路由 + 创建/编辑/删除 三件套）。

---

## 风险与回滚

| 风险 | 应对 |
|---|---|
| TS 类型一波卡住 | Stage 0 用 `allowJs` + `strict: false`；Stage 4 才提严 |
| TanStack Query 学习成本 | Stage 2 pilot 让团队先吃透；提供 `agentKeys` 模板 |
| Zustand 替换 Context 改 API | `useAuth()` / `useChatStream()` 形状保留不变 |
| ChatStream SSE 行为退化 | Stage 3.5 单独跑 SSE 流式 e2e 手测，对比改前 |
| 跨模块隐性依赖未发现 | ESLint 边界规则从 warn 起步；Stage 4 升 error 时一次性修 |
| 单 PR 太大 review 不动 | 每个 module 一个 PR，Stage 0/1 各一个 |

每个阶段独立 revert 不影响其他阶段。Stage 4 之前 `pages/` 等老目录都还在，可以单 module 回滚到 Stage 2 之前的形态。

---

## 不在本次重构范围

- 不改后端
- 不改 Ant Design / Vite / React Router 主版本
- 不引入 react-hook-form / Tailwind / CSS-in-JS
- 不引入 i18n（中文硬编码保持现状）
- 不做 a11y / WCAG 专项
- 不做性能优化（虚拟滚动、code split 细化等）
- 不写 e2e 自动化（playwright），只做手测

---

## 完成后形态对比

| 指标 | 现状 | 完成后 |
|---|---|---|
| 文件数 | 50 | ~120（增加 module 子目录 + 测试） |
| 总行数 | 5495 | 7000-8000（含测试 + 类型） |
| `App.jsx` 行数 | 356 | ≤30 |
| `pages/` 平均行数 | 172 | ≤120（重渲染逻辑外移） |
| 共享 UI 组件数 | 1（PrivateRoute） | 8-10 |
| TypeScript 覆盖 | 0% | 100%（strict） |
| 测试 | 0 | 三层 ≥70% |
| 跨模块违规检测 | 无 | ESLint 编译期 |
| 后端模块对应 | 模糊 | 1:1 |
