import { DEFAULT_PAGE_SIZE } from '@/constants';
import api from '@/services/client';

// 审批行（待审批/历史共用）。字段对齐后端 domain.ToolApproval 与 handler DTO 的 JSON tag。
// 昵称字段由后端批量解析（display_name > github_login > actor_id，M5），缺失时前端回退 raw id。
export interface ApprovalRow {
  id: string;
  subject_kind: string;
  tool_name: string;
  server_id: string;
  risk_level: string;
  status: string;
  user_id: string;
  user_display_name?: string;
  assigned_approver?: string;
  assigned_approver_name?: string;
  invalidation_reason?: string;
  conversation_id?: string;
  created_at: string;
  expires_at: string;
  decided_by?: string;
  decided_by_name?: string;
  decision_reason?: string;
}

// 审批详情（admin/owner 工作台）：payload 为后端解密并脱敏后的参数视图。
export interface ApprovalDetail extends ApprovalRow {
  payload?: Record<string, unknown>;
}

export interface ApprovalHistoryPage {
  approvals: ApprovalRow[];
  total: number;
  page: number;
  page_size: number;
}

export interface ApprovalExecuteResult {
  status: 'executed';
  output?: Record<string, unknown>;
}

export type ApprovalDecision = 'approved' | 'rejected';

export const approvalApi = {
  listPending: async (): Promise<ApprovalRow[]> => {
    const { data } = await api.get<{ approvals: ApprovalRow[] }>('/agents/tool-approvals');
    return data.approvals ?? [];
  },

  listHistory: async (
    page: number,
    pageSize: number = DEFAULT_PAGE_SIZE,
  ): Promise<ApprovalHistoryPage> => {
    const { data } = await api.get<ApprovalHistoryPage>('/agents/tool-approvals/history', {
      params: { page, page_size: pageSize },
    });
    return data;
  },

  getDetail: async (id: string): Promise<ApprovalDetail> => {
    const { data } = await api.get<ApprovalDetail>(`/agents/tool-approvals/${encodeURIComponent(id)}`);
    return data;
  },

  execute: async (id: string): Promise<ApprovalExecuteResult> => {
    const { data } = await api.post<ApprovalExecuteResult>(`/agents/tool-approvals/${encodeURIComponent(id)}/execute`);
    return data;
  },

  setAssignee: async (id: string, assignedApprover: string): Promise<void> => {
    await api.put(`/agents/tool-approvals/${encodeURIComponent(id)}/assignee`, { assignedApprover });
  },

  decide: async (id: string, decision: ApprovalDecision, reason?: string): Promise<void> => {
    await api.post(`/agents/tool-approvals/${encodeURIComponent(id)}/decision`, { decision, reason });
  },
};
