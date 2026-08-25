import { OperationProposalsPanel } from '../components/OperationProposalsPanel';

// 操作提案审批已并入审批中心「权限审批」tab（/approvals?tab=permission），
// 本页保留为 admin 独立入口的薄包装，路由重定向后不再可达。
export const OperationProposalsPage = () => <OperationProposalsPanel />;
