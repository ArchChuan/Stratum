import { Route } from 'react-router-dom';

import { PrivateRoute } from './components/PrivateRoute';
import { TenantsListPage } from './pages/admin/TenantsListPage';
import { CallbackPage } from './pages/auth/CallbackPage';
import { LoginPage } from './pages/auth/LoginPage';
import { OnboardingPage } from './pages/auth/OnboardingPage';
import { MembersPage } from './pages/tenant/MembersPage';
import { SettingsPage } from './pages/tenant/SettingsPage';

export const iamPublicRoutes = [
  <Route key="login" path="/login" element={<LoginPage />} />,
  <Route key="callback" path="/auth/callback" element={<CallbackPage />} />,
  <Route key="onboarding" path="/onboarding" element={<OnboardingPage />} />,
];

export const iamPrivateRoutes = [
  <Route
    key="tenant-members"
    path="/tenant/members"
    element={
      <PrivateRoute>
        <MembersPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="tenant-settings"
    path="/tenant/settings"
    element={
      <PrivateRoute>
        <SettingsPage />
      </PrivateRoute>
    }
  />,
  <Route
    key="admin-tenants"
    path="/admin/tenants"
    element={
      <PrivateRoute requiredRole="global_admin">
        <TenantsListPage />
      </PrivateRoute>
    }
  />,
];
