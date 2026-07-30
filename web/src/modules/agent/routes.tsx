import { Route } from 'react-router-dom';

import { AgentChatPage } from './pages/AgentChatPage';
import { AgentManagementPage } from './pages/AgentManagementPage';
import { CreateAgentPage } from './pages/CreateAgentPage';
import { EditAgentPage } from './pages/EditAgentPage';
import { ResourceChangeProposalPage } from './pages/ResourceChangeProposalPage';

import { PrivateRoute } from '@/modules/iam';

export const agentRoutes = [
  <Route
    key="resource-change-proposal"
    path="/resource-change-proposals/:id"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <ResourceChangeProposalPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="agents"
    path="/agents"
    element={
      <PrivateRoute>
        <AgentManagementPage />
      </PrivateRoute>
    }
  />,
	<Route
		key="agents-list"
		path="/agents/list"
		element={<PrivateRoute><AgentManagementPage /></PrivateRoute>}
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
];
