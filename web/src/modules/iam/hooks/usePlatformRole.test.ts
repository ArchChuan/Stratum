import { renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { usePlatformRole } from './usePlatformRole';

const { mockUserRef } = vi.hoisted(() => ({
  mockUserRef: { value: { global_role: 'user' } as { global_role: string } | null },
}));

// 精确 mock AuthContext 而非整个 @/modules/iam barrel，避免 mock 泄漏污染同 worker
// 其他测试文件的 tenantApi 等模块解析。
vi.mock('@/modules/iam/components/AuthContext', () => ({
  useAuth: () => ({ user: mockUserRef.value }),
}));

describe('usePlatformRole', () => {
  it('defaults to user when role is missing', () => {
    mockUserRef.value = null;
    const { result } = renderHook(() => usePlatformRole());
    expect(result.current.role).toBe('user');
    expect(result.current.isSystemAdmin).toBe(false);
    expect(result.current.isGlobalAdmin).toBe(false);
    expect(result.current.hasPlatformRole('user')).toBe(true);
    expect(result.current.hasPlatformRole('system_admin')).toBe(false);
  });

  it('defaults to user when role is empty', () => {
    mockUserRef.value = { global_role: '' };
    const { result } = renderHook(() => usePlatformRole());
    expect(result.current.role).toBe('user');
    expect(result.current.isSystemAdmin).toBe(false);
  });

  it('recognizes system_admin', () => {
    mockUserRef.value = { global_role: 'system_admin' };
    const { result } = renderHook(() => usePlatformRole());
    expect(result.current.role).toBe('system_admin');
    expect(result.current.isSystemAdmin).toBe(true);
    expect(result.current.isGlobalAdmin).toBe(false);
    expect(result.current.hasPlatformRole('system_admin')).toBe(true);
    expect(result.current.hasPlatformRole('global_admin')).toBe(false);
  });

  it('recognizes global_admin', () => {
    mockUserRef.value = { global_role: 'global_admin' };
    const { result } = renderHook(() => usePlatformRole());
    expect(result.current.isGlobalAdmin).toBe(true);
    expect(result.current.isSystemAdmin).toBe(true);
    expect(result.current.hasPlatformRole('global_admin')).toBe(true);
  });
});
