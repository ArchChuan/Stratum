import { Route } from 'react-router-dom';

import { AgentChatPage } from './pages/AgentChatPage';
import { AgentsListPage } from './pages/AgentsListPage';
import { CreateAgentPage } from './pages/CreateAgentPage';
import { EditAgentPage } from './pages/EditAgentPage';
import { ExecutionHistoryPage } from './pages/ExecutionHistoryPage';

import { PrivateRoute } from '@/modules/iam';

export const agentRoutes = [
  <Route
    key="agents"
    path="/agents"
    element={
      <PrivateRoute>
        <AgentsListPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="agents-create"
    path="/agents/create"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <CreateAgentPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="agents-edit"
    path="/agents/:id/edit"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <EditAgentPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="agents-chat"
    path="/chat"
    element={
      <PrivateRoute>
        <AgentChatPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="agents-history"
    path="/history"
    element={
      <PrivateRoute>
        <ExecutionHistoryPage />
      </PrivateRoute>
    }
  />,
];
