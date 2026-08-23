import { Navigate, Routes, useLocation } from 'react-router-dom';

import { AppShell } from './layout/AppShell';

import { agentRoutes } from '@/modules/agent';
import { approvalsRoutes } from '@/modules/approvals';
import { auditRoutes } from '@/modules/audit';
import { collabRoutes } from '@/modules/collab';
import { dashboardRoutes } from '@/modules/dashboard';
import { evaluationRoutes } from '@/modules/evaluation';
import { iamPublicRoutes, iamPrivateRoutes, useAuth } from '@/modules/iam';
import { knowledgeRoutes } from '@/modules/knowledge';
import { llmRoutes } from '@/modules/llm';
import { mcpRoutes } from '@/modules/mcp';
import { memoryRoutes } from '@/modules/memory';
import { operationGateRoutes } from '@/modules/operation-gate';
import { parametersRoutes } from '@/modules/parameters';
import { scheduledTaskRoutes } from '@/modules/scheduled-task';
import { skillRoutes } from '@/modules/skill';
import { workflowRoutes } from '@/modules/workflow';
import systemE2ESurface from '@/services/e2e-surface.json';

const AUTH_PATHS = ['/login', '/auth/callback', '/onboarding'];

export const MANAGED_ROUTE_PATHS = systemE2ESurface.routes;

export const AppRouter = () => {
  const { user, loading } = useAuth();
  const location = useLocation();
  const isAuthPage = AUTH_PATHS.some((p) => location.pathname.startsWith(p));

  // 注册/登录成功后用户必有租户（新用户默认进默认租户）；若因历史竞态、
  // 旧缓存或直接访问停留在 /onboarding，会话一旦恢复（current_tenant 就绪）
  // 就应回主页，onboarding 不应成为已登录用户的中间页。
  if (!loading && location.pathname === '/onboarding' && user?.current_tenant) {
    return <Navigate to="/" replace />;
  }

  if (isAuthPage) {
    return <Routes>{iamPublicRoutes}</Routes>;
  }

  return (
    <AppShell>
      <Routes key={user?.tenant_id || 'no-tenant'}>
        {dashboardRoutes}
        {evaluationRoutes}
        {mcpRoutes}
        {knowledgeRoutes}
        {skillRoutes}
        {agentRoutes}
        {llmRoutes}
        {workflowRoutes}
        {operationGateRoutes}
        {parametersRoutes}
        {approvalsRoutes}
        {auditRoutes}
        {memoryRoutes}
        {collabRoutes}
        {scheduledTaskRoutes}
        {iamPrivateRoutes}
        {iamPublicRoutes}
      </Routes>
    </AppShell>
  );
};

export default AppRouter;
