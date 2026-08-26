import { z } from 'zod';

export const opProposalStatusSchema = z.enum(['proposed', 'reviewing', 'approved', 'rejected', 'executed', 'cancelled']);
export type OpProposalStatus = z.infer<typeof opProposalStatusSchema>;

export const opTypeSchema = z.enum(['revision_apply', 'cross_agent_delegate', 'schedule_create', 'self_modify', 'grant_editor']);
export type OpType = z.infer<typeof opTypeSchema>;

export const operationProposalSchema = z.object({
  id: z.string(),
  agentId: z.string(),
  targetAgentId: z.string().optional(),
  opType: opTypeSchema,
  delegation: z.string().optional(),
  maxDailyCostUsd: z.number().optional(),
  maxDailyExecutions: z.number().optional(),
  payloadSummary: z.unknown(),
  status: opProposalStatusSchema,
  proposerId: z.string(),
  reviewedBy: z.string().optional(),
  reviewNote: z.string().optional(),
  createdAt: z.string(),
  resolvedAt: z.string().optional(),
  expiresAt: z.string().optional(),
});
export type OperationProposal = z.infer<typeof operationProposalSchema>;

export const operationProposalListSchema = z.object({
  proposals: z.array(operationProposalSchema),
});
export type OperationProposalList = z.infer<typeof operationProposalListSchema>;

// 分页历史响应（后端 GET /operation-proposals/history）：非 pending 终态按角色过滤，
// admin/owner 全租户、member 仅本人。
export const operationProposalPageSchema = z.object({
  proposals: z.array(operationProposalSchema),
  total: z.number(),
  page: z.number(),
  pageSize: z.number(),
});
export type OperationProposalPage = z.infer<typeof operationProposalPageSchema>;

export const selfModifyResultSchema = z.object({
  status: z.enum(['pending_approval', 'approved']),
  reason: z.string(),
  proposalId: z.string().optional(),
  agent: z.unknown().optional(),
  usageWarning: z.string().optional(),
});
export type SelfModifyResult = z.infer<typeof selfModifyResultSchema>;

// grant_editor 提案提交成功后的响应：资源页「申请编辑权限 / 申请查看权限」返回
// 202 {"status":"pending_approval"}，提示已进入审批中心。
export const pendingApprovalSchema = z.object({
  status: z.enum(['pending_approval']),
});
export type PendingApproval = z.infer<typeof pendingApprovalSchema>;

// 审批中心展示用的类型/状态常量（operation-gate 与 approvals 页面共用）。
export const OP_TYPE_LABELS: Record<string, string> = {
  revision_apply: '修订应用',
  cross_agent_delegate: '跨 Agent 委托',
  schedule_create: '定时任务创建',
  self_modify: '自修改',
  grant_editor: '权限申请',
};
export const STATUS_LABELS: Record<string, string> = {
  proposed: '待审批',
  reviewing: '审批中',
  approved: '已批准',
  rejected: '已拒绝',
  executed: '已执行',
  cancelled: '已取消',
};
export const STATUS_COLORS: Record<string, string> = {
  proposed: 'orange',
  reviewing: 'blue',
  approved: 'green',
  rejected: 'red',
  executed: 'default',
  cancelled: 'default',
};

// 审批中心/铃铛展示用：grant_editor 提案复用 agentId 承载任意资源 id（skill/knowledge_doc），
// 优先展示 payloadSummary.resourceName（「workspace/文档标题」等），回退 agentId / 提案 id。
export const proposalResourceLabel = (p: Pick<OperationProposal, 'payloadSummary' | 'agentId' | 'id'>): string => {
  const ps = (p.payloadSummary ?? {}) as { resourceName?: string };
  return ps.resourceName || p.agentId || p.id;
};
