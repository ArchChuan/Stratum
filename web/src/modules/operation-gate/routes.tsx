import { Navigate, Route } from 'react-router-dom';

// 操作提案与权限申请并入审批中心「权限审批」tab：/operation-proposals 保留路径
// 但重定向到 /approvals?tab=permission（approvals 路由 member 可访问，面板内按
// 角色分流 admin 审批 / member 只看自己的）。
export const operationGateRoutes = [
  <Route key="operation-proposals" path="/operation-proposals"
    element={<Navigate to="/approvals?tab=permission" replace />} />,
];
