# MCP 服务器管理前端界面 Design

## Goal

在租户前端实现 MCP 服务器的连接管理界面，允许用户连接新服务器（stdio / sse / http）、断开已有服务器、查看服务器暴露的 Tools 和 Resources。

## Architecture

单页面方案：`web/src/pages/MCPServersPage.jsx`，包含三个内部组件（ServerTable、ConnectDrawer、ServerDetailDrawer），所有状态集中在页面组件持有。`api.js` 新增 5 个 MCP 相关函数。`App.jsx` 新增路由 `/mcp` 和菜单项。

**Tech Stack:** React 18 · Ant Design 5 · Axios · React Router 6

---

## 文件变更

| 文件 | 操作 | 说明 |
|------|------|------|
| `web/src/pages/MCPServersPage.jsx` | 新建 | 主页面，含 ServerTable / ConnectDrawer / ServerDetailDrawer |
| `web/src/services/api.js` | 修改 | 新增 5 个 MCP API 函数 |
| `web/src/App.jsx` | 修改 | 新增路由 `/mcp` + 菜单项「MCP 服务器」|

---

## 组件设计

### MCPServersPage（顶层）

状态：
- `servers: []` — 服务器列表，从 `GET /api/v1/mcp/servers` 加载
- `loading: bool` — 列表加载中
- `connectOpen: bool` — ConnectDrawer 开关
- `detailServer: object | null` — 当前查看的服务器，null 时 DetailDrawer 关闭

生命周期：mount 时调用 `fetchServers()`。

### ServerTable

AntD Table，`dataSource=servers`，列定义：

| 列 | 字段 | 渲染 |
|----|------|------|
| 名称 | `name` | 文本 |
| ID | `id` | `ellipsis: true`，宽 180px |
| Transport | `transport` | `<Tag>`：stdio=blue / sse=green / http=cyan |
| 状态 | `status` | `<Badge>`：connected=success / disconnected=default / error=error |
| Tools | 动态 | `server.tools?.length ?? '-'` |
| 操作 | — | 「查看」按钮 + 「断开」Popconfirm（仅 connected 状态可点断开）|

顶部右侧：「连接服务器」按钮（type=primary，icon=PlusOutlined），点击打开 ConnectDrawer。

### ConnectDrawer

宽度 480px，标题「连接 MCP 服务器」。

Form 字段（layout=vertical）：

| 字段 | 组件 | 必填 | 说明 |
|------|------|------|------|
| 服务器 ID | Input | 否 | 留空则前端生成 `crypto.randomUUID()` |
| 服务器名称 | Input | 是 | maxLength=64 |
| Transport | Select | 是 | 选项：stdio / sse / http |
| 命令（command）| Input | stdio 必填 | 仅 transport=stdio 时显示 |
| 参数（args）| Input | 否 | 仅 stdio 显示；placeholder「--port 3000 --verbose」；提交时按空格分割为数组 |
| 环境变量（env）| TextArea | 否 | 仅 stdio 显示；每行 KEY=VALUE；提交时解析为 `{KEY: VALUE}` map |
| URL | Input | sse/http 必填 | 仅 transport=sse/http 时显示 |
| 超时（秒）| InputNumber | 否 | 默认 30，min=1，max=300 |

提交逻辑：
1. 校验通过后构造 `MCPServerConfig` 对象
2. 调用 `connectMCPServer(cfg)`
3. 成功：关闭 Drawer，`message.success`，刷新列表
4. 失败：`message.error(err.response?.data?.error || '连接失败')`

### ServerDetailDrawer

宽度 640px，标题为服务器名称。打开时并发请求 `getMCPServerTools(id)` 和 `getMCPServerResources(id)`。

内容结构：
- 顶部 Descriptions（4列）：ID、Transport、状态、版本
- Tabs：
  - **工具**（默认激活）：Table，列 name（宽 200）+ description（ellipsis）；无数据时显示「此服务器未暴露任何工具」
  - **资源**：Table，列 uri（宽 200，ellipsis）+ name + mimeType；无数据时显示「此服务器未暴露任何资源」

---

## API 层（api.js 新增）

```js
// MCP
export const getMCPServers = () => api.get('/api/v1/mcp/servers');
export const connectMCPServer = (data) => api.post('/api/v1/mcp/servers', data);
export const disconnectMCPServer = (id) => api.delete(`/api/v1/mcp/servers/${id}`);
export const getMCPServerTools = (id) => api.get(`/api/v1/mcp/servers/${id}/tools`);
export const getMCPServerResources = (id) => api.get(`/api/v1/mcp/servers/${id}/resources`);
```

---

## 路由 & 菜单（App.jsx）

新增路由（放在 `/memory` 路由之后）：
```jsx
<Route path="/mcp" element={<PrivateRoute><MCPServersPage /></PrivateRoute>} />
```

新增菜单项（`menuItems` 数组，放在「记忆管理」之后）：
```js
{ key: '/mcp', icon: <ApiOutlined />, label: <Link to="/mcp">MCP 服务器</Link> },
```

新增 import：
```js
import MCPServersPage from './pages/MCPServersPage';
// App.jsx icons import 中加入 ApiOutlined
```

---

## 错误处理

- 列表加载失败：`message.error('获取 MCP 服务器列表失败')`
- 连接失败：`message.error(err.response?.data?.error || '连接失败')`
- 断开失败：`message.error(err.response?.data?.error || '断开失败')`
- Tools/Resources 加载失败：DetailDrawer 内对应 Tab 显示 AntD `Alert`（type=error）

---

## 约束

- 页面组件 ≤200 行，ConnectDrawer 内部独立函数，超出则提取为独立组件文件
- 无 `console.log`，无 raw `fetch`，所有 API 调用走 `services/api.js`
- 所有用户可见文字用中文
- Transport Select 选项值与后端 `MCPServerConfig.Transport` 字段完全一致（`stdio`/`sse`/`http`）
- args 解析：按空格 split，过滤空字符串
- env 解析：按行 split，每行找第一个 `=` 分割，忽略无 `=` 的行
