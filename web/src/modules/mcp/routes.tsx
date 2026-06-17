import { Route } from 'react-router-dom';

import { CreateMCPPage } from './pages/CreateMCPPage';
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
      <PrivateRoute>
        <CreateMCPPage />
      </PrivateRoute>
    }
  />,
];
