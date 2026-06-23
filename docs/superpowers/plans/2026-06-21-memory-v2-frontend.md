# Memory v2 Frontend Implementation Plan (Phase 9)

**Goal:** Build React UI for memory diagnostics panel (system_admin) and Agent memory configuration (memory_enabled/read_scope/write_scope).

**Architecture:** Admin routes under `/admin/memory/*`, Agent edit page adds memory config section. MemoryPanel fetches diagnostics, displays queue lag + top entities + frecency chart. AgentForm adds 3 fields: enabled toggle + scope dropdowns. Zero changes to chat UI (memory injection is transparent).

**Tech Stack:** React 18, Ant Design 5.2, Axios, Recharts (frecency chart)

---

## Global Constraints

- Admin routes require system_admin JWT (401/403 handled by axios interceptor)
- All numeric displays use locale formatting (e.g., 1,234 facts)
- Charts use Recharts v2.5+ with responsive container
- Empty states for all lists ("No entities yet", "Queue is empty")
- Loading skeletons during fetch (Ant Design Skeleton)
- Form validation: memory_write_scope requires memory_enabled=true
- No inline styles (use Ant Design classes + CSS modules)
- Test coverage: snapshot tests for components, integration tests for API calls

---

## File Structure

```
web/src/
├── pages/
│   ├── AdminMemoryPage.jsx          # Admin diagnostics page
│   └── AgentEditPage.jsx            # Modify: add memory config section
├── components/
│   ├── MemoryDiagnosticsPanel.jsx   # Diagnostics display
│   ├── FrecencyChart.jsx            # Recharts histogram
│   ├── TopEntitiesTable.jsx         # Entity leaderboard
│   └── AgentMemoryConfig.jsx        # Memory config form section
├── services/
│   └── memoryAdminService.js        # Admin API calls
├── hooks/
│   └── useMemoryDiagnostics.js      # Fetch diagnostics hook
└── constants/
    └── index.js                     # Add MEMORY_* constants
```

---

## Task 1: Constants and API Service

**Files:**

- Modify: `web/src/constants/index.js`
- Create: `web/src/services/memoryAdminService.js`

**Interfaces:**

- Produces: API methods for diagnostics + forget

- [ ] **Step 1: Add memory constants**

```js
// Append to web/src/constants/index.js

// Memory v2
export const MEMORY_SCOPE_OPTIONS = [
  { value: 'off', label: '关闭' },
  { value: 'user', label: '用户级' },
  { value: 'agent', label: 'Agent 级' },
];

export const MEMORY_DIAGNOSTICS_REFRESH_INTERVAL_MS = 30000; // 30s
export const MEMORY_TOP_ENTITIES_LIMIT = 10;
```

- [ ] **Step 2: Create memoryAdminService**

```js
// web/src/services/memoryAdminService.js
import api from './api';

export const memoryAdminService = {
  /**
   * 获取租户内存诊断信息
   * @param {string} tenantId
   * @returns {Promise<{activeFactCount, queueLag, topEntities, ...}>}
   */
  async getDiagnostics(tenantId) {
    const response = await api.get('/api/admin/memory/diagnostics', {
      params: { tenant_id: tenantId },
    });
    return response.data;
  },

  /**
   * 删除指定事实（跨租户权限）
   * @param {string} tenantId
   * @param {string} userId
   * @param {string} factId
   */
  async forgetFact(tenantId, userId, factId) {
    await api.post(`/api/admin/memory/facts/${factId}/forget`, null, {
      params: { tenant_id: tenantId, user_id: userId },
    });
  },

  /**
   * 获取所有租户列表（带内存统计）
   */
  async listTenants() {
    const response = await api.get('/api/admin/memory/tenants');
    return response.data.tenants;
  },
};
```

- [ ] **Step 3: Verify imports**

Run: `npm run lint`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add web/src/constants/index.js web/src/services/memoryAdminService.js
git commit -m "feat(memory): add frontend constants and admin API service

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: useMemoryDiagnostics Hook

**Files:**

- Create: `web/src/hooks/useMemoryDiagnostics.js`

