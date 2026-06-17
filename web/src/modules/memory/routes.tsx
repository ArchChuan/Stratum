import { Route } from 'react-router-dom';

import { MemoryPage } from './pages/MemoryPage';

import { PrivateRoute } from '@/modules/iam';

export const memoryRoutes = [
  <Route
    key="memory"
    path="/memory"
    element={
      <PrivateRoute>
        <MemoryPage />
      </PrivateRoute>
    }
  />,
];
