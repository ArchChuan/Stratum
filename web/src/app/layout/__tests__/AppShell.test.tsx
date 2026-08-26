import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { AppShell } from '../AppShell';

const responsive = vi.hoisted(() => ({ isMobile: false, isCompact: false }));
const authState = vi.hoisted(() => ({ tenantName: '测试团队' }));

vi.mock('@/shared/hooks', () => ({
  useResponsive: () => responsive,
}));

vi.mock('@/modules/iam', () => ({
  authApi: { createUserTenant: vi.fn() },
  useAuth: () => ({
    user: { tenant_id: 'tenant-1', github_login: 'tester' },
    tenants: [{ tenant_id: 'tenant-1', name: authState.tenantName }],
    switchTenant: vi.fn(),
  }),
  // ApprovalNotificationBell 经 approvals barrel 间接引入 llmRoutes → PrivateRoute，
  // mock 边界需提供该导出（与 evaluation/routes.test.tsx 同款）。
  PrivateRoute: ({ children }: { children: React.ReactNode }) => children,
  // ApprovalNotificationBell 的 useApprovalNotifications 用 useTenantRole().isAdmin
  // 决定拉取数据源；AppShell 布局测试不关注铃铛内容，member 分支即可。
  useTenantRole: () => ({ isAdmin: false }),
}));

vi.mock('../UserMenu', () => ({ UserMenu: () => <div>用户菜单</div> }));

vi.mock('@/services/client', () => ({
  default: { get: vi.fn(() => new Promise(() => {})) },
}));

beforeAll(() => {
  const getComputedStyle = window.getComputedStyle.bind(window);
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
});

const CurrentPath = () => {
  const location = useLocation();
  return <output aria-label="当前路径">{location.pathname}</output>;
};

const renderShell = (initialPath = '/') => render(
  <MemoryRouter
    initialEntries={[initialPath]}
  >
    <AppShell>
      <CurrentPath />
    </AppShell>
  </MemoryRouter>,
);

describe('AppShell responsive navigation', () => {
  beforeEach(() => {
    responsive.isMobile = false;
    responsive.isCompact = false;
    authState.tenantName = '测试团队';
  });

  it('shows a labelled navigation button and drawer instead of a fixed sider on mobile', async () => {
    responsive.isMobile = true;
    const { container } = renderShell();

    expect(screen.getByRole('button', { name: '打开主导航' })).toBeInTheDocument();
    expect(container.querySelector('.ant-layout-sider')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: '打开主导航' }));

    expect(await screen.findByRole('dialog', { name: '主导航' })).toBeInTheDocument();
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument();
  });

  it('closes the mobile drawer after selecting a route', async () => {
    responsive.isMobile = true;
    renderShell('/');

    fireEvent.click(screen.getByRole('button', { name: '打开主导航' }));
    const drawer = await screen.findByRole('dialog', { name: '主导航' });
    // label 为字符串后菜单项由 antd 渲染为 menuitem,不再是 <Link>
    fireEvent.click(screen.getByRole('menuitem', { name: /Agent 对话/ }));

    expect(screen.getByRole('status', { name: '当前路径' })).toHaveTextContent('/chat');
    await waitFor(() => expect(drawer).not.toBeVisible());
  });

  it('keeps the fixed sider navigation on desktop', async () => {
    const { container } = renderShell();

    await waitFor(() => expect(container.querySelector('.ant-layout-sider')).toBeInTheDocument());
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '打开主导航' })).toBeNull();
  });

  it('closes the mobile drawer when the viewport changes to desktop', async () => {
    responsive.isMobile = true;
    const view = renderShell();
    fireEvent.click(screen.getByRole('button', { name: '打开主导航' }));
    const drawer = await screen.findByRole('dialog', { name: '主导航' });

    responsive.isMobile = false;
    view.rerender(
      <MemoryRouter>
        <AppShell>
          <CurrentPath />
        </AppShell>
      </MemoryRouter>,
    );

    await waitFor(() => expect(drawer).not.toBeVisible());
    expect(view.container.querySelector('.ant-layout-sider')).toBeInTheDocument();
  });

  it('constrains a long tenant name on mobile while exposing its full value', () => {
    responsive.isMobile = true;
    authState.tenantName = '很长的团队名称'.repeat(10).slice(0, 64);
    renderShell();

    const tenantName = screen.getByTitle(authState.tenantName);
    expect(tenantName).toHaveAccessibleName(`当前租户：${authState.tenantName}`);
    expect(tenantName).toHaveStyle({
      maxWidth: 'calc(100vw - 176px)',
      overflow: 'hidden',
      textOverflow: 'ellipsis',
      whiteSpace: 'nowrap',
    });
  });
});
