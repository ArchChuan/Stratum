import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { User } from '../model/auth';

import { AuthProvider, useAuth } from './AuthContext';

vi.mock('@/services/client', () => ({
  default: {
    post: vi.fn().mockRejectedValue(new Error('no session')),
  },
  setupApiInterceptors: vi.fn(),
  markAuthReady: vi.fn(),
}));

vi.mock('../api/auth.api', () => ({
  authApi: {
    me: vi.fn(),
    logout: vi.fn().mockResolvedValue(undefined),
  },
}));

vi.mock('../api/tenant.api', () => ({
  tenantApi: {
    settings: vi.fn().mockResolvedValue({ tenant_name: '我的团队' }),
    listMine: vi.fn().mockResolvedValue([]),
  },
}));

function Probe() {
  const { user, login } = useAuth();
  return (
    <div>
      <button
        type="button"
        onClick={() =>
          login(
            {
              sub: 'u1',
              tenant_id: 'tenant-created',
              role: 'owner',
              avatar_url: '',
              github_login: 'tester',
              username: 'tester',
            } as User,
            'access-token',
          )
        }
      >
        登录
      </button>
      <div data-testid="current-tenant">{user?.current_tenant?.id ?? 'none'}</div>
    </div>
  );
}

describe('AuthContext login', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('sets current_tenant synchronously from tenant_id so ProtectedRoute never redirects to onboarding', () => {
    render(
      <AuthProvider>
        <Probe />
      </AuthProvider>,
    );

    fireEvent.click(screen.getByRole('button', { name: '登录' }));

    // The async tenant-name refresh may still be in flight; the tenant context
    // must already be usable on the very next render.
    expect(screen.getByTestId('current-tenant')).toHaveTextContent('tenant-created');
  });
});
