import { Route } from 'react-router-dom';

import { KnowledgeDetailPage } from './pages/KnowledgeDetailPage';
import { KnowledgePage } from './pages/KnowledgePage';

import { PrivateRoute } from '@/modules/iam';

export const knowledgeRoutes = [
  <Route
    key="knowledge"
    path="/knowledge"
    element={
      <PrivateRoute>
        <KnowledgePage />
      </PrivateRoute>
    }
  />,
  <Route
    key="knowledge-detail"
    path="/knowledge/:name"
    element={
      <PrivateRoute>
        <KnowledgeDetailPage />
      </PrivateRoute>
    }
  />,
];
