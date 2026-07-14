import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { tenantApi } from '../api/tenant.api';

import { useTenantMembers } from './useTenantMembers';

vi.mock('../api/tenant.api', () => ({
  tenantApi: {
    members: vi.fn(),
    inviteMember: vi.fn(),
    removeMember: vi.fn(),
    updateMemberRole: vi.fn(),
  },
}));

vi.mock('@/modules/iam', () => ({
  useAuth: () => ({ user: { sub: 'owner-1', role: 'owner' } }),
}));

describe('useTenantMembers', () => {
  beforeEach(() => {
    vi.mocked(tenantApi.members).mockReset();
    vi.mocked(tenantApi.members).mockImplementation(async (page = 1, pageSize = 20) => ({
      members: [],
      total: 25,
      page,
      page_size: pageSize,
    }));
  });

  it('loads the first server-side page and can navigate to the second page', async () => {
    const { result } = renderHook(() => useTenantMembers());

    await waitFor(() => expect(tenantApi.members).toHaveBeenCalledWith(1, 20));
    expect(result.current.pagination).toEqual({ current: 1, pageSize: 20, total: 25 });

    await act(async () => {
      await result.current.fetchPage(2, 20);
    });

    expect(tenantApi.members).toHaveBeenLastCalledWith(2, 20);
    expect(result.current.pagination).toEqual({ current: 2, pageSize: 20, total: 25 });
  });
});
