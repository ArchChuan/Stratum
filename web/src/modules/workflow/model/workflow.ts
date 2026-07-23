import { z } from 'zod';

export const workflowNodeTypeSchema = z.enum(['agent', 'skill', 'mcp_tool', 'condition', 'approval']);
export type WorkflowNodeType = z.infer<typeof workflowNodeTypeSchema>;

export const workflowEffectClassSchema = z.enum(['pure', 'idempotent', 'non_idempotent']);

export const workflowRetrySchema = z.object({
  max_attempts: z.number().int().nonnegative().optional().default(0),
  backoff_ms: z.number().int().nonnegative().optional().default(0),
});

const workflowNodeBase = z.object({
  id: z.string().min(1),
  name: z.string().optional().default(''),
  input_mapping: z.record(z.string()).optional().default({}),
  output_mapping: z.record(z.string()).optional().default({}),
  retry: workflowRetrySchema.optional().default({ max_attempts: 0, backoff_ms: 0 }),
  timeout_ms: z.number().int().nonnegative().optional().default(0),
});

export const workflowNodeSchema = z.discriminatedUnion('type', [
  workflowNodeBase.extend({ type: z.literal('agent'), agent_id: z.string().min(1) }),
  workflowNodeBase.extend({
    type: z.literal('skill'),
    agent_id: z.string().min(1),
    skill_id: z.string().min(1),
    skill_revision_id: z.string().min(1),
  }),
  workflowNodeBase.extend({
    type: z.literal('mcp_tool'),
    agent_id: z.string().optional().default(''),
    mcp_server_id: z.string().min(1),
    mcp_tool_name: z.string().min(1),
    effect_class: workflowEffectClassSchema,
  }),
  workflowNodeBase.extend({
    type: z.literal('condition'),
    agent_id: z.string().optional().default(''),
    condition: z.string().min(1),
  }),
  workflowNodeBase.extend({
    type: z.literal('approval'),
    agent_id: z.string().optional().default(''),
  }),
]);
export type WorkflowNode = z.infer<typeof workflowNodeSchema>;

export const workflowEdgeSchema = z.object({
  id: z.string().optional().default(''),
  from: z.string().min(1),
  to: z.string().min(1),
  condition_value: z.boolean().optional(),
  default: z.boolean().optional().default(false),
});
export type WorkflowEdge = z.infer<typeof workflowEdgeSchema>;

export const workflowSpecSchema = z.object({
  nodes: z.array(workflowNodeSchema),
  edges: z.array(workflowEdgeSchema),
  max_concurrency: z.number().int().nonnegative().optional().default(0),
});
export type WorkflowSpec = z.infer<typeof workflowSpecSchema>;

export const workflowInputFieldTypeSchema = z.enum([
  'short_text',
  'long_text',
  'number',
  'single_select',
  'multi_select',
  'boolean',
  'date',
]);
export type WorkflowInputFieldType = z.infer<typeof workflowInputFieldTypeSchema>;

export const workflowInputOptionSchema = z.object({ label: z.string().min(1), value: z.string().min(1) });

export const workflowInputFieldSchema = z.object({
  key: z.string().min(1),
  label: z.string().min(1),
  type: workflowInputFieldTypeSchema,
  required: z.boolean().optional().default(false),
  description: z.string().optional().default(''),
  default: z.unknown().optional(),
  options: z.array(workflowInputOptionSchema).optional().default([]),
});
export type WorkflowInputField = z.infer<typeof workflowInputFieldSchema>;

export const workflowInputSchemaSchema = z.object({
  task_label: z.string().min(1),
  task_description: z.string().optional().default(''),
  fields: z.array(workflowInputFieldSchema).optional().default([]),
});
export type WorkflowInputSchema = z.infer<typeof workflowInputSchemaSchema>;

export const workflowDefinitionSchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  description: z.string(),
  revision: z.number().int().positive(),
  spec: workflowSpecSchema,
  input_schema: workflowInputSchemaSchema,
  created_at: z.string(),
  updated_at: z.string(),
});
export type WorkflowDefinition = z.infer<typeof workflowDefinitionSchema>;

export const workflowVersionSchema = z.object({
  id: z.string().min(1),
  definition_id: z.string().min(1),
  version: z.number().int().positive(),
  name: z.string().min(1),
  description: z.string(),
  spec: workflowSpecSchema,
  input_schema: workflowInputSchemaSchema,
  created_at: z.string(),
});
export type WorkflowVersion = z.infer<typeof workflowVersionSchema>;

export const workflowSummarySchema = z.object({
  id: z.string().min(1),
  name: z.string().min(1),
  description: z.string(),
  revision: z.number().int().positive(),
  updated_at: z.string(),
});
export type WorkflowSummary = z.infer<typeof workflowSummarySchema>;

export const workflowVersionSummarySchema = z.object({
  id: z.string().min(1),
  definition_id: z.string().min(1),
  version: z.number().int().positive(),
  name: z.string().min(1),
  description: z.string(),
  created_at: z.string(),
});

export const workflowPageSchema = z.object({
  workflows: z.array(workflowSummarySchema),
  total: z.number().int().nonnegative(),
  page: z.number().int().positive(),
  page_size: z.number().int().positive(),
});
export type WorkflowPage = z.infer<typeof workflowPageSchema>;

export const workflowVersionPageSchema = z.object({
  versions: z.array(workflowVersionSummarySchema),
  total: z.number().int().nonnegative(),
  page: z.number().int().positive(),
  page_size: z.number().int().positive(),
});
export type WorkflowVersionPage = z.infer<typeof workflowVersionPageSchema>;

export const validationIssueSchema = z.object({
  field: z.string().optional(),
  path: z.string().optional(),
  node_id: z.string().optional(),
  edge_id: z.string().optional(),
  code: z.string(),
  message: z.string(),
});
export type ValidationIssue = z.infer<typeof validationIssueSchema>;

export type WorkflowDraftPayload = Pick<WorkflowDefinition, 'name' | 'description' | 'spec' | 'input_schema'>;
