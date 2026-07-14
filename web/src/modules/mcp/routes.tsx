import { Route } from 'react-router-dom';

import { CreateMCPPage } from './pages/CreateMCPPage';
import { EditMCPPage } from './pages/EditMCPPage';
import { MCPServersPage } from './pages/MCPServersPage';

import { PrivateRoute } from '@/modules/iam';

export const mcpRoutes = [
  <Route
    key="mcp"
    path="/mcp"
    element={
      <PrivateRoute>
        <MCPServersPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="mcp-create"
    path="/mcp/create"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <CreateMCPPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="mcp-edit"
    path="/mcp/:id/edit"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <EditMCPPage />
      </PrivateRoute>
    }
  />,
];
