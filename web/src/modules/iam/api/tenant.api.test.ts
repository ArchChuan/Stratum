import { beforeEach, describe, expect, it, vi } from 'vitest';

import { tenantApi } from './tenant.api';

import api from '@/services/client';

vi.mock('@/services/client', () => ({
  default: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

describe('tenantApi.members', () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
  });

  it('requests and returns a server-side page', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        members: [
          {
            user_id: 'user-21',
            github_login: 'member-21',
            avatar_url: '',
            role: 'member',
            joined_at: '2026-07-14T00:00:00Z',
          },
        ],
        total: 25,
        page: 2,
        page_size: 20,
      },
    });

    await expect(tenantApi.members(2, 20)).resolves.toMatchObject({
      total: 25,
      page: 2,
      page_size: 20,
      members: [{ user_id: 'user-21' }],
    });
    expect(api.get).toHaveBeenCalledWith('/tenant/members', {
      params: { page: 2, page_size: 20 },
    });
  });
});

describe('tenantApi.listAllTenants', () => {
  it('requests and returns a server-side administrator page', async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: {
        tenants: [{ id: 'tenant-21', name: '第二页租户', slug: 'tenant-21', status: 'active' }],
        total: 25,
        page: 2,
        page_size: 20,
      },
    });

    await expect(tenantApi.listAllTenants(2, 20)).resolves.toMatchObject({
      total: 25,
      page: 2,
      page_size: 20,
      tenants: [{ id: 'tenant-21' }],
    });
    expect(api.get).toHaveBeenCalledWith('/admin/tenants', {
      params: { page: 2, page_size: 20 },
    });
  });
});

describe('tenantApi.createTenant', () => {
  it('posts every administrator-controlled tenant field', async () => {
    vi.mocked(api.post).mockResolvedValue({ data: { id: 'tenant-created' } });
    const input = { name: '验收租户', slug: 'acceptance', plan: 'free', status: 'active' } as const;

    await tenantApi.createTenant(input);

    expect(api.post).toHaveBeenCalledWith('/admin/tenants', input);
  });
});

describe('tenantApi.joinExisting', () => {
  it('posts only the invitation code in the request body', async () => {
    vi.mocked(api.post).mockResolvedValue({ data: { tenant_id: 'tenant-target' } });
    await expect(tenantApi.joinExisting('one-time-code')).resolves.toEqual({ tenant_id: 'tenant-target' });
    expect(api.post).toHaveBeenCalledWith('/tenant/join', { invitation_code: 'one-time-code' });
  });
});
