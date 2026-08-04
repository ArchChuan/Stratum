import { Route } from 'react-router-dom';

import { OperationProposalsPage } from './pages/OperationProposalsPage';

import { PrivateRoute } from '@/modules/iam';

export const operationGateRoutes = [
  <Route key="operation-proposals" path="/operation-proposals"
    element={<PrivateRoute requiredTenantRole="admin"><OperationProposalsPage /></PrivateRoute>} />,
];
