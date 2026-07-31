import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { authApi } from '../../api/auth.api';
import { useAuth } from '../../components/AuthContext';

import { LoginPage } from './LoginPage';

const navigate = vi.fn();

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, useNavigate: () => navigate };
});

vi.mock('../../api/auth.api', () => ({
  authApi: {
    guest: vi.fn(),
    me: vi.fn(),
  },
}));

vi.mock('../../components/AuthContext', () => ({
  useAuth: vi.fn(),
}));

describe('LoginPage guest login', () => {
  const login = vi.fn();

  beforeEach(() => {
    login.mockReset();
    navigate.mockReset();
    vi.mocked(authApi.guest).mockReset();
    vi.mocked(authApi.me).mockReset();
    vi.mocked(useAuth).mockReturnValue({
      user: null,
      accessToken: null,
      tokenRef: { current: null },
      loading: false,
      tenants: [],
      login,
      logout: vi.fn(),
      switchTenant: vi.fn(),
      setAccessToken: vi.fn(),
    });
  });

  it('logs in from the guest response without a follow-up me request', async () => {
    vi.mocked(authApi.guest).mockResolvedValue({
      access_token: 'guest-token',
      tenant_id: 'tenant-default',
      user: {
        sub: 'guest-1',
        tenant_id: 'tenant-default',
        role: 'member',
        global_role: '',
        system_role: 'user',
        avatar_url: '',
        github_login: 'guest-demo',
      },
    } as Awaited<ReturnType<typeof authApi.guest>>);

    render(<MemoryRouter><LoginPage /></MemoryRouter>);
    // Switch to the third-party tab — guest button is on the second tab.
    fireEvent.click(screen.getByText('第三方登录'));
    fireEvent.click(screen.getByRole('button', { name: /快速体验/ }));

    await waitFor(() => expect(login).toHaveBeenCalled());
    expect(authApi.me).not.toHaveBeenCalled();
    expect(login).toHaveBeenCalledWith(
      expect.objectContaining({
        sub: 'guest-1',
        tenant_id: 'tenant-default',
        github_login: 'guest-demo',
      }),
      'guest-token',
    );
    expect(navigate).toHaveBeenCalledWith('/', { replace: true });
  });
});
