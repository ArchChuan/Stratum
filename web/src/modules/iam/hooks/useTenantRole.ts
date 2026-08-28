import { useAuth } from '@/modules/iam';

// 租户角色层级，与后端 middleware.RequireTenantRole 一致：owner > admin > member。
const TENANT_ROLE_RANK: Record<string, number> = { member: 1, admin: 2, owner: 3 };
// 平台角色层级，与后端 middleware.RequirePlatformAdmin 一致。
const PLATFORM_ROLE_RANK: Record<string, number> = { user: 1, system_admin: 2, global_admin: 3 };

export interface TenantRoleInfo {
  /** 有效租户角色：平台管理员（system_admin/global_admin）至少视为 admin，owner 保留。 */
  role: string;
  /** admin 或 owner。可管理（新建/编辑/删除）各类资源。 */
  isAdmin: boolean;
  /** 仅 owner。 */
  isOwner: boolean;
  /** 普通成员（member）：只能对话/执行/查看，不能新建/修改/删除。 */
  isMember: boolean;
  /** 判断是否达到某最低角色要求。 */
  hasTenantRole: (min: string) => boolean;
}

/**
 * useTenantRole 统一读取当前租户角色，供前端按钮/入口的权限隐藏使用。
 *
 * 后端已对写操作做 admin 拦截，这里仅负责 UI 层隐藏对应入口，避免成员点了才被 403。
 * 平台管理员在任意租户内至少视为 admin（与后端 EffectiveTenantRole 语义一致），
 * 但绝不视为 owner。
 */
export const useTenantRole = (): TenantRoleInfo => {
  const { user } = useAuth();
  const rawRole = user?.role ?? user?.current_tenant?.role ?? 'member';
  const platformRank = PLATFORM_ROLE_RANK[user?.global_role ?? 'user'] ?? 0;
  const isPlatformAdmin = platformRank >= PLATFORM_ROLE_RANK.system_admin;
  const role = isPlatformAdmin && TENANT_ROLE_RANK[rawRole] < TENANT_ROLE_RANK.admin ? 'admin' : rawRole;
  const rank = TENANT_ROLE_RANK[role] ?? 0;

  return {
    role,
    isAdmin: rank >= TENANT_ROLE_RANK.admin,
    isOwner: rank >= TENANT_ROLE_RANK.owner,
    isMember: rank <= TENANT_ROLE_RANK.member,
    hasTenantRole: (min: string) => rank >= (TENANT_ROLE_RANK[min] ?? 0),
  };
};
