# Multi-Tenant Plan 6: 前端改造 — 认证、Onboarding、权限守卫

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 ClawHermes 前端接入 GitHub OAuth 登录，实现多租户 Onboarding 流程、基于角色的路由守卫，以及成员管理与租户设置页面。

**Architecture:** 使用 React Context（AuthContext）在内存中持有 access_token 和用户信息，refresh_token 完全由后端通过 httpOnly cookie 管理，前端不接触。axios 实例统一加 `withCredentials: true`，401 时自动调用 `/auth/refresh` 刷新 token 并重试原请求。PrivateRoute 组件根据登录状态和角色在路由层统一拦截，避免在每个页面重复守卫逻辑。

**Tech Stack:** React 18, React Router v6, Ant Design v5, axios（已安装）, Vite（VITE_API_BASE_URL env var）

---

## File Map

| 操作 | 文件 | 职责 |
|------|------|------|
| Create | `web/src/contexts/AuthContext.jsx` | 全局认证状态：user 对象、accessToken（内存）、login/logout/refreshToken 方法 |
| Create | `web/src/hooks/useAuth.js` | `useContext(AuthContext)` wrapper，供各组件调用 |
| Modify | `web/src/services/api.js` | 加 Bearer token 注入 + 401 自动刷新重试拦截器 |
| Create | `web/src/pages/auth/LoginPage.jsx` | GitHub OAuth 入口页，点击跳转 `/auth/github` |
| Create | `web/src/pages/auth/CallbackPage.jsx` | 处理 `/auth/callback`，调 `/auth/me`，按 `is_new` 分流 |
| Create | `web/src/pages/auth/OnboardingPage.jsx` | 创建租户 / 加入租户（两 Tab 表单） |
| Create | `web/src/components/PrivateRoute.jsx` | 路由守卫：未登录→/login，无租户→/onboarding，global_admin 守卫 |
| Create | `web/src/pages/tenant/MembersPage.jsx` | 成员列表 + 邀请成员 Modal + 移除成员（admin 限定） |
| Create | `web/src/pages/tenant/SettingsPage.jsx` | 租户名称 / 头像 URL 编辑表单 |
| Create | `web/src/pages/admin/TenantsListPage.jsx` | 全局管理员：所有租户列表，可禁用/启用 |
| Modify | `web/src/App.jsx` | 包裹 AuthContext.Provider，注册新路由，现有路由加 PrivateRoute |

---

### Task 1: 确认 axios 已安装，配置 .env 模板

**Files:**
- Check: `web/package.json`
- Modify: `web/.env.example`（如不存在则 create）

- [ ] **Step 1: 确认 axios 存在于 dependencies**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
grep '"axios"' package.json
```

Expected: `"axios": "^1.3.4"` — 已安装，无需 npm install。

- [ ] **Step 2: 查看 / 创建 .env.example**

```bash
ls /home/yang/go-projects/ClawHermes-AI-Go/web/.env* 2>/dev/null || echo "no env files"
```

- [ ] **Step 3: 在 web/ 根目录确保 .env.development 存在并包含 VITE_API_BASE_URL**

如文件不存在，创建 `web/.env.development`：

```
VITE_API_BASE_URL=http://localhost:8080
```

如已存在，确认包含该变量即可。

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/.env.development web/.env.example
git commit -m "chore(web): ensure VITE_API_BASE_URL env var template"
```

---

### Task 2: 创建 AuthContext + useAuth hook

**Files:**
- Create: `web/src/contexts/AuthContext.jsx`
- Create: `web/src/hooks/useAuth.js`

AuthContext 存储：
- `user`: `{ id, github_login, avatar_url, global_role, current_tenant, role }` | `null`
- `accessToken`: string | `null`（内存，不写 localStorage）
- `login(user, token)` — 由 CallbackPage 在拿到 `/auth/me` 响应后调用
- `logout()` — 清空内存状态，调 `/auth/logout`，跳 `/login`
- `setAccessToken(token)` — 供 interceptor 刷新后更新 token
- `loading` boolean — 初始化时从 `/auth/me` 恢复会话用

- [ ] **Step 1: 创建 `web/src/contexts/AuthContext.jsx`**

```jsx
// web/src/contexts/AuthContext.jsx
import React, { createContext, useState, useEffect, useRef } from 'react';
import api from '../services/api';

export const AuthContext = createContext(null);

export const AuthProvider = ({ children }) => {
  const [user, setUser] = useState(null);
  const [accessToken, setAccessToken] = useState(null);
  const [loading, setLoading] = useState(true);
  // ref 供 axios interceptor 同步读取最新 token，避免闭包陈旧值
  const tokenRef = useRef(null);

  const updateToken = (token) => {
    tokenRef.current = token;
    setAccessToken(token);
  };

  // 应用启动时用 httpOnly cookie 中的 refresh_token 恢复会话
  useEffect(() => {
    const restoreSession = async () => {
      try {
        const res = await api.get('/auth/me');
        setUser(res.data.user);
        updateToken(res.data.access_token);
      } catch {
        // cookie 无效或已过期，用户需要重新登录
        setUser(null);
        updateToken(null);
      } finally {
        setLoading(false);
      }
    };
    restoreSession();
  }, []);

  const login = (userData, token) => {
    setUser(userData);
    updateToken(token);
  };

  const logout = async () => {
    try {
      await api.post('/auth/logout');
    } catch {
      // 忽略错误，强制本地清理
    }
    setUser(null);
    updateToken(null);
  };

  return (
    <AuthContext.Provider
      value={{ user, accessToken, tokenRef, loading, login, logout, setAccessToken: updateToken }}
    >
      {children}
    </AuthContext.Provider>
  );
};
```

