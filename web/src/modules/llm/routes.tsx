import { Route } from 'react-router-dom';

import { ModelManagementPage } from './pages/ModelManagementPage';

import { PrivateRoute } from '@/modules/iam';

export const llmRoutes = [
  <Route
    key="models"
    path="/models"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <ModelManagementPage />
      </PrivateRoute>
    }
  />,
];
