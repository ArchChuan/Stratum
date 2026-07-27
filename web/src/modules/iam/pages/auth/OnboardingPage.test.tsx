import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { tenantApi } from '../../api/tenant.api';
import { useAuth } from '../../components/AuthContext';

import { OnboardingPage } from './OnboardingPage';

const navigate = vi.fn();

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return { ...actual, useNavigate: () => navigate };
});
vi.mock('../../api/tenant.api', () => ({ tenantApi: { joinExisting: vi.fn() } }));
vi.mock('../../api/auth.api', () => ({ authApi: { register: vi.fn(), me: vi.fn(), createUserTenant: vi.fn() } }));
vi.mock('../../components/AuthContext', () => ({ useAuth: vi.fn() }));

describe('OnboardingPage existing user', () => {
  const switchTenant = vi.fn();

  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockReturnValue({
        matches: false,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    });
    vi.mocked(useAuth).mockReturnValue({
      user: {
        sub: 'member-1', tenant_id: 'tenant-source', role: 'member', avatar_url: '', github_login: 'member-1',
      },
      accessToken: 'in-memory-token', tokenRef: { current: 'in-memory-token' }, loading: false, tenants: [],
      login: vi.fn(), logout: vi.fn(), switchTenant, setAccessToken: vi.fn(),
    });
  });

  it('joins with the invitation then switches tenant', async () => {
    vi.mocked(tenantApi.joinExisting).mockResolvedValue({ tenant_id: 'tenant-target' });
    render(<MemoryRouter initialEntries={['/onboarding']}><OnboardingPage /></MemoryRouter>);

    fireEvent.click(screen.getByRole('tab', { name: '加入已有租户' }));
    fireEvent.change(screen.getByLabelText('邀请码'), { target: { value: 'one-time-code' } });
    fireEvent.click(screen.getByRole('button', { name: /加入租户/ }));

    await waitFor(() => expect(tenantApi.joinExisting).toHaveBeenCalledWith('one-time-code'));
    expect(switchTenant).toHaveBeenCalledWith('tenant-target');
    expect(navigate).toHaveBeenCalledWith('/', { replace: true });
  });
});