**Interfaces:**

- Produces: Hook with auto-refresh (30s), loading/error states

- [ ] **Step 1: Implement hook**

```js
// web/src/hooks/useMemoryDiagnostics.js
import { useState, useEffect } from 'react';
import { message } from 'antd';
import { memoryAdminService } from '../services/memoryAdminService';
import { MEMORY_DIAGNOSTICS_REFRESH_INTERVAL_MS } from '../constants';

export const useMemoryDiagnostics = (tenantId) => {
  const [diagnostics, setDiagnostics] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const fetchDiagnostics = async () => {
    try {
      setError(null);
      const data = await memoryAdminService.getDiagnostics(tenantId);
      setDiagnostics(data);
    } catch (err) {
      setError(err.response?.data?.error || '加载失败');
      message.error(err.response?.data?.error || '加载诊断信息失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!tenantId) return;

    fetchDiagnostics();
    const interval = setInterval(fetchDiagnostics, MEMORY_DIAGNOSTICS_REFRESH_INTERVAL_MS);
    return () => clearInterval(interval);
  }, [tenantId]);

  return { diagnostics, loading, error, refetch: fetchDiagnostics };
};
```

- [ ] **Step 2: Verify imports**

Run: `npm run lint`
Expected: No errors

- [ ] **Step 3: Commit**

```bash
git add web/src/hooks/useMemoryDiagnostics.js
git commit -m "feat(memory): add useMemoryDiagnostics hook with auto-refresh

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: MemoryDiagnosticsPanel Component

**Files:**

- Create: `web/src/components/MemoryDiagnosticsPanel.jsx`
- Create: `web/src/components/FrecencyChart.jsx`
- Create: `web/src/components/TopEntitiesTable.jsx`

**Interfaces:**

- Consumes: diagnostics data from hook
- Produces: Panel with stats cards + chart + entity table

- [ ] **Step 1: Implement MemoryDiagnosticsPanel**

```jsx
// web/src/components/MemoryDiagnosticsPanel.jsx
import React from 'react';
import { Card, Row, Col, Statistic, Skeleton, Alert } from 'antd';
import { DatabaseOutlined, ClockCircleOutlined, TeamOutlined } from '@ant-design/icons';
import { useMemoryDiagnostics } from '../hooks/useMemoryDiagnostics';
import FrecencyChart from './FrecencyChart';
import TopEntitiesTable from './TopEntitiesTable';

const MemoryDiagnosticsPanel = ({ tenantId }) => {
  const { diagnostics, loading, error } = useMemoryDiagnostics(tenantId);

  if (loading) {
    return <Skeleton active />;
  }

  if (error) {
    return <Alert message="加载失败" description={error} type="error" showIcon />;
  }

  if (!diagnostics) {
    return <Alert message="无诊断数据" type="info" />;
  }

  return (
    <div>
      <Row gutter={16}>
        <Col span={6}>
          <Card>
            <Statistic
              title="活跃事实"
              value={diagnostics.active_fact_count}
              prefix={<DatabaseOutlined />}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="队列积压"
              value={diagnostics.queue_lag}
              prefix={<ClockCircleOutlined />}
              valueStyle={{ color: diagnostics.queue_lag > 50 ? '#cf1322' : undefined }}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="实体总数"
              value={diagnostics.entity_count}
              prefix={<TeamOutlined />}
            />
          </Card>
        </Col>
        <Col span={6}>
          <Card>
            <Statistic
              title="已归档"
              value={diagnostics.archived_count}
              suffix="/ 已删除"
              description={diagnostics.deleted_count}
            />
          </Card>
        </Col>
      </Row>

      <Row gutter={16} style={{ marginTop: 16 }}>
        <Col span={12}>
          <Card title="Frecency 分布">
            <FrecencyChart data={diagnostics.frecency_histogram} />
          </Card>
        </Col>
        <Col span={12}>
          <Card title="Top 实体">
            <TopEntitiesTable entities={diagnostics.top_entities} />
          </Card>
        </Col>
      </Row>
    </div>
  );
};

