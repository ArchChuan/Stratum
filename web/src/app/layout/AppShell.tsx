import {
  CloseOutlined,
  MenuOutlined,
  PlusCircleOutlined,
  RobotOutlined,
  SwapOutlined,
} from '@ant-design/icons';
import {
  Badge,
  Button,
  Drawer,
  Dropdown,
  Form,
  Input,
  Layout,
  Menu,
  Modal,
  Space,
  message,
} from 'antd';
import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { useNavigate, useLocation } from 'react-router-dom';

import { UserMenu } from './UserMenu';
import { buildMenuItems, resolveOpenKeys } from './menu.config';

import { useAuth, authApi } from '@/modules/iam';
import api from '@/services/client';
import { useResponsive } from '@/shared/hooks';
import { extractErrorMessage, isForbidden } from '@/shared/lib';

const { Header, Content, Sider } = Layout;

interface AppShellProps {
  children: ReactNode;
}

interface NavigationContentProps {
  collapsed?: boolean;
  menuItems: ReturnType<typeof buildMenuItems>;
  pathname: string;
  onSelect: (key: string) => void;
}

/**
 * memo + 字符串 label:让菜单树的渲染成本在路由切换时归零。
 * 根因(实测):每次切换 antd Menu 对 26 个 <Link> label 全量 reconcile,
 * 主线程阻塞 50-80ms,合成器保留旧帧产生残影/慢。
 * 修复:label 改为字符串(menu.config),菜单 reconcile 降至 ~0ms;
 * selectedKeys 受控高亮,前进/回退/直达 URL 均正确。
 * openKeys 只在挂载时确定:展开状态是用户持久控制,路径变化不应重置。
 */
