import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { PrivateRoute } from './PrivateRoute';

const auth = vi.hoisted(() => ({ user: null as Record<string, unknown> | null, loading: true }));
vi.mock('./AuthContext', () => ({ useAuth: () => auth }));

const platformUser = (globalRole: string) => ({
  sub: 'u1',
  global_role: globalRole,
  current_tenant: { id: 't1', name: 't1' },
});

const renderRoute = (requiredRole?: string) =>
  render(
    <MemoryRouter>
      <PrivateRoute requiredRole={requiredRole}>
        <div>受保护内容</div>
      </PrivateRoute>
    </MemoryRouter>,
  );

describe('PrivateRoute', () => {
  let consoleError: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    auth.user = null;
    auth.loading = true;
    consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
  });

  afterEach(() => {
    consoleError.mockRestore();
  });

  it('renders an accessible loading state without an Ant Design Spin warning', () => {
    render(
      <MemoryRouter>
        <PrivateRoute>
          <div>受保护内容</div>
        </PrivateRoute>
      </MemoryRouter>,
    );

    expect(screen.getByRole('status', { name: '加载中...' })).toBeInTheDocument();
    expect(consoleError).not.toHaveBeenCalledWith(expect.stringContaining('[antd: Spin]'));
  });

  it('allows global_admin for requiredRole=system_admin', () => {
    auth.user = platformUser('global_admin');
    auth.loading = false;
    renderRoute('system_admin');
    expect(screen.getByText('受保护内容')).toBeInTheDocument();
  });

  it('allows global_admin for requiredRole=global_admin', () => {
    auth.user = platformUser('global_admin');
    auth.loading = false;
    renderRoute('global_admin');
    expect(screen.getByText('受保护内容')).toBeInTheDocument();
  });

  it('allows system_admin for requiredRole=system_admin', () => {
    auth.user = platformUser('system_admin');
    auth.loading = false;
    renderRoute('system_admin');
    expect(screen.getByText('受保护内容')).toBeInTheDocument();
  });

  it('denies system_admin for requiredRole=global_admin', () => {
    auth.user = platformUser('system_admin');
    auth.loading = false;
    renderRoute('global_admin');
    expect(screen.getByText('您没有访问此页面的权限。')).toBeInTheDocument();
  });

  it('denies plain user for requiredRole=system_admin', () => {
    auth.user = platformUser('user');
    auth.loading = false;
    renderRoute('system_admin');
    expect(screen.getByText('您没有访问此页面的权限。')).toBeInTheDocument();
  });

  it('treats missing global_role as plain user (fail closed)', () => {
    auth.user = { sub: 'u1', current_tenant: { id: 't1', name: 't1' } };
    auth.loading = false;
    renderRoute('system_admin');
    expect(screen.getByText('您没有访问此页面的权限。')).toBeInTheDocument();
  });
});
