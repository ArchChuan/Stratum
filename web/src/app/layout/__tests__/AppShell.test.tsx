import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';

import { AppShell } from '../AppShell';

const responsive = vi.hoisted(() => ({ isMobile: false, isCompact: false }));

vi.mock('@/shared/hooks', () => ({
  useResponsive: () => responsive,
}));

vi.mock('@/modules/iam', () => ({
  authApi: { createUserTenant: vi.fn() },
  useAuth: () => ({
    user: { tenant_id: 'tenant-1', github_login: 'tester' },
    tenants: [{ tenant_id: 'tenant-1', name: '测试团队' }],
    switchTenant: vi.fn(),
  }),
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
    future={{ v7_relativeSplatPath: true, v7_startTransition: true }}
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
    fireEvent.click(screen.getByRole('link', { name: /Agent 对话/ }));

    expect(screen.getByRole('status', { name: '当前路径' })).toHaveTextContent('/chat');
    await waitFor(() => expect(drawer).not.toBeVisible());
  });

  it('keeps the fixed sider navigation on desktop', async () => {
    const { container } = renderShell();

    await waitFor(() => expect(container.querySelector('.ant-layout-sider')).toBeInTheDocument());
    expect(screen.getByRole('navigation', { name: '主导航' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '打开主导航' })).toBeNull();
  });
});