- [ ] **Step 2: 创建 `web/src/hooks/useAuth.js`**

```js
// web/src/hooks/useAuth.js
import { useContext } from 'react';
import { AuthContext } from '../contexts/AuthContext';

export const useAuth = () => {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error('useAuth must be used inside AuthProvider');
  }
  return ctx;
};
```

- [ ] **Step 3: 手动验证（无副作用）**

在浏览器 console 尚不可测；稍后在 Task 11 集成后一并验证。此步只需确认文件存在语法无报错：

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
node --input-type=module --eval "import('./src/hooks/useAuth.js').then(()=>console.log('OK'))" 2>&1 | head -5
```

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/contexts/AuthContext.jsx web/src/hooks/useAuth.js
git commit -m "feat(web): add AuthContext and useAuth hook"
```

---

### Task 3: 改造 api.js — Bearer token 注入 + 401 自动刷新重试

**Files:**
- Modify: `web/src/services/api.js`

注意：axios 实例已存在，只需补充拦截器逻辑。`withCredentials: true` 让浏览器在跨域请求时携带 httpOnly cookie（refresh_token）。

- [ ] **Step 1: 替换 `web/src/services/api.js` 全文**

```js
// web/src/services/api.js
import axios from 'axios';

// -------------------------------------------------------
// axios 实例
// -------------------------------------------------------
const api = axios.create({
  baseURL: import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080',
  timeout: 10000,
  withCredentials: true, // 携带 httpOnly cookie (refresh_token)
});

// -------------------------------------------------------
// Token 注入
// AuthContext 初始化后通过 setupApiInterceptors() 注入 tokenRef
// -------------------------------------------------------
let _tokenRef = { current: null };

export const setupApiInterceptors = (tokenRef, onLogout) => {
  _tokenRef = tokenRef;

  // 请求拦截：注入 Bearer token
  api.interceptors.request.use(
    (config) => {
      const token = _tokenRef.current;
      if (token) {
        config.headers['Authorization'] = `Bearer ${token}`;
      }
      return config;
    },
    (error) => Promise.reject(error)
  );

  // 响应拦截：401 自动刷新并重试
  let isRefreshing = false;
  let pendingQueue = [];

  const processQueue = (error, token = null) => {
    pendingQueue.forEach((p) => (error ? p.reject(error) : p.resolve(token)));
    pendingQueue = [];
  };

  api.interceptors.response.use(
    (response) => response,
    async (error) => {
      const originalRequest = error.config;

      if (error.response?.status === 401 && !originalRequest._retry) {
        if (isRefreshing) {
          return new Promise((resolve, reject) => {
            pendingQueue.push({ resolve, reject });
          }).then((token) => {
            originalRequest.headers['Authorization'] = `Bearer ${token}`;
            return api(originalRequest);
          });
        }

        originalRequest._retry = true;
        isRefreshing = true;

        try {
          const res = await api.post('/auth/refresh'); // cookie 自动携带
          const newToken = res.data.access_token;
          _tokenRef.current = newToken;
          processQueue(null, newToken);
          originalRequest.headers['Authorization'] = `Bearer ${newToken}`;
          return api(originalRequest);
        } catch (refreshError) {
          processQueue(refreshError, null);
          onLogout?.();
          return Promise.reject(refreshError);
        } finally {
          isRefreshing = false;
        }
      }

      return Promise.reject(error);
    }
  );
};

// -------------------------------------------------------
// Health Check
// -------------------------------------------------------
export const checkHealth = () => api.get('/health');

// -------------------------------------------------------
// Auth API
// -------------------------------------------------------
export const getMe = () => api.get('/auth/me');
export const postLogout = () => api.post('/auth/logout');
export const postRefresh = () => api.post('/auth/refresh');

// -------------------------------------------------------
// Tenant API
// -------------------------------------------------------
export const createTenant = (data) => api.post('/tenants', data);
export const joinTenant = (inviteCode) => api.post('/tenants/join', { invite_code: inviteCode });
export const getTenantMembers = () => api.get('/tenants/members');
export const inviteMember = (data) => api.post('/tenants/members/invite', data);
export const removeMember = (userId) => api.delete(`/tenants/members/${userId}`);
export const updateTenant = (data) => api.put('/tenants', data);
export const getAllTenants = () => api.get('/admin/tenants');
export const setTenantEnabled = (tenantId, enabled) =>
  api.patch(`/admin/tenants/${tenantId}`, { enabled });

// -------------------------------------------------------
// Skills API
// -------------------------------------------------------
export const getAllSkills = () => api.get('/skills');
export const getSkillById = (id) => api.get(`/skills/${id}`);
export const createSkill = (data) => api.post('/skills', data);
export const updateSkill = (id, data) => api.put(`/skills/${id}`, data);
export const deleteSkill = (id) => api.delete(`/skills/${id}`);

// -------------------------------------------------------
// Agents API
// -------------------------------------------------------
export const getAllAgents = () => api.get('/agents');
export const getAgentById = (id) => api.get(`/agents/${id}`);
export const createAgent = (data) => api.post('/agents', data);
export const executeAgent = (id, task) => api.post(`/agents/${id}/execute`, task);

// -------------------------------------------------------
// Memory API
// -------------------------------------------------------
export const createSession = (data) => api.post('/memory/sessions', data);
export const addMemory = (data) => api.post('/memory', data);
export const getMemoryById = (id) => api.get(`/memory/${id}`);
export const searchMemory = (data) => api.post('/memory/search', data);
export const deleteMemory = (id) => api.delete(`/memory/${id}`);
export const getMemoryStats = (params) => api.get('/memory/stats', { params });
export const clearSession = (sessionId, params) => api.delete(`/memory/session/${sessionId}`, { params });
export const getMemoryEntities = (params) => api.get('/memory/entities', { params });
export const extractEntities = (data) => api.post('/memory/extract-entities', data);
export const getMemorySummary = (sessionId, params) => api.get(`/memory/summary/${sessionId}`, { params });

export default api;
```

