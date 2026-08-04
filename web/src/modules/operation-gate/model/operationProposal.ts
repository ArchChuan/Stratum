import { z } from 'zod';

export const opProposalStatusSchema = z.enum(['proposed', 'reviewing', 'approved', 'rejected', 'executed']);
export type OpProposalStatus = z.infer<typeof opProposalStatusSchema>;

export const opTypeSchema = z.enum(['revision_apply', 'cross_agent_delegate', 'schedule_create', 'self_modify']);
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

export const selfModifyResultSchema = z.object({
  status: z.enum(['pending_approval', 'approved']),
  reason: z.string(),
  proposalId: z.string().optional(),
  agent: z.unknown().optional(),
  usageWarning: z.string().optional(),
});
export type SelfModifyResult = z.infer<typeof selfModifyResultSchema>;
