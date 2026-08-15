import { Route } from 'react-router-dom';

import { AuditEventsPage } from './pages/AuditEventsPage';

import { PrivateRoute } from '@/modules/iam';

export const auditRoutes = [
  <Route
    key="audit"
    path="/audit"
    element={
      <PrivateRoute requiredTenantRole="admin">
        <AuditEventsPage />
      </PrivateRoute>
    }
  />,
];
