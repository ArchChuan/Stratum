import { Route } from 'react-router-dom';

import { PromptListPage } from './pages/PromptListPage';

import { PrivateRoute } from '@/modules/iam';

export const promptRoutes = [
  <Route
    key="prompts"
    path="/prompts"
    element={
      <PrivateRoute requiredRole="global_admin">
        <PromptListPage />
      </PrivateRoute>
    }
  />,
];
