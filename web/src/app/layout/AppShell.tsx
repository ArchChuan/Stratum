import { RobotOutlined, SwapOutlined, PlusCircleOutlined } from '@ant-design/icons';
import { Layout, Menu, Space, Badge, Dropdown, Modal, Form, Input, Button, message } from 'antd';
import { useEffect, useState, type ReactNode } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';

import { UserMenu } from './UserMenu';
import { buildMenuItems, resolveOpenKeys } from './menu.config';

import { useAuth, authApi } from '@/modules/iam';
import api from '@/services/client';
import { extractErrorMessage } from '@/shared/lib';

const { Header, Content, Sider } = Layout;

interface AppShellProps {
  children: ReactNode;
}

export const AppShell = ({ children }: AppShellProps) => {
  const [collapsed, setCollapsed] = useState(false);
  const [connected, setConnected] = useState(false);
  const [switchingTenant, setSwitchingTenant] = useState(false);
  const [createTenantOpen, setCreateTenantOpen] = useState(false);
  const [createTenantLoading, setCreateTenantLoading] = useState(false);
  const [createTenantForm] = Form.useForm<{ tenant_name: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const { user, tenants, switchTenant } = useAuth();

  useEffect(() => {
    api.get('/health')
      .then(() => setConnected(true))
      .catch(() => setConnected(false));
  }, []);

  const handleSwitchTenant = async (tenantId: string) => {
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

  const handleCreateTenant = async (values: { tenant_name: string }) => {
    setCreateTenantLoading(true);
    try {
      const res = await authApi.createUserTenant(values.tenant_name);
      await switchTenant(res.tenant_id);
      setCreateTenantOpen(false);
      createTenantForm.resetFields();
      navigate('/', { replace: true });
    } catch (err) {
      message.error(extractErrorMessage(err, '创建租户失败'));
    } finally {
      setCreateTenantLoading(false);
    }
  };

  const currentTenantId = user?.tenant_id;
  const currentTenant = tenants.find((t: any) => t.tenant_id === currentTenantId);
  const currentTenantName =
    currentTenant?.name || user?.current_tenant?.name || currentTenantId || '';

  const tenantMenuItems = [
    ...tenants.map((t: any) => ({
      key: `tenant-${t.tenant_id}`,
      icon: <SwapOutlined />,
      label:
        t.tenant_id === currentTenantId ? (
          <b>{(t.name || t.tenant_id) + '（当前）'}</b>
        ) : (
          t.name || t.tenant_id
        ),
      disabled: t.tenant_id === currentTenantId || switchingTenant,
      onClick: () => handleSwitchTenant(t.tenant_id),
    })),
    { type: 'divider' as const },
    {
      key: 'create-tenant',
      icon: <PlusCircleOutlined />,
      label: '创建新租户',
      onClick: () => setCreateTenantOpen(true),
    },
  ];

  const menuItems = buildMenuItems(user);
  const openKeys = resolveOpenKeys(location.pathname);

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
        <div
          style={{
            padding: collapsed ? '18px 8px' : '18px 20px',
            display: 'flex',
            alignItems: 'center',
            gap: 10,
            borderBottom: '1px solid rgba(255,255,255,0.06)',
            marginBottom: 4,
          }}
        >
          <div
            style={{
              width: 28,
              height: 28,
              borderRadius: 8,
              background: 'linear-gradient(135deg, #1677ff 0%, #722ed1 100%)',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              flexShrink: 0,
            }}
          >
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
        <Header
          style={{
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
          }}
        >
          <Space size={12}>
            <Badge
              status={connected ? 'success' : 'error'}
              text={
                <span style={{ fontSize: 13, color: '#595959' }}>
                  {connected ? '已连接' : '未连接'}
                </span>
              }
            />
            {currentTenantName && tenants.length > 0 && (
              <>
                <span style={{ color: '#e8e8e8' }}>|</span>
                <Dropdown
                  menu={{ items: tenantMenuItems }}
                  placement="bottomLeft"
                  trigger={['click']}
                >
                  <span
                    style={{
                      color: '#1677ff',
                      fontWeight: 500,
                      cursor: 'pointer',
                      fontSize: 13,
                      display: 'flex',
                      alignItems: 'center',
                      gap: 4,
                    }}
                  >
                    {currentTenantName}
                    <span style={{ fontSize: 10 }}>▾</span>
                  </span>
                </Dropdown>
              </>
            )}
          </Space>

          <UserMenu />
        </Header>

        <Content
          style={{
            margin: 0,
            background: '#f5f7fa',
            minHeight: 'calc(100vh - 56px)',
            padding: 24,
          }}
        >
          {children}
        </Content>
      </Layout>

      <Modal
        title="创建新租户"
        open={createTenantOpen}
        onCancel={() => {
          setCreateTenantOpen(false);
          createTenantForm.resetFields();
        }}
        footer={null}
        destroyOnHidden
      >
        <Form form={createTenantForm} layout="vertical" onFinish={handleCreateTenant}>
          <Form.Item
            label="租户名称"
            name="tenant_name"
            rules={[{ required: true, message: '请输入租户名称' }]}
          >
            <Input placeholder="例如：我的团队" maxLength={64} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" htmlType="submit" block loading={createTenantLoading}>
              创建
            </Button>
          </Form.Item>
        </Form>
      </Modal>
    </Layout>
  );
};

export default AppShell;
