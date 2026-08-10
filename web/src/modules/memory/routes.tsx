import { Route } from 'react-router-dom';

import { MyMemoriesPage } from './pages/MyMemoriesPage';

import { PrivateRoute } from '@/modules/iam';

export const memoryRoutes = [
  <Route
    key="memory"
    path="/memory"
    element={
      <PrivateRoute requiredTenantRole="member">
        <MyMemoriesPage />
      </PrivateRoute>
    }
  />,
];
