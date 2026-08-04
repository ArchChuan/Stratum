import { Route } from 'react-router-dom';

import { CollaborationsPage } from './pages/CollaborationsPage';

import { PrivateRoute } from '@/modules/iam';

export const collabRoutes = [
  <Route key="collaborations" path="/collaborations"
    element={<PrivateRoute requiredTenantRole="member"><CollaborationsPage /></PrivateRoute>} />,
];
