import { useAuth } from '@/modules/iam';

// 平台角色层级，与后端 middleware.RequirePlatformAdmin 一致：global_admin > system_admin > user。
const PLATFORM_ROLE_RANK: Record<string, number> = { user: 1, system_admin: 2, global_admin: 3 };

export interface PlatformRoleInfo {
  /** 当前平台角色，取自 user.global_role，默认 user。 */
  role: string;
  /** system_admin 或 global_admin。 */
  isSystemAdmin: boolean;
  /** 仅 global_admin。 */
  isGlobalAdmin: boolean;
  /** 判断是否达到某最低平台角色要求。 */
  hasPlatformRole: (min: string) => boolean;
}

/**
 * usePlatformRole 统一读取平台角色（users.global_role），供平台管理入口的权限隐藏使用。
 * 与 useTenantRole 完全脱钩（租户一个体系，平台一个体系）。
 */
export const usePlatformRole = (): PlatformRoleInfo => {
  const { user } = useAuth();
  const role = user?.global_role || 'user';
  const rank = PLATFORM_ROLE_RANK[role] ?? 0;

  return {
    role,
    isSystemAdmin: rank >= PLATFORM_ROLE_RANK.system_admin,
    isGlobalAdmin: rank >= PLATFORM_ROLE_RANK.global_admin,
    hasPlatformRole: (min: string) => rank >= (PLATFORM_ROLE_RANK[min] ?? 0),
  };
};
