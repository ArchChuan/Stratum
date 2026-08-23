import { Route } from 'react-router-dom';

import { ApprovalsPage } from './pages/ApprovalsPage';

import { PrivateRoute } from '@/modules/iam';

export const approvalsRoutes = [
  <Route
    key="approvals"
    path="/approvals"
    element={
      <PrivateRoute requiredTenantRole="member">
        <ApprovalsPage />
      </PrivateRoute>
    }
  />,
];
