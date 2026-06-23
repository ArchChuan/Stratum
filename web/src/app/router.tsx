import { Routes, useLocation } from 'react-router-dom';

import { AppShell } from './layout/AppShell';

import { agentRoutes } from '@/modules/agent';
import { dashboardRoutes } from '@/modules/dashboard';
import { iamPublicRoutes, iamPrivateRoutes, useAuth } from '@/modules/iam';
import { knowledgeRoutes } from '@/modules/knowledge';
import { mcpRoutes } from '@/modules/mcp';
import { skillRoutes } from '@/modules/skill';

const AUTH_PATHS = ['/login', '/auth/callback', '/onboarding'];

export const AppRouter = () => {
  const { user } = useAuth();
  const location = useLocation();
  const isAuthPage = AUTH_PATHS.some((p) => location.pathname.startsWith(p));

  if (isAuthPage) {
    return <Routes>{iamPublicRoutes}</Routes>;
  }

  return (
    <AppShell>
      <Routes key={user?.tenant_id || 'no-tenant'}>
        {dashboardRoutes}
        {mcpRoutes}
        {knowledgeRoutes}
        {skillRoutes}
        {agentRoutes}
        {iamPublicRoutes}
      </Routes>
    </AppShell>
  );
};

export default AppRouter;
