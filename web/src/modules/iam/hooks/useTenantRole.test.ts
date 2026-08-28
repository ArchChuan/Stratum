import { describe, expect, it, vi } from 'vitest';

import { useTenantRole } from './useTenantRole';

import { useAuth } from '@/modules/iam';

vi.mock('@/modules/iam', () => ({
  useAuth: vi.fn(),
}));

describe('useTenantRole', () => {
  it('platform admin is admin even in a foreign tenant', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'member', global_role: 'system_admin', current_tenant: { role: 'member' } },
    } as any);
    const { role, isAdmin, isOwner } = useTenantRole();
    expect(role).toBe('admin');
    expect(isAdmin).toBe(true);
    expect(isOwner).toBe(false);
  });

  it('global admin with owner keeps owner', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'owner', global_role: 'global_admin', current_tenant: { role: 'owner' } },
    } as any);
    const { role, isAdmin, isOwner } = useTenantRole();
    expect(role).toBe('owner');
    expect(isAdmin).toBe(true);
    expect(isOwner).toBe(true);
  });

  it('ordinary member unchanged', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'member', global_role: 'user', current_tenant: { role: 'member' } },
    } as any);
    const { role, isAdmin, isMember } = useTenantRole();
    expect(role).toBe('member');
    expect(isAdmin).toBe(false);
    expect(isMember).toBe(true);
  });

  it('hasTenantRole respects elevation', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'member', global_role: 'system_admin', current_tenant: { role: 'member' } },
    } as any);
    const { hasTenantRole } = useTenantRole();
    expect(hasTenantRole('admin')).toBe(true);
    expect(hasTenantRole('owner')).toBe(false);
  });
});
