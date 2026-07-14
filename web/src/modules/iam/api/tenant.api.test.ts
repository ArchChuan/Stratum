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
