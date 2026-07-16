import { useAuth } from '@/modules/iam';

// 租户角色层级，与后端 middleware.RequireTenantRole 一致：owner > admin > member。
const TENANT_ROLE_RANK: Record<string, number> = { member: 1, admin: 2, owner: 3 };

export interface TenantRoleInfo {
  /** 当前租户内的角色，优先取 user.role，回退 current_tenant.role，默认 member。 */
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
 */
export const useTenantRole = (): TenantRoleInfo => {
  const { user } = useAuth();
  const role = user?.role ?? user?.current_tenant?.role ?? 'member';
  const rank = TENANT_ROLE_RANK[role] ?? 0;

  return {
    role,
    isAdmin: rank >= TENANT_ROLE_RANK.admin,
    isOwner: rank >= TENANT_ROLE_RANK.owner,
    isMember: rank <= TENANT_ROLE_RANK.member,
    hasTenantRole: (min: string) => rank >= (TENANT_ROLE_RANK[min] ?? 0),
  };
};
