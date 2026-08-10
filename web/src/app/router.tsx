import { Routes, useLocation } from 'react-router-dom';

import { AppShell } from './layout/AppShell';

import { agentRoutes } from '@/modules/agent';
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
import { promptRoutes } from '@/modules/prompt';
import { scheduledTaskRoutes } from '@/modules/scheduled-task';
import { skillRoutes } from '@/modules/skill';
import { workflowRoutes } from '@/modules/workflow';
import systemE2ESurface from '@/services/e2e-surface.json';

const AUTH_PATHS = ['/login', '/auth/callback', '/onboarding'];

export const MANAGED_ROUTE_PATHS = systemE2ESurface.routes;

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
        {evaluationRoutes}
        {mcpRoutes}
        {knowledgeRoutes}
        {skillRoutes}
        {agentRoutes}
        {llmRoutes}
        {workflowRoutes}
        {operationGateRoutes}
        {promptRoutes}
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
