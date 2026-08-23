import { render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { AppRouter } from './router';

import { useAuth } from '@/modules/iam';

vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<typeof import('react-router-dom')>();
  return {
    ...actual,
    useLocation: () => ({ pathname: '/onboarding' }),
    Navigate: ({ to }: { to: string }) => <div data-testid="navigate-to">{to}</div>,
    Routes: ({ children }: { children: ReactNode }) => <>{children}</>,
  };
});

vi.mock('@/app/layout/AppShell', () => ({
  AppShell: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

vi.mock('@/modules/iam', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/modules/iam')>();
  return {
    ...actual,
    useAuth: vi.fn(),
    iamPublicRoutes: [<div key="onboarding">onboarding-page</div>],
    iamPrivateRoutes: [],
  };
});

describe('AppRouter onboarding exit', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('redirects a tenant-holding user away from /onboarding instead of stranding them', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: {
        sub: 'u1',
        tenant_id: 'tenant-1',
        role: 'owner',
        current_tenant: { id: 'tenant-1', name: '我的租户', role: 'owner' },
        avatar_url: '',
        github_login: 'tester',
        username: 'tester',
      },
      loading: false,
    } as never);

    render(<AppRouter />);

    expect(screen.getByTestId('navigate-to')).toHaveTextContent('/');
    expect(screen.queryByText('onboarding-page')).not.toBeInTheDocument();
  });

  it('keeps showing onboarding only for an unauthenticated onboarding-token session', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: null,
      loading: false,
    } as never);

    render(<AppRouter />);

    expect(screen.getByText('onboarding-page')).toBeInTheDocument();
    expect(screen.queryByTestId('navigate-to')).not.toBeInTheDocument();
  });
});