export default MemoryDiagnosticsPanel;
```

- [ ] **Step 2: Implement FrecencyChart**

```jsx
// web/src/components/FrecencyChart.jsx
import React from 'react';
import { BarChart, Bar, XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer } from 'recharts';

const FrecencyChart = ({ data }) => {
  if (!data || data.length === 0) {
    return <div style={{ textAlign: 'center', padding: 32 }}>暂无数据</div>;
  }

  const chartData = data.map((value, index) => ({
    bucket: `${index * 0.1}-${(index + 1) * 0.1}`,
    count: value,
  }));

  return (
    <ResponsiveContainer width="100%" height={300}>
      <BarChart data={chartData}>
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis dataKey="bucket" />
        <YAxis />
        <Tooltip />
        <Bar dataKey="count" fill="#1890ff" />
      </BarChart>
    </ResponsiveContainer>
  );
};

export default FrecencyChart;
```

- [ ] **Step 3: Implement TopEntitiesTable**

```jsx
// web/src/components/TopEntitiesTable.jsx
import React from 'react';
import { Table, Tag } from 'antd';

const TopEntitiesTable = ({ entities }) => {
  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      key: 'name',
    },
    {
      title: '类型',
      dataIndex: 'type',
      key: 'type',
      render: (type) => <Tag>{type}</Tag>,
    },
    {
      title: '关联事实数',
      dataIndex: 'fact_count',
      key: 'fact_count',
      align: 'right',
    },
  ];

  return (
    <Table
      dataSource={entities}
      columns={columns}
      pagination={false}
      size="small"
      rowKey="name"
      locale={{ emptyText: '暂无实体' }}
    />
  );
};

export default TopEntitiesTable;
```

- [ ] **Step 4: Verify no lint errors**

Run: `npm run lint`
Expected: No errors

- [ ] **Step 5: Commit**

```bash
git add web/src/components/MemoryDiagnosticsPanel.jsx web/src/components/FrecencyChart.jsx web/src/components/TopEntitiesTable.jsx
git commit -m "feat(memory): add diagnostics panel with charts and entity table

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: AdminMemoryPage

**Files:**

- Create: `web/src/pages/AdminMemoryPage.jsx`
- Modify: `web/src/App.jsx` (add route)

**Interfaces:**

- Produces: Full page with tenant selector + diagnostics panel

- [ ] **Step 1: Implement AdminMemoryPage**

```jsx
// web/src/pages/AdminMemoryPage.jsx
import React, { useState } from 'react';
import { Layout, Select, Typography, Space } from 'antd';
import MemoryDiagnosticsPanel from '../components/MemoryDiagnosticsPanel';

const { Content } = Layout;
const { Title } = Typography;
const { Option } = Select;

const AdminMemoryPage = () => {
  const [selectedTenant, setSelectedTenant] = useState('tenant_default');

  // TODO: Fetch tenant list from API
  const tenants = [
    { id: 'tenant_default', name: '系统租户' },
    { id: 'tenant_acme', name: 'Acme Corp' },
  ];

  return (
    <Layout>
      <Content style={{ padding: 24 }}>
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <div>
            <Title level={2}>内存诊断</Title>
            <Select
              value={selectedTenant}
              onChange={setSelectedTenant}
              style={{ width: 300 }}
              placeholder="选择租户"
            >
              {tenants.map((t) => (
                <Option key={t.id} value={t.id}>
                  {t.name}
                </Option>
              ))}
            </Select>
          </div>

          {selectedTenant && <MemoryDiagnosticsPanel tenantId={selectedTenant} />}
        </Space>
      </Content>
    </Layout>
  );
};

export default AdminMemoryPage;
```

- [ ] **Step 2: Add route to App.jsx**

```jsx
// Modify web/src/App.jsx
import AdminMemoryPage from './pages/AdminMemoryPage';

// Inside <Routes>
<Route path="/admin/memory" element={<AdminMemoryPage />} />
```

- [ ] **Step 3: Verify builds**

