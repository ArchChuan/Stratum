import React, { useState, useEffect } from 'react';
import { Layout, Menu, Space, Typography, Dropdown, Avatar, message, Modal, Form, Input, Button, Badge } from 'antd';
import {
  AppstoreOutlined, PlusCircleOutlined, HistoryOutlined, DashboardOutlined,
  RobotOutlined, CommentOutlined, DatabaseOutlined, UserOutlined, LogoutOutlined,
  TeamOutlined, SettingOutlined, GlobalOutlined, SwapOutlined, ApiOutlined, BookOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';
import { Routes, Route, useNavigate, Link, useLocation } from 'react-router-dom';

import SkillsListPage from './pages/SkillsListPage';
import CreateSkillPage from './pages/CreateSkillPage';
import ExecutionHistoryPage from './pages/ExecutionHistoryPage';
import DashboardPage from './pages/DashboardPage';
import AgentsListPage from './pages/AgentsListPage';
import CreateAgentPage from './pages/CreateAgentPage';
import EditAgentPage from './pages/EditAgentPage';
import AgentChatPage from './pages/AgentChatPage';
import MemoryPage from './pages/MemoryPage';
import MCPServersPage from './pages/MCPServersPage';
import LoginPage from './pages/auth/LoginPage';
import CallbackPage from './pages/auth/CallbackPage';
import OnboardingPage from './pages/auth/OnboardingPage';
import MembersPage from './pages/tenant/MembersPage';
import SettingsPage from './pages/tenant/SettingsPage';
import TenantsListPage from './pages/admin/TenantsListPage';
import KnowledgePage from './pages/KnowledgePage';
import KnowledgeDetailPage from './pages/KnowledgeDetailPage';
import PrivateRoute from './components/PrivateRoute';
import { AuthProvider } from './contexts/AuthContext';
import { useAuth } from './hooks/useAuth';
import { setupApiInterceptors, checkHealth, createUserTenant } from './services/api';

const { Header, Content, Sider } = Layout;

const AppInner = () => {
  const [collapsed, setCollapsed] = useState(false);
  const [connected, setConnected] = useState(false);
  const [switchingTenant, setSwitchingTenant] = useState(false);
  const [createTenantOpen, setCreateTenantOpen] = useState(false);
  const [createTenantLoading, setCreateTenantLoading] = useState(false);
  const [createTenantForm] = Form.useForm();
  const navigate = useNavigate();
  const location = useLocation();
  const { user, logout, tokenRef, tenants, switchTenant } = useAuth();

  useEffect(() => {
    setupApiInterceptors(tokenRef, () => { logout(); navigate('/login', { replace: true }); });
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    checkHealth().then(() => setConnected(true)).catch(() => setConnected(false));
  }, []);

  const isAuthPage = ['/login', '/auth/callback', '/onboarding'].some(p => location.pathname.startsWith(p));

  if (isAuthPage) {
    return (
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/auth/callback" element={<CallbackPage />} />
        <Route path="/onboarding" element={<OnboardingPage />} />
      </Routes>
    );
  }

  const handleSwitchTenant = async (tenantId) => {
    if (tenantId === user?.tenant_id) return;
    setSwitchingTenant(true);
    try {
      await switchTenant(tenantId);
      navigate('/', { replace: true });
    } catch {
      message.error('切换租户失败');
    } finally {
      setSwitchingTenant(false);
    }
  };

  const handleCreateTenant = async (values) => {
    setCreateTenantLoading(true);
    try {
      const res = await createUserTenant(values.tenant_name);
      const newToken = res.data.access_token;
      await switchTenant(res.data.tenant_id);
      setCreateTenantOpen(false);
      createTenantForm.resetFields();
      localStorage.setItem('access_token', newToken);
      navigate('/', { replace: true });
    } catch (err) {
      message.error(err.response?.data?.error || '创建租户失败');
    } finally {
      setCreateTenantLoading(false);
    }
  };

  const tenantMenuItems = [
    ...tenants.map(t => ({
      key: `tenant-${t.tenant_id}`,
      icon: <SwapOutlined />,
      label: t.tenant_id === user?.tenant_id ? <b>{t.name}（当前）</b> : t.name,
      disabled: t.tenant_id === user?.tenant_id || switchingTenant,
      onClick: () => handleSwitchTenant(t.tenant_id),
    })),
    { type: 'divider' },
    {
      key: 'create-tenant',
      icon: <PlusCircleOutlined />,
      label: '创建新租户',
      onClick: () => setCreateTenantOpen(true),
    },
  ];

  const menuItems = [
    {
      key: '/',
      icon: <DashboardOutlined />,
      label: <Link to="/">概览</Link>,
    },
    {
      key: 'agent-group',
      icon: <RobotOutlined />,
      label: 'Agent',
      children: [
        { key: '/agents', icon: <RobotOutlined />, label: <Link to="/agents">Agent 列表</Link> },
        { key: '/agents/create', icon: <PlusCircleOutlined />, label: <Link to="/agents/create">创建 Agent</Link> },
        { key: '/chat', icon: <CommentOutlined />, label: <Link to="/chat">Agent 对话</Link> },
        { key: '/history', icon: <HistoryOutlined />, label: <Link to="/history">执行历史</Link> },
      ],
    },
    {
      key: 'skill-group',
      icon: <ThunderboltOutlined />,
      label: '技能',
      children: [
        { key: '/skills', icon: <AppstoreOutlined />, label: <Link to="/skills">技能列表</Link> },
        { key: '/skills/create', icon: <PlusCircleOutlined />, label: <Link to="/skills/create">创建技能</Link> },
      ],
    },
    {
      key: 'knowledge-group',
      icon: <BookOutlined />,
      label: '知识与记忆',
      children: [
        { key: '/knowledge', icon: <BookOutlined />, label: <Link to="/knowledge">知识库</Link> },
        { key: '/memory', icon: <DatabaseOutlined />, label: <Link to="/memory">记忆管理</Link> },
      ],
    },
    {
      key: '/mcp',
      icon: <ApiOutlined />,
      label: <Link to="/mcp">MCP 服务器</Link>,
    },
    ...(user?.current_tenant ? [
      {
        key: 'tenant-group',
        icon: <TeamOutlined />,
        label: '团队',
        children: [
          { key: '/tenant/members', icon: <TeamOutlined />, label: <Link to="/tenant/members">成员管理</Link> },
          { key: '/tenant/settings', icon: <SettingOutlined />, label: <Link to="/tenant/settings">租户设置</Link> },
        ],
      },
    ] : []),
    ...(user?.global_role === 'global_admin' ? [
      { key: '/admin/tenants', icon: <GlobalOutlined />, label: <Link to="/admin/tenants">全局租户</Link> },
    ] : []),
  ];

  const userMenuItems = [
    { key: 'logout', icon: <LogoutOutlined />, label: '退出登录', danger: true, onClick: () => { logout(); navigate('/login', { replace: true }); } },
  ];

  // Determine which submenu keys should be open by default based on current path
  const openKeys = ['/agents', '/agents/create', '/chat', '/history'].some(p => location.pathname.startsWith(p))
    ? ['agent-group']
    : ['/skills', '/skills/create'].some(p => location.pathname.startsWith(p))
      ? ['skill-group']
      : ['/knowledge', '/memory'].some(p => location.pathname.startsWith(p))
        ? ['knowledge-group']
        : ['/tenant'].some(p => location.pathname.startsWith(p))
          ? ['tenant-group']
          : [];

  return (
    <Layout style={{ minHeight: '100vh' }}>
      <Sider
        collapsible
        collapsed={collapsed}
        onCollapse={setCollapsed}
        width={220}
        style={{
          overflow: 'auto',
          height: '100vh',
          position: 'fixed',
          left: 0,
          top: 0,
          bottom: 0,
          background: '#141414',
        }}
      >
        <div style={{
          padding: collapsed ? '18px 8px' : '18px 20px',
          display: 'flex',
          alignItems: 'center',
          gap: 10,
          borderBottom: '1px solid rgba(255,255,255,0.06)',
          marginBottom: 4,
        }}>
          <div style={{
            width: 28, height: 28, borderRadius: 8,
            background: 'linear-gradient(135deg, #1677ff 0%, #722ed1 100%)',
            display: 'flex', alignItems: 'center', justifyContent: 'center',
            flexShrink: 0,
          }}>
            <RobotOutlined style={{ color: '#fff', fontSize: 14 }} />
          </div>
          {!collapsed && (
            <span style={{ color: '#fff', fontWeight: 600, fontSize: 15, whiteSpace: 'nowrap' }}>
              Stratum AI
            </span>
          )}
        </div>

        <Menu
          theme="dark"
          selectedKeys={[location.pathname]}
          defaultOpenKeys={openKeys}
          mode="inline"
          items={menuItems}
          style={{ background: '#141414', borderRight: 0 }}
          onClick={({ key }) => {
            if (!key.endsWith('-group')) navigate(key);
          }}
        />
      </Sider>

      <Layout style={{ marginLeft: collapsed ? 80 : 220, transition: 'margin-left 0.2s' }}>
        <Header style={{
          padding: '0 24px',
          background: '#fff',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          borderBottom: '1px solid #f0f0f0',
          height: 56,
          lineHeight: '56px',
          position: 'sticky',
          top: 0,
          zIndex: 100,
          boxShadow: '0 1px 4px rgba(0,0,0,0.05)',
        }}>
          <Space size={12}>
            <Badge
              status={connected ? 'success' : 'error'}
              text={
                <span style={{ fontSize: 13, color: '#595959' }}>
                  {connected ? '已连接' : '未连接'}
                </span>
              }
            />
            {user?.current_tenant?.name && tenants.length > 0 && (
              <>
                <span style={{ color: '#e8e8e8' }}>|</span>
                <Dropdown menu={{ items: tenantMenuItems }} placement="bottomLeft" trigger={['click']}>
                  <span style={{
                    color: '#1677ff', fontWeight: 500, cursor: 'pointer',
                    fontSize: 13, display: 'flex', alignItems: 'center', gap: 4,
                  }}>
                    {user.current_tenant.name}
                    <span style={{ fontSize: 10 }}>▾</span>
                  </span>
                </Dropdown>
              </>
            )}
          </Space>

          {user ? (
            <Dropdown menu={{ items: userMenuItems }} placement="bottomRight">
              <Space style={{ cursor: 'pointer' }} size={8}>
                <Avatar
                  src={user.avatar_url}
                  icon={<UserOutlined />}
                  size={28}
                  style={{ background: '#1677ff' }}
                />
                <span style={{ fontSize: 13, color: '#262626' }}>{user.github_login}</span>
              </Space>
            </Dropdown>
          ) : (
            <Link to="/login">登录</Link>
          )}
        </Header>

        <Content style={{ margin: 0, background: '#f5f7fa', minHeight: 'calc(100vh - 56px)', padding: 24 }}>
          <Routes key={user?.tenant_id || 'no-tenant'}>
            <Route path="/" element={<PrivateRoute><DashboardPage /></PrivateRoute>} />
            <Route path="/skills" element={<PrivateRoute><SkillsListPage /></PrivateRoute>} />
            <Route path="/skills/create" element={<PrivateRoute><CreateSkillPage /></PrivateRoute>} />
            <Route path="/agents" element={<PrivateRoute><AgentsListPage /></PrivateRoute>} />
            <Route path="/agents/create" element={<PrivateRoute><CreateAgentPage /></PrivateRoute>} />
            <Route path="/agents/:id/edit" element={<PrivateRoute><EditAgentPage /></PrivateRoute>} />
            <Route path="/chat" element={<PrivateRoute><AgentChatPage /></PrivateRoute>} />
            <Route path="/memory" element={<PrivateRoute><MemoryPage /></PrivateRoute>} />
            <Route path="/mcp" element={<PrivateRoute><MCPServersPage /></PrivateRoute>} />
            <Route path="/knowledge" element={<PrivateRoute><KnowledgePage /></PrivateRoute>} />
            <Route path="/knowledge/:name" element={<PrivateRoute><KnowledgeDetailPage /></PrivateRoute>} />
            <Route path="/history" element={<PrivateRoute><ExecutionHistoryPage /></PrivateRoute>} />
            <Route path="/tenant/members" element={<PrivateRoute><MembersPage /></PrivateRoute>} />
            <Route path="/tenant/settings" element={<PrivateRoute><SettingsPage /></PrivateRoute>} />
            <Route path="/admin/tenants" element={<PrivateRoute requiredRole="global_admin"><TenantsListPage /></PrivateRoute>} />
            <Route path="/login" element={<LoginPage />} />
            <Route path="/auth/callback" element={<CallbackPage />} />
            <Route path="/onboarding" element={<OnboardingPage />} />
          </Routes>
        </Content>
      </Layout>

      <Modal
        title="创建新租户"
        open={createTenantOpen}
        onCancel={() => { setCreateTenantOpen(false); createTenantForm.resetFields(); }}
        footer={null}
        destroyOnClose
      >
        <Form form={createTenantForm} layout="vertical" onFinish={handleCreateTenant}>
          <Form.Item label="租户名称" name="tenant_name" rules={[{ required: true, message: '请输入租户名称' }]}>
            <Input placeholder="例如：我的团队" maxLength={64} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={createTenantLoading}>创建</Button>
          </Form.Item>
        </Form>
      </Modal>
    </Layout>
  );
};

const App = () => (
  <AuthProvider>
    <AppInner />
  </AuthProvider>
);

export default App;
