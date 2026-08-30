import {
  operationProposalListSchema,
  operationProposalPageSchema,
  operationProposalSchema,
  pendingApprovalSchema,
  selfModifyResultSchema,
  type OperationProposal,
  type OperationProposalPage,
} from '../model/operationProposal';

import { DEFAULT_PAGE_SIZE } from '@/constants';
import api from '@/services/client';

// 六类资源的白名单自助申请：workflow/mcp/workspace 走统一 request-editor；
// knowledge_doc 走 request-access（编辑/查看一体，保留原路由）。
export type GrantableResourceType =
  | 'agent'
  | 'skill'
  | 'knowledge_doc'
  | 'mcp'
  | 'knowledge_workspace'
  | 'workflow';

export interface RequestEditorAccessOptions {
  workspaceName?: string;
  resourceName?: string;
}

function requestAccessUrl(resourceType: GrantableResourceType, resourceId: string, workspaceName?: string): string {
  switch (resourceType) {
    case 'knowledge_doc':
      return `/knowledge/workspaces/${workspaceName ?? ''}/documents/${resourceId}/request-access`;
    case 'knowledge_workspace':
      return `/knowledge/workspaces/${resourceId}/request-editor`;
    case 'mcp':
      return `/mcp/servers/${resourceId}/request-editor`;
    default:
      return `/${resourceType}s/${resourceId}/request-editor`;
  }
}

export const operationProposalApi = {
  listPending: async (): Promise<OperationProposal[]> => {
    const response = await api.get('/operation-proposals');
    return operationProposalListSchema.parse(response.data).proposals;
  },
  get: async (id: string) => {
    const response = await api.get(`/operation-proposals/${id}`);
    return operationProposalSchema.parse(response.data);
  },
  startReview: async (id: string) => {
    await api.post(`/operation-proposals/${id}/review`);
  },
  approve: async (id: string) => {
    await api.post(`/operation-proposals/${id}/approve`);
  },
  reject: async (id: string, note: string) => {
    await api.post(`/operation-proposals/${id}/reject`, { note });
  },
  selfModify: async (agentId: string, payload: Record<string, unknown>) => {
    const response = await api.post(`/agents/${agentId}/self-modify`, payload);
    return selfModifyResultSchema.parse(response.data);
  },
  // 我发起的全部提案（任意状态，最新在前）：权限审批 tab 的成员视图。
  listMine: async (): Promise<OperationProposal[]> => {
    const response = await api.get('/operation-proposals/mine');
    return operationProposalListSchema.parse(response.data).proposals;
  },
  // 白名单自助申请：六类资源统一入口，URL 按资源类型 switch（见 requestAccessUrl）。
  // body 只发 resourceType + resourceName：resourceId 走路由 param，resourceType 由后端
  // 从路由推导并交叉校验；body 冗余字段（如 resourceId）会被 DisallowUnknownFields 拒掉。
  // resourceName 仅用于审批中心展示；knowledge_doc 需带 workspaceName 定位路由。
  requestEditorAccess: async (
    resourceType: GrantableResourceType,
    resourceId: string,
    opts?: RequestEditorAccessOptions,
  ) => {
    const response = await api.post(requestAccessUrl(resourceType, resourceId, opts?.workspaceName), {
      resourceType,
      resourceName: opts?.resourceName,
    });
    return pendingApprovalSchema.parse(response.data);
  },
  // 分页历史（非 pending 终态，后端按角色过滤：admin/owner 全租户，member 仅本人）。
  listHistory: async (page: number, pageSize = DEFAULT_PAGE_SIZE): Promise<OperationProposalPage> => {
    const response = await api.get('/operation-proposals/history', { params: { page, page_size: pageSize } });
    return operationProposalPageSchema.parse(response.data);
  },
  // 取消待审批提案：发起人自撤（member）或管理员代撤（admin），落 cancelled 终态。
  cancel: async (id: string) => {
    await api.post(`/operation-proposals/${id}/cancel`);
  },
};
