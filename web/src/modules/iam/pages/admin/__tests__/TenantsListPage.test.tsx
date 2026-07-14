import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeAll, expect, it, vi } from 'vitest';

import { TenantsListPage } from '../TenantsListPage';

const api = vi.hoisted(() => ({
  listAllTenants: vi.fn(),
  setTenantEnabled: vi.fn(),
  adminDeleteTenant: vi.fn(),
}));

vi.mock('@/shared/hooks', () => ({ useResponsive: () => ({ isMobile: true }) }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => ({}) }));
vi.mock('@/modules/iam/api/tenant.api', () => ({ tenantApi: api }));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});

it('renders admin tenant details and confirms destructive mobile action', async () => {
  api.listAllTenants.mockResolvedValue([{
    id: 'tenant-2',
    name: '产品团队',
    slug: 'product',
    status: 'active',
    member_count: 8,
    created_at: '2026-07-10T00:00:00Z',
    is_default: false,
  }]);
  api.adminDeleteTenant.mockResolvedValue(undefined);
  render(<TenantsListPage />);

  expect(await screen.findByText('产品团队')).toBeInTheDocument();
  expect(screen.getByText('tenant-2')).toBeInTheDocument();
  expect(screen.getByText('启用')).toBeInTheDocument();
  expect(screen.getByText('8 位成员')).toBeInTheDocument();

  fireEvent.click(screen.getByRole('button', { name: '删除租户' }));
  const confirmation = await screen.findByText(/确认删除租户/);
  const popover = confirmation.closest('.ant-popover');
  fireEvent.click(within(popover as HTMLElement).getByRole('button', { name: '确认删除' }));

  await waitFor(() => expect(api.adminDeleteTenant).toHaveBeenCalledWith('tenant-2'));
  expect(document.querySelector('.ant-table')).not.toBeInTheDocument();
});
