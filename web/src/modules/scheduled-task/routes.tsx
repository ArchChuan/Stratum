import { Route } from 'react-router-dom';

import { ScheduledTaskListPage } from './pages/ScheduledTaskListPage';

import { PrivateRoute } from '@/modules/iam';

export const scheduledTaskRoutes = [
  <Route key="scheduled-tasks" path="/scheduled-tasks"
    element={<PrivateRoute requiredTenantRole="member"><ScheduledTaskListPage /></PrivateRoute>} />,
];