- [ ] **Step 2: 手动测试（检查语法）**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npx vite build --mode development 2>&1 | grep -E "error|Error|warn" | head -20
```

Expected: 无 error（warn 可接受）。

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/services/api.js
git commit -m "feat(web): axios interceptors — Bearer token inject + 401 auto-refresh"
```

---

### Task 4: LoginPage

**Files:**
- Create: `web/src/pages/auth/LoginPage.jsx`

- [ ] **Step 1: 创建目录并写文件**

```bash
mkdir -p /home/yang/go-projects/ClawHermes-AI-Go/web/src/pages/auth
```

```jsx
// web/src/pages/auth/LoginPage.jsx
import React from 'react';
import { Button, Card, Typography, Space } from 'antd';
import { GithubOutlined } from '@ant-design/icons';

const { Title, Text } = Typography;

const API_BASE = import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080';

const LoginPage = () => {
  const handleGithubLogin = () => {
    // 后端重定向到 GitHub OAuth，完成后回调 /auth/callback
    window.location.href = `${API_BASE}/auth/github`;
  };

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: '#f0f2f5',
      }}
    >
      <Card style={{ width: 380, textAlign: 'center', boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
        <Space direction="vertical" size="large" style={{ width: '100%' }}>
          <div>
            <Title level={2} style={{ marginBottom: 4 }}>ClawHermes AI</Title>
            <Text type="secondary">多租户 AI Agent 平台</Text>
          </div>
          <Button
            type="primary"
            size="large"
            icon={<GithubOutlined />}
            block
            onClick={handleGithubLogin}
          >
            使用 GitHub 登录
          </Button>
          <Text type="secondary" style={{ fontSize: 12 }}>
            登录即代表同意服务条款
          </Text>
        </Space>
      </Card>
    </div>
  );
};

export default LoginPage;
```

- [ ] **Step 2: 手动测试步骤**

