import { z } from 'zod';

export const proposalStatusSchema = z.enum([
  'draft', 'ready_for_review', 'confirmed', 'applying', 'applied', 'invalid',
  'stale', 'expired', 'failed', 'unknown_outcome', 'cancelled',
]);
export const proposalOperationSchema = z.enum(['create', 'update']);

const agentPayloadSchema = z.object({
  name: z.string(), description: z.string(), model: z.string(), maxIterations: z.number(),
  maxContextTokens: z.number(), skillIds: z.array(z.string()).optional(),
  mcpToolIds: z.array(z.string()).optional(), workspaceIds: z.array(z.string()).optional(),
}).strict();
const skillDraftPayloadSchema = z.object({
  name: z.string(), description: z.string(), instructions: z.string(),
}).strict();
const mcpRetrySchema = z.object({
  enabled: z.boolean(), maxRetries: z.number(), initialDelayMs: z.number(),
  maxDelayMs: z.number(), backoffFactor: z.number(),
}).strict();
const mcpPayloadSchema = z.object({
  name: z.string(), version: z.string(), transport: z.string(), command: z.string().optional(),
  args: z.array(z.string()).optional(), url: z.string().optional(),
  capabilities: z.array(z.string()).optional(), timeoutSec: z.number(), retry: mcpRetrySchema.optional(),
}).strict();
const knowledgePayloadSchema = z.object({
  name: z.string(), description: z.string(), embeddingModel: z.string(),
}).strict();

const proposalBase = {
  id: z.string(), conversationId: z.string().optional(), proposerId: z.string(), confirmerId: z.string().optional(),
  resourceId: z.string().optional(), operation: proposalOperationSchema,
  baselineFingerprint: z.string().optional(), summary: z.string(), status: proposalStatusSchema,
  errorCode: z.string().optional(), expiresAt: z.string(), createdAt: z.string(), updatedAt: z.string(),
  applyResult: z.object({
    resourceId: z.string().optional(), fingerprint: z.string().optional(), readback: z.unknown().optional(),
  }).optional(),
  events: z.array(z.object({
    id: z.string().optional(), proposalId: z.string().optional(), actorId: z.string(),
    fromStatus: proposalStatusSchema.optional(), toStatus: proposalStatusSchema,
    code: z.string().optional(), summary: z.string().optional(), createdAt: z.string(),
  })).nullish().transform((value) => value ?? []),
};

export const resourceChangeProposalSchema = z.discriminatedUnion('resourceKind', [
  z.object({ ...proposalBase, resourceKind: z.literal('agent'), payload: agentPayloadSchema }),
  z.object({ ...proposalBase, resourceKind: z.literal('skill_draft'), payload: skillDraftPayloadSchema }),
  z.object({ ...proposalBase, resourceKind: z.literal('mcp_config'), payload: mcpPayloadSchema }),
  z.object({ ...proposalBase, resourceKind: z.literal('knowledge_workspace'), payload: knowledgePayloadSchema }),
]);

export const resourceChangeProposalArtifactSchema = z.object({
  id: z.string(), resourceKind: z.enum(['agent', 'skill_draft', 'mcp_config', 'knowledge_workspace']),
  operation: proposalOperationSchema, status: proposalStatusSchema, summary: z.string(), expiresAt: z.string(),
}).strict();

export type ResourceChangeProposal = z.infer<typeof resourceChangeProposalSchema>;
export type ResourceChangeProposalArtifact = z.infer<typeof resourceChangeProposalArtifactSchema>;
export type ProposalPayload = ResourceChangeProposal['payload'];

export const TERMINAL_PROPOSAL_STATUSES = new Set<ResourceChangeProposal['status']>([
  'applied', 'invalid', 'stale', 'expired', 'failed', 'unknown_outcome', 'cancelled',
]);
