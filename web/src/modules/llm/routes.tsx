import { Route } from 'react-router-dom';

import { ModelManagementPage } from './pages/ModelManagementPage';

import { PlatformAdminGate, PrivateRoute } from '@/modules/iam';

// 模型目录为公共平台资源：所有登录租户成员可只读查看（数据 GET 对 member 开放），
// 编辑（厂商/模型增删改、启停）需 system_admin 及以上，由 PlatformAdminGate 置灰。
export const llmRoutes = [
  <Route
    key="models"
    path="/models"
    element={
      <PrivateRoute>
        <PlatformAdminGate minRole="system_admin">
          <ModelManagementPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
];
