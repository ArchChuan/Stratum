# MCP 服务器管理前端界面 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在租户前端实现 MCP 服务器连接管理界面，支持 stdio/sse/http 三种 transport，提供连接、断开、查看 Tools/Resources 功能。

**Architecture:** 单页面 `MCPServersPage.jsx`，内含 `ConnectDrawer`（连接表单）和 `ServerDetailDrawer`（Tools/Resources 查看）两个子组件；状态集中在页面顶层持有。`api.js` 新增 5 个 MCP API 函数，`App.jsx` 新增路由 `/mcp` 和菜单项。

**Tech Stack:** React 18 · Ant Design 5 · Axios · React Router 6 · Vite 4

---

## 文件结构

| 文件 | 操作 | 说明 |
|------|------|------|
| `web/src/services/api.js` | 修改 | 末尾新增 5 个 MCP 函数 |
| `web/src/pages/MCPServersPage.jsx` | 新建 | 主页面 + ConnectDrawer + ServerDetailDrawer |
| `web/src/App.jsx` | 修改 | import MCPServersPage、添加 Route、添加 menuItem |

---

### Task 1: api.js 新增 MCP API 函数

**Files:**
- Modify: `web/src/services/api.js:148-149`（在 `export default api;` 前插入）

- [ ] **Step 1: 在 api.js 末尾、`export default api;` 之前追加 MCP 函数**

打开 `web/src/services/api.js`，在第 149 行 `export default api;` 之前插入：

```js
// MCP
export const getMCPServers = () => api.get('/api/v1/mcp/servers');
export const connectMCPServer = (data) => api.post('/api/v1/mcp/servers', data);
export const disconnectMCPServer = (id) => api.delete(`/api/v1/mcp/servers/${id}`);
export const getMCPServerTools = (id) => api.get(`/api/v1/mcp/servers/${id}/tools`);
export const getMCPServerResources = (id) => api.get(`/api/v1/mcp/servers/${id}/resources`);
```

- [ ] **Step 2: 验证文件无语法错误**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run build 2>&1 | head -30
```

Expected: 构建成功，或仅因其他已知问题警告，无 api.js 语法错误。

- [ ] **Step 3: Commit**

```bash
git add web/src/services/api.js
git commit -m "feat(mcp): add MCP API service functions"
```

---

### Task 2: 新建 MCPServersPage.jsx

**Files:**
- Create: `web/src/pages/MCPServersPage.jsx`

页面完整实现分3个步骤写入。

- [ ] **Step 1: 写 MCPServersPage.jsx（第一段：imports + ConnectDrawer）**

新建文件 `web/src/pages/MCPServersPage.jsx`，写入以下内容（第 1~120 行）：

```jsx
import React, { useState, useEffect } from 'react';
import {
  Table, Button, Tag, Badge, Popconfirm, Drawer, Form, Input,
  Select, InputNumber, Space, Descriptions, Tabs, Alert, message,
} from 'antd';
import { PlusOutlined } from '@ant-design/icons';
import {
  getMCPServers, connectMCPServer, disconnectMCPServer,
  getMCPServerTools, getMCPServerResources,
} from '../services/api';

const TRANSPORT_COLORS = { stdio: 'blue', sse: 'green', http: 'cyan' };
const STATUS_MAP = { connected: 'success', disconnected: 'default', error: 'error' };

function parseArgs(str) {
  return (str || '').split(/\s+/).filter(Boolean);
}

function parseEnv(str) {
  const result = {};
  (str || '').split('\n').forEach((line) => {
    const idx = line.indexOf('=');
    if (idx > 0) result[line.slice(0, idx).trim()] = line.slice(idx + 1);
  });
  return result;
}

