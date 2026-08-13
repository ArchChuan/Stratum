import { Route } from 'react-router-dom';

import { ModelProfilePage } from './pages/ModelProfilePage';

import { PrivateRoute } from '@/modules/iam';

export const mechanismRoutes = [
  <Route
    key="mechanism-profiles"
    path="/mechanism/profiles"
    element={
      <PrivateRoute requiredRole="global_admin">
        <ModelProfilePage />
      </PrivateRoute>
    }
  />,
];