启动 dev server：
```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web && npm run dev
```
访问 `http://localhost:5173/login`，应看到居中卡片 + "使用 GitHub 登录" 按钮。点击按钮，浏览器应跳转到 `http://localhost:8080/auth/github`（后端未启动时会报连接失败，属正常）。

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/pages/auth/LoginPage.jsx
git commit -m "feat(web): add GitHub OAuth LoginPage"
```

---

### Task 5: CallbackPage — 处理 OAuth 回调

**Files:**
- Create: `web/src/pages/auth/CallbackPage.jsx`

CallbackPage 挂载时：
1. 读取 URL query param `is_new`（由后端在回调 URL 中附加）
2. 调 `GET /auth/me` 获取用户信息和 access_token
3. 调 `login(user, token)` 写入 AuthContext
4. `is_new === 'true'` 跳 `/onboarding`，否则跳 `/`

- [ ] **Step 1: 创建 `web/src/pages/auth/CallbackPage.jsx`**

```jsx
// web/src/pages/auth/CallbackPage.jsx
import React, { useEffect, useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { Spin, Typography, Alert } from 'antd';
import { getMe } from '../../services/api';
import { useAuth } from '../../hooks/useAuth';

const { Text } = Typography;

const CallbackPage = () => {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { login } = useAuth();
  const [error, setError] = useState(null);

  useEffect(() => {
    const handleCallback = async () => {
      try {
        const res = await getMe();
        const { user, access_token } = res.data;
        login(user, access_token);

        const isNew = searchParams.get('is_new') === 'true';
        navigate(isNew ? '/onboarding' : '/', { replace: true });
      } catch (err) {
        const msg = err.response?.data?.message || '登录失败，请重试';
        setError(msg);
      }
    };

    handleCallback();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (error) {
    return (
      <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Alert type="error" message={error} description={<a href="/login">返回登录</a>} />
      </div>
    );
  }

  return (
    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Spin size="large" tip="正在完成登录..." />
    </div>
  );
};

export default CallbackPage;
```

- [ ] **Step 2: 手动测试步骤**

- 后端完整启动后，在浏览器完成 GitHub OAuth 流程
- GitHub 授权后，浏览器跳回 `http://localhost:5173/auth/callback?is_new=true`（或 `is_new=false`）
- 新用户应跳转到 `/onboarding`，老用户跳转到 `/`
- 若出现错误，检查后端 `/auth/me` 接口是否返回 `{ user, access_token }`

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/pages/auth/CallbackPage.jsx
git commit -m "feat(web): add OAuth CallbackPage with is_new routing"
```

---

### Task 6: OnboardingPage — 创建 / 加入租户

**Files:**
- Create: `web/src/pages/auth/OnboardingPage.jsx`

- [ ] **Step 1: 创建 `web/src/pages/auth/OnboardingPage.jsx`**

```jsx
// web/src/pages/auth/OnboardingPage.jsx
import React, { useState } from 'react';
import { Tabs, Form, Input, Button, Card, Typography, message, Space } from 'antd';
import { useNavigate } from 'react-router-dom';
import { createTenant, joinTenant, getMe } from '../../services/api';
import { useAuth } from '../../hooks/useAuth';

const { Title, Text } = Typography;

const OnboardingPage = () => {
  const navigate = useNavigate();
  const { login } = useAuth();
  const [createLoading, setCreateLoading] = useState(false);
  const [joinLoading, setJoinLoading] = useState(false);

  // 成功加入/创建租户后，重新拉取 user 信息（此时 current_tenant 已更新）
  const refreshAndRedirect = async () => {
    const res = await getMe();
    login(res.data.user, res.data.access_token);
    navigate('/', { replace: true });
  };

  const handleCreate = async (values) => {
    setCreateLoading(true);
    try {
      await createTenant({ name: values.name, slug: values.slug });
      message.success('租户创建成功！');
      await refreshAndRedirect();
    } catch (err) {
      message.error(err.response?.data?.message || '创建失败');
    } finally {
      setCreateLoading(false);
    }
  };

  const handleJoin = async (values) => {
    setJoinLoading(true);
    try {
      await joinTenant(values.invite_code);
      message.success('加入成功！');
      await refreshAndRedirect();
    } catch (err) {
      message.error(err.response?.data?.message || '加入失败，邀请码无效或已过期');
    } finally {
      setJoinLoading(false);
    }
  };

  const tabItems = [
    {
      key: 'create',
      label: '创建新租户',
      children: (
        <Form layout="vertical" onFinish={handleCreate}>
          <Form.Item
            label="租户名称"
            name="name"
            rules={[{ required: true, message: '请输入租户名称' }]}
          >
            <Input placeholder="例如：我的团队" maxLength={64} />
          </Form.Item>
          <Form.Item
            label="Slug（URL 标识）"
            name="slug"
            rules={[
              { required: true, message: '请输入 slug' },
              { pattern: /^[a-z0-9-]+$/, message: '只允许小写字母、数字和连字符' },
            ]}
          >
            <Input placeholder="例如：my-team" maxLength={32} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={createLoading}>
              创建租户
            </Button>
          </Form.Item>
        </Form>
      ),
    },
    {
      key: 'join',
      label: '加入已有租户',
      children: (
        <Form layout="vertical" onFinish={handleJoin}>
          <Form.Item
            label="邀请码"
            name="invite_code"
            rules={[{ required: true, message: '请输入邀请码' }]}
          >
            <Input placeholder="粘贴管理员给您的邀请码" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={joinLoading}>
              加入租户
            </Button>
          </Form.Item>
        </Form>
      ),
    },
  ];

  return (
    <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center', background: '#f0f2f5' }}>
      <Card style={{ width: 440, boxShadow: '0 4px 12px rgba(0,0,0,0.1)' }}>
        <Space direction="vertical" size="middle" style={{ width: '100%' }}>
          <div style={{ textAlign: 'center' }}>
            <Title level={3} style={{ marginBottom: 4 }}>欢迎使用 ClawHermes</Title>
            <Text type="secondary">创建您的租户空间，或加入已有团队</Text>
          </div>
          <Tabs defaultActiveKey="create" items={tabItems} />
        </Space>
      </Card>
    </div>
  );
};

export default OnboardingPage;
```

- [ ] **Step 2: 手动测试步骤**

访问 `http://localhost:5173/onboarding`：
- "创建新租户" tab：填写名称和 slug，点击提交，验证调用 `POST /tenants`
- "加入已有租户" tab：填写邀请码，验证调用 `POST /tenants/join`
- 表单验证：slug 填写 `My Team`（大写+空格）应报错
- 成功后应跳转到 `/`

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/pages/auth/OnboardingPage.jsx
git commit -m "feat(web): add OnboardingPage — create or join tenant"
```

---

### Task 7: PrivateRoute — 路由守卫

**Files:**
- Create: `web/src/components/PrivateRoute.jsx`

守卫逻辑：
1. `loading === true` → 显示全屏 Spin（等待会话恢复）
2. `user === null` → Navigate to `/login`
3. `user.current_tenant === null && pathname !== '/onboarding'` → Navigate to `/onboarding`
4. `requiredRole === 'global_admin' && user.global_role !== 'global_admin'` → 403 提示

- [ ] **Step 1: 创建目录并写文件**

```bash
mkdir -p /home/yang/go-projects/ClawHermes-AI-Go/web/src/components
```

```jsx
// web/src/components/PrivateRoute.jsx
import React from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { Spin, Result, Button } from 'antd';
import { useAuth } from '../hooks/useAuth';

/**
 * @param {object} props
 * @param {React.ReactNode} props.children
 * @param {'global_admin'} [props.requiredRole] - 若指定，则额外要求 global_role 匹配
 */
const PrivateRoute = ({ children, requiredRole }) => {
  const { user, loading } = useAuth();
  const location = useLocation();

  if (loading) {
    return (
      <div style={{ minHeight: '100vh', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <Spin size="large" tip="加载中..." />
      </div>
    );
  }

  if (!user) {
    return <Navigate to="/login" state={{ from: location }} replace />;
  }

  if (!user.current_tenant && location.pathname !== '/onboarding') {
    return <Navigate to="/onboarding" replace />;
  }

  if (requiredRole && user.global_role !== requiredRole) {
    return (
      <Result
        status="403"
        title="403"
        subTitle="您没有访问此页面的权限。"
        extra={<Button type="primary" onClick={() => window.history.back()}>返回</Button>}
      />
    );
  }

  return children;
};

export default PrivateRoute;
```

- [ ] **Step 2: 手动测试步骤**

（此步骤在 Task 11 集成路由后可验证）
- 未登录时访问 `/`，应跳转到 `/login`
- 已登录但无租户时，访问 `/`，应跳转到 `/onboarding`
- 访问 `/admin/tenants`（需要 global_admin），普通用户应看到 403 页面

- [ ] **Step 3: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/components/PrivateRoute.jsx
git commit -m "feat(web): add PrivateRoute guard — auth/tenant/role checks"
```

---

### Task 8: MembersPage — 成员列表 + 邀请 + 移除

**Files:**
- Create: `web/src/pages/tenant/MembersPage.jsx`

- [ ] **Step 1: 创建目录**

```bash
mkdir -p /home/yang/go-projects/ClawHermes-AI-Go/web/src/pages/tenant
```

- [ ] **Step 2: 创建 `web/src/pages/tenant/MembersPage.jsx`**

```jsx
// web/src/pages/tenant/MembersPage.jsx
import React, { useEffect, useState } from 'react';
import {
  Table, Button, Modal, Form, Input, Select, Avatar, Tag,
  Space, Typography, Popconfirm, message
} from 'antd';
import { UserAddOutlined } from '@ant-design/icons';
import { getTenantMembers, inviteMember, removeMember } from '../../services/api';
import { useAuth } from '../../hooks/useAuth';

const { Title } = Typography;

const MembersPage = () => {
  const { user } = useAuth();
  const [members, setMembers] = useState([]);
  const [loading, setLoading] = useState(false);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [inviteLoading, setInviteLoading] = useState(false);
  const [form] = Form.useForm();

  const isAdmin = user?.role === 'admin';

  const fetchMembers = async () => {
    setLoading(true);
    try {
      const res = await getTenantMembers();
      setMembers(res.data.members || []);
    } catch {
      message.error('获取成员列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchMembers();
  }, []);

  const handleInvite = async (values) => {
    setInviteLoading(true);
    try {
      await inviteMember(values);
      message.success('邀请已发送');
      setInviteOpen(false);
      form.resetFields();
      fetchMembers();
    } catch (err) {
      message.error(err.response?.data?.message || '邀请失败');
    } finally {
      setInviteLoading(false);
    }
  };

  const handleRemove = async (userId) => {
    try {
      await removeMember(userId);
      message.success('成员已移除');
      fetchMembers();
    } catch (err) {
      message.error(err.response?.data?.message || '移除失败');
    }
  };

  const columns = [
    {
      title: '用户',
      dataIndex: 'github_login',
      render: (login, record) => (
        <Space>
          <Avatar src={record.avatar_url} size="small">{login?.[0]?.toUpperCase()}</Avatar>
          {login}
        </Space>
      ),
    },
    {
      title: '角色',
      dataIndex: 'role',
      render: (role) => (
        <Tag color={role === 'admin' ? 'blue' : 'default'}>{role === 'admin' ? '管理员' : '成员'}</Tag>
      ),
    },
    {
      title: '加入时间',
      dataIndex: 'joined_at',
      render: (v) => v ? new Date(v).toLocaleDateString('zh-CN') : '-',
    },
    ...(isAdmin
      ? [
          {
            title: '操作',
            key: 'action',
            render: (_, record) =>
              record.id !== user?.id ? (
                <Popconfirm
                  title="确认移除该成员？"
                  onConfirm={() => handleRemove(record.id)}
                  okText="确认"
                  cancelText="取消"
                >
                  <Button danger size="small">移除</Button>
                </Popconfirm>
              ) : (
                <Tag>（您）</Tag>
              ),
          },
        ]
      : []),
  ];

  return (
    <div>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
        <Title level={4} style={{ margin: 0 }}>成员管理</Title>
        {isAdmin && (
          <Button type="primary" icon={<UserAddOutlined />} onClick={() => setInviteOpen(true)}>
            邀请成员
          </Button>
        )}
      </div>

      <Table
        dataSource={members}
        columns={columns}
        rowKey="id"
        loading={loading}
        pagination={{ pageSize: 20 }}
      />

      <Modal
        title="邀请成员"
        open={inviteOpen}
        onCancel={() => { setInviteOpen(false); form.resetFields(); }}
        footer={null}
      >
        <Form form={form} layout="vertical" onFinish={handleInvite}>
          <Form.Item
            label="GitHub 用户名"
            name="github_login"
            rules={[{ required: true, message: '请输入 GitHub 用户名' }]}
          >
            <Input placeholder="例如：octocat" />
          </Form.Item>
          <Form.Item label="角色" name="role" initialValue="member">
            <Select options={[{ value: 'member', label: '成员' }, { value: 'admin', label: '管理员' }]} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={inviteLoading}>
              发送邀请
            </Button>
          </Form.Item>
        </Form>
      </Modal>
    </div>
  );
};

export default MembersPage;
```

- [ ] **Step 3: 手动测试步骤**

访问 `http://localhost:5173/tenant/members`（Task 11 注册路由后）：
- admin 角色：应看到"邀请成员"按钮和每行"移除"按钮
- member 角色：按钮不可见
- 点击"邀请成员"，填写表单，验证 `POST /tenants/members/invite` 被调用
- 点击"移除"，确认弹窗后验证 `DELETE /tenants/members/{userId}` 被调用

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/pages/tenant/MembersPage.jsx
git commit -m "feat(web): add MembersPage with invite modal and remove action"
```

---

### Task 9: SettingsPage + TenantsListPage

**Files:**
- Create: `web/src/pages/tenant/SettingsPage.jsx`
- Create: `web/src/pages/admin/TenantsListPage.jsx`

- [ ] **Step 1: 创建目录**

```bash
mkdir -p /home/yang/go-projects/ClawHermes-AI-Go/web/src/pages/admin
```

- [ ] **Step 2: 创建 `web/src/pages/tenant/SettingsPage.jsx`**

```jsx
// web/src/pages/tenant/SettingsPage.jsx
import React, { useState } from 'react';
import { Form, Input, Button, Typography, message, Card } from 'antd';
import { updateTenant } from '../../services/api';
import { useAuth } from '../../hooks/useAuth';

const { Title } = Typography;

const SettingsPage = () => {
  const { user, login, accessToken } = useAuth();
  const [loading, setLoading] = useState(false);

  const handleSave = async (values) => {
    setLoading(true);
    try {
      await updateTenant(values);
      message.success('设置已保存');
      // 刷新用户信息中的租户名
      login({ ...user, current_tenant: { ...user.current_tenant, ...values } }, accessToken);
    } catch (err) {
      message.error(err.response?.data?.message || '保存失败');
    } finally {
      setLoading(false);
    }
  };

  return (
    <div>
      <Title level={4} style={{ marginBottom: 24 }}>租户设置</Title>
      <Card style={{ maxWidth: 480 }}>
        <Form
          layout="vertical"
          initialValues={{
            name: user?.current_tenant?.name || '',
            avatar_url: user?.current_tenant?.avatar_url || '',
          }}
          onFinish={handleSave}
        >
          <Form.Item
            label="租户名称"
            name="name"
            rules={[{ required: true, message: '请输入租户名称' }]}
          >
            <Input maxLength={64} />
          </Form.Item>
          <Form.Item label="头像 URL" name="avatar_url">
            <Input placeholder="https://example.com/avatar.png" />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" loading={loading}>
              保存
            </Button>
          </Form.Item>
        </Form>
      </Card>
    </div>
  );
};

export default SettingsPage;
```

- [ ] **Step 3: 创建 `web/src/pages/admin/TenantsListPage.jsx`**

```jsx
// web/src/pages/admin/TenantsListPage.jsx
import React, { useEffect, useState } from 'react';
import { Table, Button, Tag, Typography, Popconfirm, message, Space } from 'antd';
import { getAllTenants, setTenantEnabled } from '../../services/api';

const { Title } = Typography;

const TenantsListPage = () => {
  const [tenants, setTenants] = useState([]);
  const [loading, setLoading] = useState(false);

  const fetchTenants = async () => {
    setLoading(true);
    try {
      const res = await getAllTenants();
      setTenants(res.data.tenants || []);
    } catch {
      message.error('获取租户列表失败');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchTenants();
  }, []);

  const handleToggle = async (tenantId, currentEnabled) => {
    try {
      await setTenantEnabled(tenantId, !currentEnabled);
      message.success(currentEnabled ? '已禁用' : '已启用');
      fetchTenants();
    } catch (err) {
      message.error(err.response?.data?.message || '操作失败');
    }
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 80 },
    { title: '租户名称', dataIndex: 'name' },
    { title: 'Slug', dataIndex: 'slug' },
    {
      title: '成员数',
      dataIndex: 'member_count',
      render: (v) => v ?? '-',
    },
    {
      title: '状态',
      dataIndex: 'enabled',
      render: (enabled) => (
        <Tag color={enabled ? 'green' : 'red'}>{enabled ? '启用' : '禁用'}</Tag>
      ),
    },
    {
      title: '操作',
      key: 'action',
      render: (_, record) => (
        <Popconfirm
          title={`确认${record.enabled ? '禁用' : '启用'}该租户？`}
          onConfirm={() => handleToggle(record.id, record.enabled)}
          okText="确认"
          cancelText="取消"
        >
          <Button size="small" danger={record.enabled}>
            {record.enabled ? '禁用' : '启用'}
          </Button>
        </Popconfirm>
      ),
    },
  ];

  return (
    <div>
      <Title level={4} style={{ marginBottom: 16 }}>所有租户</Title>
      <Table
        dataSource={tenants}
        columns={columns}
        rowKey="id"
        loading={loading}
        pagination={{ pageSize: 20 }}
      />
    </div>
  );
};

export default TenantsListPage;
```

- [ ] **Step 4: 手动测试步骤**

- 访问 `/tenant/settings`，应看到带初始值的表单，修改名称后保存，验证 `PUT /tenants` 被调用
- 访问 `/admin/tenants`（global_admin），应看到租户列表 + 禁用/启用按钮

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/pages/tenant/SettingsPage.jsx web/src/pages/admin/TenantsListPage.jsx
git commit -m "feat(web): add SettingsPage and TenantsListPage"
```

---

### Task 10: NavBar 改造 — 右上角用户 Dropdown

**Files:**
- Modify: `web/src/App.jsx`（Header 部分内联修改，不单独提取组件）

Header 右上角需增加：头像 + 用户名 + Dropdown 菜单，包含：
- 租户成员（/tenant/members，需有 current_tenant）
- 租户设置（/tenant/settings，需 admin 角色）
- 全局管理（/admin/tenants，需 global_admin）
- 退出登录

- [ ] **Step 1: 将以下内容写入临时参考（不修改 App.jsx，Task 11 一起改）**

NavBar Dropdown 逻辑如下（供 Task 11 嵌入 Header）：

```jsx
// 嵌入 App.jsx Header 的右侧区域
import { Dropdown, Avatar } from 'antd';
import { UserOutlined, LogoutOutlined, TeamOutlined, SettingOutlined, GlobalOutlined } from '@ant-design/icons';
import { useAuth } from './hooks/useAuth';

// 在 App 组件内部：
const { user, logout } = useAuth();

const userMenuItems = [
  ...(user?.current_tenant ? [{ key: 'members', icon: <TeamOutlined />, label: <Link to="/tenant/members">成员管理</Link> }] : []),
  ...(user?.role === 'admin' ? [{ key: 'settings', icon: <SettingOutlined />, label: <Link to="/tenant/settings">租户设置</Link> }] : []),
  ...(user?.global_role === 'global_admin' ? [{ key: 'admin', icon: <GlobalOutlined />, label: <Link to="/admin/tenants">全局管理</Link> }] : []),
  { type: 'divider' },
  { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true, onClick: logout },
];

// 替换 Header 右侧 Space 为：
{user ? (
  <Dropdown menu={{ items: userMenuItems }} placement="bottomRight">
    <Space style={{ cursor: 'pointer', paddingRight: 16 }}>
      <Avatar src={user.avatar_url} icon={<UserOutlined />} size="small" />
      <span>{user.github_login}</span>
    </Space>
  </Dropdown>
) : (
  <Link to="/login" style={{ paddingRight: 16 }}>登录</Link>
)}
```

此代码将在 Task 11 整合进 App.jsx。

- [ ] **Step 2: 无需单独 commit（代码将随 Task 11 一并提交）**

---

### Task 11: App.jsx 路由整合 — AuthProvider + 新路由 + PrivateRoute

**Files:**
- Modify: `web/src/App.jsx`

这是最终集成任务，将前面所有 Task 的组件串联起来。

- [ ] **Step 1: 替换 `web/src/App.jsx` 全文**

```jsx
// web/src/App.jsx
import React, { useState, useEffect } from 'react';
import { Layout, Menu, Space, Typography, Dropdown, Avatar } from 'antd';
import {
  AppstoreOutlined,
  PlusCircleOutlined,
  HistoryOutlined,
  DashboardOutlined,
  RobotOutlined,
  CommentOutlined,
  DatabaseOutlined,
  UserOutlined,
  LogoutOutlined,
  TeamOutlined,
  SettingOutlined,
  GlobalOutlined,
} from '@ant-design/icons';
import { Routes, Route, useNavigate, Link, useLocation } from 'react-router-dom';

// 页面
import SkillsListPage from './pages/SkillsListPage';
import CreateSkillPage from './pages/CreateSkillPage';
import ExecutionHistoryPage from './pages/ExecutionHistoryPage';
import DashboardPage from './pages/DashboardPage';
import AgentsListPage from './pages/AgentsListPage';
import CreateAgentPage from './pages/CreateAgentPage';
import AgentChatPage from './pages/AgentChatPage';
import MemoryPage from './pages/MemoryPage';
// Auth 页面（不受 PrivateRoute 保护）
import LoginPage from './pages/auth/LoginPage';
import CallbackPage from './pages/auth/CallbackPage';
import OnboardingPage from './pages/auth/OnboardingPage';
// 租户 / 管理员页面
import MembersPage from './pages/tenant/MembersPage';
import SettingsPage from './pages/tenant/SettingsPage';
import TenantsListPage from './pages/admin/TenantsListPage';

// 守卫 + Context
import PrivateRoute from './components/PrivateRoute';
import { AuthProvider } from './contexts/AuthContext';
import { useAuth } from './hooks/useAuth';
import { setupApiInterceptors, checkHealth } from './services/api';

const { Header, Content, Sider } = Layout;
const { Title } = Typography;

// -------------------------------------------------------
// 内部 App（需要在 AuthProvider 内才能使用 useAuth）
// -------------------------------------------------------
const AppInner = () => {
  const [collapsed, setCollapsed] = useState(false);
  const [connected, setConnected] = useState(false);
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout, tokenRef } = useAuth();

  // 把 tokenRef 注入 axios interceptor（只执行一次）
  useEffect(() => {
    setupApiInterceptors(tokenRef, logout);
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    checkHealth()
      .then(() => setConnected(true))
      .catch(() => setConnected(false));
  }, []);

  // 未登录时不渲染侧边栏
  const isAuthPage = ['/login', '/auth/callback', '/onboarding'].includes(location.pathname);

  const menuItems = [
    { key: '/', icon: <DashboardOutlined />, label: <Link to="/">仪表盘</Link> },
    { key: '/skills', icon: <AppstoreOutlined />, label: <Link to="/skills">技能管理</Link> },
    { key: '/skills/create', icon: <PlusCircleOutlined />, label: <Link to="/skills/create">创建技能</Link> },
    { key: '/agents', icon: <RobotOutlined />, label: <Link to="/agents">智能代理</Link> },
    { key: '/agents/create', icon: <PlusCircleOutlined />, label: <Link to="/agents/create">创建代理</Link> },
    { key: '/chat', icon: <CommentOutlined />, label: <Link to="/chat">代理对话</Link> },
    { key: '/memory', icon: <DatabaseOutlined />, label: <Link to="/memory">记忆管理</Link> },
    { key: '/history', icon: <HistoryOutlined />, label: <Link to="/history">执行历史</Link> },
    ...(user?.current_tenant ? [{ key: '/tenant/members', icon: <TeamOutlined />, label: <Link to="/tenant/members">成员管理</Link> }] : []),
    ...(user?.global_role === 'global_admin' ? [{ key: '/admin/tenants', icon: <GlobalOutlined />, label: <Link to="/admin/tenants">全局租户</Link> }] : []),
  ];

  const userMenuItems = [
    ...(user?.current_tenant ? [{ key: 'members', icon: <TeamOutlined />, label: <Link to="/tenant/members">成员管理</Link> }] : []),
    ...(user?.role === 'admin' ? [{ key: 'settings', icon: <SettingOutlined />, label: <Link to="/tenant/settings">租户设置</Link> }] : []),
    ...(user?.global_role === 'global_admin' ? [{ key: 'admin', icon: <GlobalOutlined />, label: <Link to="/admin/tenants">全局管理</Link> }] : []),
    { type: 'divider' },
    { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true, onClick: () => { logout(); navigate('/login', { replace: true }); } },
  ];

  if (isAuthPage) {
    // 认证相关页面直接渲染，无需布局
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/auth/callback" element={<CallbackPage />} />
        <Route path="/onboarding" element={<OnboardingPage />} />
      </Routes>
    );
  }

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        style={{ overflow: 'auto', height: '100vh', position: 'fixed', left: 0, top: 0, bottom: 0 }}
      >
        <div style={{ padding: '16px', textAlign: 'center' }}>
          <Title style={{ color: 'white', fontSize: '16px' }} ellipsis>ClawHermes AI</Title>
        </div>
        <Menu
          theme="dark"
          selectedKeys={[location.pathname]}
          mode="inline"
          items={menuItems}
          onClick={({ key }) => navigate(key)}
        />
      </Sider>

      <Layout style={{ marginLeft: collapsed ? 80 : 200 }}>
        <Header style={{ padding: 0, background: '#fff', display: 'flex', alignItems: 'center', justifyContent: 'space-between', paddingLeft: '16px' }}>
          <Space>
            <span>状态:</span>
            <span style={{ color: connected ? 'green' : 'red' }}>{connected ? '已连接' : '未连接'}</span>
          </Space>
          {user ? (
            <Dropdown menu={{ items: userMenuItems }} placement="bottomRight">
              <Space style={{ cursor: 'pointer', paddingRight: 16 }}>
                <Avatar src={user.avatar_url} icon={<UserOutlined />} size="small" />
                <span>{user.github_login}</span>
              </Space>
            </Dropdown>
          ) : (
            <Link to="/login" style={{ paddingRight: 16 }}>登录</Link>
          )}
        </Header>

        <Content style={{ margin: '24px 16px 0', overflow: 'initial' }}>
          <div style={{ padding: 24, background: '#fff', minHeight: 360 }}>
            <Routes>
              <Route path="/" element={<PrivateRoute><DashboardPage /></PrivateRoute>} />
              <Route path="/skills" element={<PrivateRoute><SkillsListPage /></PrivateRoute>} />
              <Route path="/skills/create" element={<PrivateRoute><CreateSkillPage /></PrivateRoute>} />
              <Route path="/agents" element={<PrivateRoute><AgentsListPage /></PrivateRoute>} />
              <Route path="/agents/create" element={<PrivateRoute><CreateAgentPage /></PrivateRoute>} />
              <Route path="/chat" element={<PrivateRoute><AgentChatPage /></PrivateRoute>} />
              <Route path="/memory" element={<PrivateRoute><MemoryPage /></PrivateRoute>} />
              <Route path="/history" element={<PrivateRoute><ExecutionHistoryPage /></PrivateRoute>} />
              <Route path="/tenant/members" element={<PrivateRoute><MembersPage /></PrivateRoute>} />
              <Route path="/tenant/settings" element={<PrivateRoute><SettingsPage /></PrivateRoute>} />
              <Route path="/admin/tenants" element={<PrivateRoute requiredRole="global_admin"><TenantsListPage /></PrivateRoute>} />
              {/* 认证路由（fallback，isAuthPage 检查已优先处理） */}
              <Route path="/login" element={<LoginPage />} />
              <Route path="/auth/callback" element={<CallbackPage />} />
              <Route path="/onboarding" element={<OnboardingPage />} />
            </Routes>
          </div>
        </Content>
      </Layout>
    </Layout>
  );
};

// -------------------------------------------------------
// 根组件：包裹 AuthProvider
// -------------------------------------------------------
const App = () => (
  <AuthProvider>
    <AppInner />
  </AuthProvider>
);

export default App;
```

- [ ] **Step 2: 检查构建是否无错误**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web
npm run build 2>&1 | tail -20
```

Expected: `built in X.XXs`，无 `error` 行。

- [ ] **Step 3: 启动 dev server 做冒烟测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go/web && npm run dev
```

验证清单：
- 访问 `http://localhost:5173/` → 未登录，应跳转到 `/login`
- 访问 `http://localhost:5173/login` → 看到 GitHub 登录卡片
- 访问 `http://localhost:5173/onboarding` → 看到两 Tab 表单
- 访问 `http://localhost:5173/admin/tenants`（未登录）→ 跳转到 `/login`
- Header 显示 "已连接" / "未连接" 状态

- [ ] **Step 4: Commit**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
git add web/src/App.jsx
git commit -m "feat(web): integrate AuthProvider, PrivateRoute, and all new routes in App.jsx"
```

---

## 自检清单

| 需求 | 覆盖 Task |
|------|-----------|
| GitHub OAuth 登录 | Task 4 (LoginPage) |
| OAuth 回调处理 + is_new 分流 | Task 5 (CallbackPage) |
| 创建租户 / 加入租户 Onboarding | Task 6 (OnboardingPage) |
| access_token 存内存，不写 localStorage | Task 2 (AuthContext) |
| refresh_token 是 httpOnly cookie，前端不读取 | Task 2 (AuthContext) + Task 3 (api.js) |
| withCredentials: true | Task 3 (api.js) |
| 401 自动刷新重试 | Task 3 (api.js) |
| Bearer token 注入请求头 | Task 3 (api.js) |
| VITE_API_BASE_URL env var | Task 1 + Task 3 |
| 路由守卫（未登录/无租户/无权限） | Task 7 (PrivateRoute) |
| 成员列表 + 邀请 + 移除 | Task 8 (MembersPage) |
| 租户设置（名称/头像） | Task 9 (SettingsPage) |
| 全局管理员：所有租户列表 | Task 9 (TenantsListPage) |
| NavBar 用户 Dropdown | Task 10 + Task 11 |
| AuthContext.Provider 包裹全局 | Task 11 (App.jsx) |
| 现有路由加 PrivateRoute 保护 | Task 11 (App.jsx) |
