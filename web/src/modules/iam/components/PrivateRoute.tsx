import { Spin, Result, Button } from 'antd';
import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';

import { useAuth } from './AuthContext';

interface PrivateRouteProps {
  children: ReactNode;
  /** 平台角色最低要求：'system_admin' | 'global_admin'。与租户角色完全脱钩。 */
  requiredRole?: string;
  /** 租户内最低角色要求：'member' | 'admin' | 'owner'。用于拦截普通成员访问新建/编辑页。 */
  requiredTenantRole?: string;
}

// 平台角色层级，与后端 middleware.RequirePlatformAdmin 保持一致。
const PLATFORM_ROLE_RANK: Record<string, number> = { user: 1, system_admin: 2, global_admin: 3 };
// 租户角色层级，与后端 middleware.RequireTenantRole 保持一致（member < admin < owner）。
const TENANT_ROLE_RANK: Record<string, number> = { member: 1, admin: 2, owner: 3 };

export const PrivateRoute = ({
  children,
  requiredRole,
  requiredTenantRole,
}: PrivateRouteProps) => {
  const { user, loading } = useAuth();
  const location = useLocation();

  if (loading) {
    return (
      <div
        role="status"
        aria-label="加载中..."
        style={{
          minHeight: '100vh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Spin size="large" />
      </div>
    );
  }

  if (!user) {
    return <Navigate to="/login" state={{ from: location }} replace />;
  }

  if (!user.current_tenant && location.pathname !== '/onboarding') {
    return <Navigate to="/onboarding" replace />;
  }

  if (requiredRole) {
    const actual = PLATFORM_ROLE_RANK[user.global_role || 'user'] ?? 0;
    const required = PLATFORM_ROLE_RANK[requiredRole] ?? 0;
    if (actual < required) {
      return (
        <Result
          status="403"
          title="403"
          subTitle="您没有访问此页面的权限。"
          extra={
            <Button type="primary" onClick={() => window.history.back()}>
              返回
            </Button>
          }
        />
      );
    }
  }

  if (requiredTenantRole) {
    // 租户角色优先取 user.role，回退到 current_tenant.role。
    const tenantRole = user.role ?? user.current_tenant?.role ?? 'member';
    const required = TENANT_ROLE_RANK[requiredTenantRole] ?? 0;
    if ((TENANT_ROLE_RANK[tenantRole] ?? 0) < required) {
      return (
        <Result
          status="403"
          title="403"
          subTitle="仅管理员可访问此页面，普通成员无权限。"
          extra={
            <Button type="primary" onClick={() => window.history.back()}>
              返回
            </Button>
          }
        />
      );
    }
  }

  return <>{children}</>;
};

export default PrivateRoute;
