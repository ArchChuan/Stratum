import { Route } from 'react-router-dom';

import { PlatformSettingsPage } from './pages/PlatformSettingsPage';

import { PrivateRoute } from '@/modules/iam';

export const parametersRoutes = [
  <Route
    key="admin-settings"
    path="/admin/settings"
    element={
      <PrivateRoute requiredRole="global_admin">
        <PlatformSettingsPage />
      </PrivateRoute>
    }
  />,
];