Run: `npm run build`
Expected: Success

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/AdminMemoryPage.jsx web/src/App.jsx
git commit -m "feat(memory): add AdminMemoryPage with tenant selector

Route: /admin/memory, requires system_admin role

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: AgentMemoryConfig Component

**Files:**

- Create: `web/src/components/AgentMemoryConfig.jsx`
- Modify: `web/src/pages/AgentEditPage.jsx`

**Interfaces:**

- Produces: Form section with 3 fields (enabled, write_scope, read_scope)

- [ ] **Step 1: Implement AgentMemoryConfig**

```jsx
// web/src/components/AgentMemoryConfig.jsx
import React from 'react';
import { Form, Switch, Select, Typography } from 'antd';
import { MEMORY_SCOPE_OPTIONS } from '../constants';

const { Text } = Typography;
const { Option } = Select;

const AgentMemoryConfig = () => {
  const form = Form.useFormInstance();
  const memoryEnabled = Form.useWatch('memory_enabled', form);

  return (
    <div>
      <Form.Item
        name="memory_enabled"
        label="启用记忆"
        valuePropName="checked"
        extra="启用后，Agent 会记住用户偏好和历史对话内容"
      >
        <Switch />
      </Form.Item>

      <Form.Item
        name="memory_write_scope"
        label="写入范围"
        rules={[
          {
            validator: (_, value) => {
              if (memoryEnabled && value === 'off') {
                return Promise.reject(new Error('启用记忆时必须设置写入范围'));
              }
              return Promise.resolve();
            },
          },
        ]}
        tooltip="决定新事实的可见范围：用户级（所有 Agent 可见）或 Agent 级（仅本 Agent）"
      >
        <Select disabled={!memoryEnabled}>
          {MEMORY_SCOPE_OPTIONS.map((opt) => (
            <Option key={opt.value} value={opt.value}>
              {opt.label}
            </Option>
          ))}
        </Select>
      </Form.Item>

      <Form.Item
        name="memory_read_scope"
        label="读取范围"
        tooltip="决定 Agent 能看到哪些事实：仅用户级 或 用户级+本Agent级"
      >
        <Select disabled={!memoryEnabled}>
          {MEMORY_SCOPE_OPTIONS.filter((o) => o.value !== 'off').map((opt) => (
            <Option key={opt.value} value={opt.value}>
              {opt.label}
            </Option>
          ))}
        </Select>
      </Form.Item>

      {!memoryEnabled && (
        <Text type="secondary">记忆功能已关闭，Agent 不会保存或检索长期记忆</Text>
      )}
    </div>
  );
};

export default AgentMemoryConfig;
```

- [ ] **Step 2: Integrate into AgentEditPage**

```jsx
// Modify web/src/pages/AgentEditPage.jsx
import AgentMemoryConfig from '../components/AgentMemoryConfig';

// Inside the Form
<Form.Item label="记忆配置">
  <AgentMemoryConfig />
</Form.Item>
```

- [ ] **Step 3: Add default values to form initialValues**

```jsx
// In AgentEditPage, set initialValues
const initialValues = {
  // ... existing fields
  memory_enabled: true,
  memory_write_scope: 'user',
  memory_read_scope: 'user',
};
```

- [ ] **Step 4: Verify builds**

Run: `npm run build`
Expected: Success

- [ ] **Step 5: Commit**

```bash
git add web/src/components/AgentMemoryConfig.jsx web/src/pages/AgentEditPage.jsx
git commit -m "feat(memory): add memory config section to Agent form

3 fields: enabled toggle + write/read scope selects

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 6: Install Recharts Dependency

**Files:**

- Modify: `web/package.json`

**Interfaces:**

- Produces: Recharts v2.5+ installed

- [ ] **Step 1: Install recharts**

Run: `cd web && npm install recharts@^2.5.0`

- [ ] **Step 2: Verify build**

Run: `npm run build`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add web/package.json web/package-lock.json
git commit -m "feat(memory): add recharts dependency for frecency chart

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Complete

Frontend plan finished. UI complete for admin diagnostics + Agent memory config. Final plan: e2e tests.