const NavigationContent = memo(
  function NavigationContentInner({
    collapsed = false,
    menuItems,
    pathname,
    onSelect,
  }: NavigationContentProps) {
    const [openKeys] = useState(() => resolveOpenKeys(pathname));
    return (
      <nav
        aria-label="主导航"
        style={{ height: '100%', display: 'flex', flexDirection: 'column', overflow: 'hidden' }}
      >
    <div
      style={{
        padding: collapsed ? '18px 8px' : '18px 20px',
        display: 'flex',
        alignItems: 'center',
        gap: 10,
        borderBottom: '1px solid rgba(255,255,255,0.06)',
        marginBottom: 4,
        flexShrink: 0,
      }}
    >
      <div
        style={{
          width: 28,
          height: 28,
          borderRadius: 8,
          background: 'linear-gradient(135deg, #2563eb 0%, #722ed1 100%)',
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

        <div style={{ flex: 1, overflowY: 'auto', overflowX: 'hidden' }}>
          <Menu
            theme="dark"
            selectedKeys={[pathname]}
            defaultOpenKeys={openKeys}
            mode="inline"
            items={menuItems}
            style={{ background: '#141414', borderRight: 0 }}
            onClick={({ key }) => onSelect(key)}
          />
        </div>
      </nav>
    );
  },
);

export const AppShell = ({ children }: AppShellProps) => {
  const [collapsed, setCollapsed] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [connected, setConnected] = useState(false);
  const [switchingTenant, setSwitchingTenant] = useState(false);
  const [createTenantOpen, setCreateTenantOpen] = useState(false);
  const [createTenantLoading, setCreateTenantLoading] = useState(false);
  const [createTenantForm] = Form.useForm<{ tenant_name: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const { user, tenants, switchTenant } = useAuth();
  const { isMobile } = useResponsive();
  const contentRef = useRef<HTMLDivElement>(null);
  const prevPathRef = useRef(location.pathname);

  // 路由切换兜底:强制 LayerTree 更新,触发 Windows DComp 合成器旧层纹理回收。
  // 双 rAF 是关键:单 rAF 时 set 与 clear 在同一渲染帧内发生,transform 从未被
  // 合成器提交;双 rAF 让 translateZ(0) 存活完整一帧(层创建)再移除(层销毁),
  // 合成器完成一次层树重建,回收幽灵纹理。配合 index.css 的 isolation: isolate。
  useEffect(() => {
    if (prevPathRef.current === location.pathname) return;
    prevPathRef.current = location.pathname;
    const el = contentRef.current;
    if (!el) return;
    requestAnimationFrame(() => {
      el.style.transform = 'translateZ(0)';
      requestAnimationFrame(() => {
        el.style.transform = '';
      });
    });
  }, [location.pathname]);

  useEffect(() => {
    let cancelled = false;
    api.get('/health')
      .then(() => {
        if (!cancelled) setConnected(true);
      })
      .catch(() => {
        if (!cancelled) setConnected(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!isMobile) setMobileNavOpen(false);
  }, [isMobile]);

  const handleSwitchTenant = async (tenantId: string) => {
    if (tenantId === user?.tenant_id) return;
    setSwitchingTenant(true);
    try {
      await switchTenant(tenantId);
      navigate('/', { replace: true });
    } catch (err) {
      if (!isForbidden(err)) {
        message.error({ content: '切换租户失败', duration: 0 });
      }
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
      if (!isForbidden(err)) {
        message.error({ content: extractErrorMessage(err, '创建租户失败'), duration: 0 });
      }
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

  // menuItems 只在 user 变化时重建:路由切换时保持稳定引用,避免 antd Menu 全量 reconcile
  const menuItems = useMemo(() => buildMenuItems(user), [user]);
  const handleNavigation = useCallback(
    (key: string) => {
      if (key.endsWith('-group')) return;
      setMobileNavOpen(false);
      navigate(key);
    },
    [navigate],
  );

  return (
    <Layout style={{ minHeight: '100vh' }}>
      {!isMobile && (
        <Sider
          collapsible
          collapsed={collapsed}
          onCollapse={setCollapsed}
          width={220}
          style={{
            height: '100vh',
            position: 'fixed',
            left: 0,
            top: 0,
            bottom: 0,
            background: '#141414',
          }}
        >
          <NavigationContent
            collapsed={collapsed}
            menuItems={menuItems}
            pathname={location.pathname}
            onSelect={handleNavigation}
          />
        </Sider>
      )}

      <Layout
        style={{ marginLeft: isMobile ? 0 : collapsed ? 80 : 220, transition: 'margin-left 0.2s' }}
      >
        <Header
          style={{
            padding: isMobile ? '0 12px' : '0 24px',
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
          <Space size={12} style={{ minWidth: 0, flex: 1, overflow: 'hidden' }}>
            {isMobile && (
              <Button
                type="text"
                icon={<MenuOutlined />}
                aria-label="打开主导航"
                onClick={() => setMobileNavOpen(true)}
              />
            )}
            <span
              role="status"
              aria-label={connected ? '服务已连接' : '服务未连接'}
              style={{ display: 'inline-flex' }}
            >
              <Badge
                status={connected ? 'success' : 'error'}
                text={!isMobile && (
                  <span style={{ fontSize: 13, color: '#595959' }}>
                    {connected ? '已连接' : '未连接'}
                  </span>
                )}
              />
            </span>
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
                      color: '#2563eb',
                      fontWeight: 500,
                      cursor: 'pointer',
                      fontSize: 13,
                      display: 'flex',
                      alignItems: 'center',
                      gap: 4,
                      minWidth: 0,
                      maxWidth: isMobile ? 'calc(100vw - 176px)' : undefined,
                    }}
                  >
                    <span
                      title={currentTenantName}
                      aria-label={`当前租户：${currentTenantName}`}
                      style={{
                        maxWidth: isMobile ? 'calc(100vw - 176px)' : undefined,
                        overflow: isMobile ? 'hidden' : undefined,
                        textOverflow: isMobile ? 'ellipsis' : undefined,
                        whiteSpace: 'nowrap',
                      }}
                    >
                      {currentTenantName}
                    </span>
                    <span aria-hidden="true" style={{ fontSize: 10, flexShrink: 0 }}>▾</span>
                  </span>
                </Dropdown>
              </>
            )}
          </Space>

          <UserMenu />
        </Header>

        <Content
          ref={contentRef}
          className="app-shell-content"
          style={{
            margin: 0,
            background: '#f5f7fa',
            minHeight: 'calc(100vh - 56px)',
          }}
        >
          {children}
        </Content>
      </Layout>

      <Drawer
        title="主导航"
        placement="left"
        open={isMobile && mobileNavOpen}
        onClose={() => setMobileNavOpen(false)}
        width="min(84vw, 320px)"
        closeIcon={<CloseOutlined aria-label="关闭主导航" />}
        styles={{ body: { padding: 0, background: '#141414' } }}
      >
        <NavigationContent
          menuItems={menuItems}
          pathname={location.pathname}
          onSelect={handleNavigation}
        />
      </Drawer>

      <Modal
        title="创建新租户"
        open={createTenantOpen}
        onCancel={() => {
          setCreateTenantOpen(false);
          createTenantForm.resetFields();
        }}
        footer={null}
        destroyOnHidden
        width={isMobile ? 'calc(100vw - 24px)' : 520}
        centered={isMobile}
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
