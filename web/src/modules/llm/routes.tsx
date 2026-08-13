import { Route } from 'react-router-dom';

import { ModelManagementPage } from './pages/ModelManagementPage';

import { PrivateRoute } from '@/modules/iam';

// 模型目录为公共平台资源，仅 global admin 管理（对齐机制/提示词权限面）。
export const llmRoutes = [
  <Route
    key="models"
    path="/models"
    element={
      <PrivateRoute requiredRole="global_admin">
        <ModelManagementPage />
      </PrivateRoute>
    }
  />,
];
