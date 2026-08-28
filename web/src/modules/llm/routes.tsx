import { Route } from 'react-router-dom';

import { ModelManagementPage } from './pages/ModelManagementPage';

import { PrivateRoute } from '@/modules/iam';

// 模型目录为公共平台资源，平台管理员（system_admin）及以上可管理（查看+编辑）。
export const llmRoutes = [
  <Route
    key="models"
    path="/models"
    element={
      <PrivateRoute requiredRole="system_admin">
        <ModelManagementPage />
      </PrivateRoute>
    }
  />,
];
