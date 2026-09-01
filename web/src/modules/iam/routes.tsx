import { Route } from 'react-router-dom';

import { PlatformAuditPage } from '../audit/pages/PlatformAuditPage';

import { PlatformAdminGate } from './components/PlatformAdminGate';
import { PrivateRoute } from './components/PrivateRoute';
import { ProfilePage } from './pages/ProfilePage';
import { AdminsPage } from './pages/admin/AdminsPage';
import { TenantsListPage } from './pages/admin/TenantsListPage';
import { CallbackPage } from './pages/auth/CallbackPage';
import { LoginPage } from './pages/auth/LoginPage';
import { OnboardingPage } from './pages/auth/OnboardingPage';
import { RegisterPage } from './pages/auth/RegisterPage';
import { MembersPage } from './pages/tenant/MembersPage';
import { SettingsPage } from './pages/tenant/SettingsPage';

export const iamPublicRoutes = [
  <Route key="login" path="/login" element={<LoginPage />} />,
  <Route key="register" path="/register" element={<RegisterPage />} />,
  <Route key="callback" path="/auth/callback" element={<CallbackPage />} />,
  <Route key="onboarding" path="/onboarding" element={<OnboardingPage />} />,
];

export const iamPrivateRoutes = [
  <Route
    key="profile"
    path="/profile"
    element={
      <PrivateRoute>
        <ProfilePage />
      </PrivateRoute>
    }
  />,
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
      <PrivateRoute>
        <PlatformAdminGate minRole="system_admin">
          <TenantsListPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
  <Route
    key="admin-admins"
    path="/admin/admins"
    element={
      <PrivateRoute>
        <PlatformAdminGate minRole="global_admin">
          <AdminsPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
  <Route
    key="admin-audit"
    path="/admin/audit"
    element={
      <PrivateRoute>
        <PlatformAuditPage />
      </PrivateRoute>
    }
  />,
];
