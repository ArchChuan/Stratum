import { z } from 'zod';

export const collabStrategySchema = z.enum(['sequential', 'parallel', 'swarm', 'pipeline', 'hierarchical']);
export type CollabStrategy = z.infer<typeof collabStrategySchema>;

export const collabStatusSchema = z.enum(['created', 'running', 'completed', 'failed', 'canceled']);
export type CollabStatus = z.infer<typeof collabStatusSchema>;

export const taskStepStatusSchema = z.enum(['pending', 'claimed', 'running', 'completed', 'failed', 'canceled']);
export type TaskStepStatus = z.infer<typeof taskStepStatusSchema>;

export const collaborationSchema = z.object({
  id: z.string(),
  taskDescription: z.string(),
  strategy: collabStrategySchema,
  status: collabStatusSchema,
  createdBy: z.string(),
  participants: z.array(z.string()),
  createdAt: z.string(),
  startedAt: z.string().optional(),
  completedAt: z.string().optional(),
});
export type Collaboration = z.infer<typeof collaborationSchema>;

export const collaborationListSchema = z.object({
  collaborations: z.array(collaborationSchema),
});
export type CollaborationList = z.infer<typeof collaborationListSchema>;

export const taskStepSchema = z.object({
  id: z.string(),
  planId: z.string(),
  agentId: z.string(),
  dependencies: z.array(z.string()),
  status: taskStepStatusSchema,
  input: z.record(z.unknown()),
  output: z.record(z.unknown()).optional(),
  error: z.string().optional(),
  createdAt: z.string(),
});
export type TaskStep = z.infer<typeof taskStepSchema>;

export const collaborationDetailSchema = z.object({
  collaboration: collaborationSchema,
  steps: z.array(taskStepSchema),
});
export type CollaborationDetail = z.infer<typeof collaborationDetailSchema>;
