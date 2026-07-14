import { Spin, Result, Button } from 'antd';
import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';

import { useAuth } from './AuthContext';

interface PrivateRouteProps {
  children: ReactNode;
  requiredRole?: string;
  /** 租户内最低角色要求：'member' | 'admin' | 'owner'。用于拦截普通成员访问新建/编辑页。 */
  requiredTenantRole?: string;
}

// 租户角色层级，与后端 middleware.RequireTenantRole 保持一致。
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
        style={{
          minHeight: '100vh',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}
      >
        <Spin size="large" tip="加载中..." />
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
    const isGlobalAdmin = user.global_role === 'global_admin';
    const isSystemAdmin = user.system_role === 'system_admin' || isGlobalAdmin;
    const ok =
      requiredRole === 'global_admin'
        ? isGlobalAdmin
        : requiredRole === 'system_admin'
          ? isSystemAdmin
          : user.global_role === requiredRole || user.system_role === requiredRole;

    if (!ok) {
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
