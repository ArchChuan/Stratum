import { Route } from 'react-router-dom';

import { EvaluationCenterPage } from './pages/EvaluationCenterPage';

import { PrivateRoute } from '@/modules/iam';

export const evaluationRoutes = [
  <Route
    key="evaluations"
    path="/evaluations"
    element={
      <PrivateRoute>
        <EvaluationCenterPage />
      </PrivateRoute>
    }
  />,
];