function ConnectDrawer({ open, onClose, onSuccess }) {
  const [form] = Form.useForm();
  const [submitting, setSubmitting] = useState(false);
  const transport = Form.useWatch('transport', form);

  const handleFinish = async (values) => {
    setSubmitting(true);
    try {
      const cfg = {
        id: values.id || crypto.randomUUID(),
        name: values.name,
        transport: values.transport,
        command: values.command || '',
        args: parseArgs(values.args),
        env: parseEnv(values.env),
        url: values.url || '',
        timeout: (values.timeout_sec || 30) * 1e9,
      };
      await connectMCPServer(cfg);
      message.success('服务器连接成功');
      form.resetFields();
      onSuccess();
    } catch (err) {
      message.error(err.response?.data?.error || '连接失败');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Drawer
      title="连接 MCP 服务器"
      width={480}
      open={open}
      onClose={() => { form.resetFields(); onClose(); }}
      destroyOnClose
    >
      <Form form={form} layout="vertical" onFinish={handleFinish}>
        <Form.Item label="服务器 ID（留空自动生成）" name="id">
          <Input placeholder="my-server-id" />
        </Form.Item>
        <Form.Item label="服务器名称" name="name" rules={[{ required: true, message: '请输入名称' }]}>
          <Input maxLength={64} />
        </Form.Item>
        <Form.Item label="Transport" name="transport" rules={[{ required: true, message: '请选择 Transport' }]}>
          <Select options={[
            { value: 'stdio', label: 'stdio（子进程）' },
            { value: 'sse',   label: 'sse（SSE 长连接）' },
            { value: 'http',  label: 'http（HTTP 轮询）' },
          ]} />
        </Form.Item>
        {transport === 'stdio' && (
          <>
            <Form.Item label="命令（command）" name="command" rules={[{ required: true, message: '请输入命令' }]}>
              <Input placeholder="node" />
            </Form.Item>
            <Form.Item label="参数（args，空格分隔）" name="args">
              <Input placeholder="server.js --port 3000" />
            </Form.Item>
            <Form.Item label="环境变量（每行 KEY=VALUE）" name="env">
              <Input.TextArea rows={4} placeholder="API_KEY=xxx&#10;DEBUG=true" />
            </Form.Item>
          </>
        )}
        {(transport === 'sse' || transport === 'http') && (
          <Form.Item label="URL" name="url" rules={[{ required: true, message: '请输入 URL' }]}>
            <Input placeholder="http://localhost:3000/mcp" />
          </Form.Item>
        )}
        <Form.Item label="超时（秒）" name="timeout_sec" initialValue={30}>
          <InputNumber min={1} max={300} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item>
          <Button type="primary" htmlType="submit" block loading={submitting}>连接</Button>
        </Form.Item>
      </Form>
    </Drawer>
  );
}
```

- [ ] **Step 2: 追加 ServerDetailDrawer 组件（第二段）**

在同一文件 `MCPServersPage.jsx` 末尾追加：

```jsx
function ServerDetailDrawer({ server, onClose }) {
  const [tools, setTools] = useState([]);
  const [resources, setResources] = useState([]);
  const [loadingTools, setLoadingTools] = useState(false);
  const [loadingRes, setLoadingRes] = useState(false);
  const [toolsError, setToolsError] = useState(null);
  const [resError, setResError] = useState(null);

  useEffect(() => {
    if (!server) return;
    setLoadingTools(true);
    setLoadingRes(true);
    setToolsError(null);
    setResError(null);

    getMCPServerTools(server.id)
      .then((r) => setTools(r.data || []))
      .catch((e) => setToolsError(e.response?.data?.error || '加载工具失败'))
      .finally(() => setLoadingTools(false));

    getMCPServerResources(server.id)
      .then((r) => setResources(r.data || []))
      .catch((e) => setResError(e.response?.data?.error || '加载资源失败'))
      .finally(() => setLoadingRes(false));
  }, [server]);

  const toolCols = [
    { title: '名称', dataIndex: 'name', width: 200 },
    { title: '描述', dataIndex: 'description', ellipsis: true },
  ];
  const resCols = [
    { title: 'URI', dataIndex: 'uri', width: 200, ellipsis: true },
    { title: '名称', dataIndex: 'name' },
    { title: 'MIME', dataIndex: 'mimeType' },
  ];

  const tabItems = [
    {
      key: 'tools',
      label: `工具（${tools.length}）`,
      children: toolsError
        ? <Alert type="error" message={toolsError} />
        : <Table size="small" dataSource={tools} columns={toolCols} rowKey="name"
            loading={loadingTools} locale={{ emptyText: '此服务器未暴露任何工具' }}
            pagination={false} />,
    },
    {
      key: 'resources',
      label: `资源（${resources.length}）`,
      children: resError
        ? <Alert type="error" message={resError} />
        : <Table size="small" dataSource={resources} columns={resCols} rowKey="uri"
            loading={loadingRes} locale={{ emptyText: '此服务器未暴露任何资源' }}
            pagination={false} />,
    },
  ];

  return (
    <Drawer
      title={server?.name || '服务器详情'}
      width={640}
      open={!!server}
      onClose={onClose}
      destroyOnClose
    >
      {server && (
        <>
          <Descriptions size="small" column={2} style={{ marginBottom: 16 }}>
            <Descriptions.Item label="ID">{server.id}</Descriptions.Item>
            <Descriptions.Item label="Transport">
              <Tag color={TRANSPORT_COLORS[server.transport]}>{server.transport}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label="状态">
              <Badge status={STATUS_MAP[server.status] || 'default'} text={server.status} />
            </Descriptions.Item>
            <Descriptions.Item label="版本">{server.version || '-'}</Descriptions.Item>
          </Descriptions>
          <Tabs defaultActiveKey="tools" items={tabItems} />
        </>
      )}
    </Drawer>
  );
}
```

- [ ] **Step 3: 追加 MCPServersPage 主组件（第三段）**

继续在同一文件末尾追加：

```jsx
export default function MCPServersPage() {
  const [servers, setServers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [connectOpen, setConnectOpen] = useState(false);
  const [detailServer, setDetailServer] = useState(null);

  const fetchServers = async () => {
    setLoading(true);
    try {
      const res = await getMCPServers();
      setServers(res.data || []);
    } catch {
      message.error('获取 MCP 服务器列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { fetchServers(); }, []);

  const handleDisconnect = async (id) => {
    try {
      await disconnectMCPServer(id);
      message.success('已断开连接');
      fetchServers();
    } catch (err) {
      message.error(err.response?.data?.error || '断开失败');
    }
  };

  const columns = [
    { title: '名称', dataIndex: 'name' },
    { title: 'ID', dataIndex: 'id', ellipsis: true, width: 180 },
    {
      title: 'Transport', dataIndex: 'transport', width: 100,
      render: (v) => <Tag color={TRANSPORT_COLORS[v]}>{v}</Tag>,
    },
    {
      title: '状态', dataIndex: 'status', width: 110,
      render: (v) => <Badge status={STATUS_MAP[v] || 'default'} text={v} />,
    },
    {
      title: 'Tools', width: 80,
      render: (_, r) => r.tools?.length ?? '-',
    },
    {
      title: '操作', width: 160,
      render: (_, r) => (
        <Space>
          <Button size="small" onClick={() => setDetailServer(r)}>查看</Button>
          <Popconfirm
            title="确认断开此服务器连接？"
            onConfirm={() => handleDisconnect(r.id)}
            disabled={r.status !== 'connected'}
          >
            <Button size="small" danger disabled={r.status !== 'connected'}>断开</Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 16 }}>
        <span style={{ fontSize: 16, fontWeight: 500 }}>MCP 服务器</span>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setConnectOpen(true)}>
          连接服务器
        </Button>
      </div>
      <Table
        dataSource={servers}
        columns={columns}
        rowKey="id"
        loading={loading}
        locale={{ emptyText: '暂无已连接的 MCP 服务器' }}
      />
      <ConnectDrawer
        open={connectOpen}
        onClose={() => setConnectOpen(false)}
        onSuccess={() => { setConnectOpen(false); fetchServers(); }}
      />
      <ServerDetailDrawer
        server={detailServer}
        onClose={() => setDetailServer(null)}
      />
    </>
  );
}
```

- [ ] **Step 4: 验证文件无语法错误**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run build 2>&1 | grep -E 'error|Error|MCPServersPage' | head -20
```

Expected: 无 MCPServersPage 相关错误。

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/MCPServersPage.jsx
git commit -m "feat(mcp): add MCPServersPage with ConnectDrawer and ServerDetailDrawer"
```

---

### Task 3: App.jsx 新增路由与菜单

**Files:**
- Modify: `web/src/App.jsx`

- [ ] **Step 1: 在 App.jsx 的 icon import 中追加 ApiOutlined**

打开 `web/src/App.jsx`，找到第 3~7 行的 icon import 块：

```js
import {
  AppstoreOutlined, PlusCircleOutlined, HistoryOutlined, DashboardOutlined,
  RobotOutlined, CommentOutlined, DatabaseOutlined, UserOutlined, LogoutOutlined,
  TeamOutlined, SettingOutlined, GlobalOutlined, SwapOutlined,
} from '@ant-design/icons';
```

修改为（末尾 `SwapOutlined` 后加逗号 + `ApiOutlined`）：

```js
import {
  AppstoreOutlined, PlusCircleOutlined, HistoryOutlined, DashboardOutlined,
  RobotOutlined, CommentOutlined, DatabaseOutlined, UserOutlined, LogoutOutlined,
  TeamOutlined, SettingOutlined, GlobalOutlined, SwapOutlined, ApiOutlined,
} from '@ant-design/icons';
```

- [ ] **Step 2: 在 App.jsx import 区域追加 MCPServersPage**

找到第 17 行 `import MemoryPage from './pages/MemoryPage';`，在其后插入：

```js
import MCPServersPage from './pages/MCPServersPage';
```

- [ ] **Step 3: 在 menuItems 数组中追加 MCP 菜单项**

找到 `menuItems` 数组中 `/memory` 条目：

```js
{ key: '/memory', icon: <DatabaseOutlined />, label: <Link to="/memory">记忆管理</Link> },
```

在其后追加：

```js
{ key: '/mcp', icon: <ApiOutlined />, label: <Link to="/mcp">MCP 服务器</Link> },
```

- [ ] **Step 4: 在 Routes 中追加 /mcp 路由**

找到 Routes 内 `/memory` 路由：

```jsx
<Route path="/memory" element={<PrivateRoute><MemoryPage /></PrivateRoute>} />
```

在其后追加：

```jsx
<Route path="/mcp" element={<PrivateRoute><MCPServersPage /></PrivateRoute>} />
```

- [ ] **Step 5: 构建验证**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run build 2>&1 | tail -20
```

Expected: `✓ built in` 或类似成功提示，无报错。

- [ ] **Step 6: Commit**

```bash
git add web/src/App.jsx
git commit -m "feat(mcp): add /mcp route and menu item to App"
```

---

### Task 4: 端到端冒烟测试（手动）

**Files:** 无需修改

- [ ] **Step 1: 启动前端 dev server**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run dev
```

- [ ] **Step 2: 在浏览器中打开 http://localhost:5173，登录后访问 /mcp**

验证要点：
1. 左侧菜单出现「MCP 服务器」选项，点击跳转正确
2. 页面显示「MCP 服务器」标题 + 「连接服务器」按钮
3. 服务器列表正常加载（空或有数据均可，无 JS 报错）
4. 点击「连接服务器」弹出 ConnectDrawer，宽度约 480px
5. 选择 stdio 后，显示 command/args/env 字段；选择 sse/http 后，显示 URL 字段
6. 刷新页面（F5），路由不 404（Vite proxy bypass 已配置 text/html → index.html）

- [ ] **Step 3: Lint 检查**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run lint 2>&1 | grep -E 'error|warning' | head -20
```

Expected: 零 error；warning 与 MCPServersPage 无关。

---

## Self-Review

**Spec coverage:**
- ✅ getMCPServers/connectMCPServer/disconnectMCPServer/getMCPServerTools/getMCPServerResources — Task 1
- ✅ ServerTable（名称/ID/Transport/状态/Tools/操作列）— Task 2 Step 3
- ✅ ConnectDrawer（transport-aware 字段、args/env 解析）— Task 2 Step 1
- ✅ ServerDetailDrawer（Descriptions + Tabs Tools/Resources）— Task 2 Step 2
- ✅ App.jsx Route /mcp + 菜单项 ApiOutlined — Task 3
- ✅ 错误处理（message.error 各场景）— Task 2 Step 1~3
- ✅ 手动 e2e 冒烟测试 — Task 4

**Placeholder scan:** 无 TBD / TODO / "handle edge cases"。

**Type consistency:** `getMCPServers → res.data`, `connectMCPServer(cfg)` cfg 字段与后端 MCPServerConfig JSON tag 对齐（id/name/transport/command/args/env/url/timeout）。断开用 `disconnectMCPServer(r.id)` — 与 `DELETE /api/v1/mcp/servers/:id` 一致。
