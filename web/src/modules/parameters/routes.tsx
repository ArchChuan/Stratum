import { Route } from 'react-router-dom';

import { PlatformSettingsPage } from './pages/PlatformSettingsPage';

import { PlatformAdminGate, PrivateRoute } from '@/modules/iam';

export const parametersRoutes = [
  <Route
    key="admin-settings"
    path="/admin/settings"
    element={
      <PrivateRoute>
        <PlatformAdminGate minRole="system_admin">
          <PlatformSettingsPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
];
