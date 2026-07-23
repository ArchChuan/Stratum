import { Route } from 'react-router-dom';

import { WorkflowCatalogPage } from './pages/WorkflowCatalogPage';

import { PrivateRoute } from '@/modules/iam';

export const workflowRoutes = [
  <Route key="workflows" path="/workflows" element={<PrivateRoute><WorkflowCatalogPage /></PrivateRoute>} />,
];
