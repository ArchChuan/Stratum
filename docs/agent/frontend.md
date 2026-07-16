# 前端规范（web/）

## 技术栈

React 18 · Vite 4 · Ant Design 5.2 · React Router 6 · Axios · Moment.js

| 目录 | 职责 |
|------|------|
| `components/` | 共享 UI 组件 |
| `hooks/` | 自定义 Hook（`use*` 命名） |
| `pages/` | 路由页面组件（`*Page.jsx`，≤200 行） |
| `services/` | API 调用层（唯一 axios 实例） |
| `utils/` | 纯函数工具 |
| `contexts/` | React Context |

## 编码规则

- 所有 API 调用走 `services/api.js` 的 axios 实例，禁止裸 `fetch`
- 错误统一：`message.error(err.response?.data?.error || '操作失败')`
- 禁止跨 `pages/` 目录导入；页面组件 ≤200 行，超出提取到 hooks/utils
- `useEffect` 依赖必须完整；异步 effect 需要 `let cancelled = false` 清理
- 用 `message` / `Modal.confirm`，禁止 `alert()` / `confirm()`
- 用户可见字符串全部中文；禁止 `console.log` 提交
- Token 禁止存 `localStorage`，用 `httpOnly` cookie 或内存 Context

## 行为常量

见 [constants.md](constants.md) 前端部分——禁止页面内硬编码 timeout / pageSize 等。

## 命名约定

- Modal 开关 `createOpen` / `editOpen`
- loading 状态 `createLoading` / `deleteLoading`
- 服务层函数：动词 + 实体名 `createWorkspace`
- Hook 返参直接解构，不加 `state` 前缀
