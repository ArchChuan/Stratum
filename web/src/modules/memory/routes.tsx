import { Route } from 'react-router-dom';

import { AdminMemoryPage } from './pages/AdminMemoryPage';

import { PrivateRoute } from '@/modules/iam';

export const memoryRoutes = [
  <Route
    key="admin-memory"
    path="/admin/memory"
    element={
      <PrivateRoute requiredRole="system_admin">
        <AdminMemoryPage />
      </PrivateRoute>
    }
  />,
];
