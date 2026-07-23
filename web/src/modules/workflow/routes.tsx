import { Route } from 'react-router-dom';

import { WorkflowCatalogPage } from './pages/WorkflowCatalogPage';
import { WorkflowDesignerPage } from './pages/WorkflowDesignerPage';

import { PrivateRoute } from '@/modules/iam';

export const workflowRoutes = [
  <Route key="workflows" path="/workflows" element={<PrivateRoute><WorkflowCatalogPage /></PrivateRoute>} />,
  <Route key="workflow-new" path="/workflows/new" element={<PrivateRoute requiredTenantRole="admin"><WorkflowDesignerPage /></PrivateRoute>} />,
  <Route key="workflow-edit" path="/workflows/:id/edit" element={<PrivateRoute requiredTenantRole="admin"><WorkflowDesignerPage /></PrivateRoute>} />,
];
