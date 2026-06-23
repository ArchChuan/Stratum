import { Spin, Result, Button } from 'antd';
import type { ReactNode } from 'react';
import { Navigate, useLocation } from 'react-router-dom';

import { useAuth } from './AuthContext';

interface PrivateRouteProps {
  children: ReactNode;
  requiredRole?: string;
}

export const PrivateRoute = ({ children, requiredRole }: PrivateRouteProps) => {
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

  return <>{children}</>;
};

export default PrivateRoute;
