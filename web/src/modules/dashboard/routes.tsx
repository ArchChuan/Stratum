import { Route } from 'react-router-dom';

import { DashboardPage } from './pages/DashboardPage';

import { PrivateRoute } from '@/modules/iam';

export const dashboardRoutes = [
  <Route
    key="dashboard"
    path="/"
    element={
      <PrivateRoute>
        <DashboardPage />
      </PrivateRoute>
    }
  />,
];
