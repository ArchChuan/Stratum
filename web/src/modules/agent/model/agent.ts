import { z } from 'zod';

export const agentSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    description: z.string().optional().default(''),
    type: z.string().optional().default('react'),
    systemPrompt: z.string().optional().default(''),
    llmModel: z.string().optional().default(''),
    maxIterations: z.number().optional(),
    maxContextTokens: z.number().optional(),
    allowedSkills: z.array(z.string()).nullish().transform((v) => v ?? []),
    mcpToolIds: z.array(z.string()).nullish().transform((v) => v ?? []),
    knowledgeWorkspaceIds: z.array(z.string()).nullish().transform((v) => v ?? []),
    memoryScope: z.string().optional().default('user'),
    isSystem: z.boolean().optional().default(false),
    managementMode: z.string().optional().default(''),
    created_at: z.string().optional(),
    updated_at: z.string().optional(),
  })
  .passthrough();
export interface Agent {
  id: string;
  name: string;
  description: string;
  type: string;
  systemPrompt: string;
  llmModel: string;
  maxIterations?: number;
  maxContextTokens?: number;
  allowedSkills: string[];
  mcpToolIds: string[];
  knowledgeWorkspaceIds: string[];
  memoryScope: string;
  isSystem?: boolean;
  managementMode?: string;
  created_at?: string;
  updated_at?: string;
  [key: string]: unknown;
}

export interface AgentFormValues {
  name: string;
  description?: string;
  systemPrompt?: string;
  llmModel: string;
  maxIterations: number;
  maxContextTokens: number;
  allowedSkills?: string[];
  mcpToolIds?: string[];
  knowledgeWorkspaceIds?: string[];
  memoryScope?: string;
}

export const conversationSchema = z
  .object({
    id: z.string(),
    name: z.string().optional().default(''),
    agent_id: z.string().optional(),
    created_at: z.string().optional(),
    updated_at: z.string().optional(),
  })
  .passthrough();
export type Conversation = z.infer<typeof conversationSchema>;

export const chatStepSchema = z
  .object({
    type: z.string().optional(),
    tool: z.string().optional(),
    input: z.unknown().optional(),
    output: z.unknown().optional(),
    thought: z.string().optional(),
    duration_ms: z.number().optional(),
  })
  .passthrough();
export type ChatStep = z.infer<typeof chatStepSchema>;

export const citationSchema = z.object({
  documentId: z.string(),
  title: z.string(),
  productVersion: z.string(),
  section: z.string(),
  url: z.string(),
  excerpt: z.string(),
});
export type Citation = z.infer<typeof citationSchema>;

export const diagnosticFactSchema = z.object({
  area: z.string(),
  objectId: z.string().optional(),
  statement: z.string(),
  source: z.string(),
  observedAt: z.string(),
});

export const evidenceGapSchema = z.object({
  area: z.string().optional(),
  source: z.string().optional(),
  code: z.string(),
});

export const diagnosticStepSchema = z.object({
  tool: z.string(),
  outcome: z.string(),
  errorCode: z.string().optional(),
  latencyMs: z.number(),
});

export const diagnosticReportSchema = z.object({
  facts: z.array(diagnosticFactSchema).nullish().transform((v) => v ?? []),
  inferences: z.array(z.string()).nullish().transform((v) => v ?? []),
  evidenceGaps: z.array(evidenceGapSchema).nullish().transform((v) => v ?? []),
  recommendedActions: z.array(z.string()).nullish().transform((v) => v ?? []),
  citations: z.array(citationSchema).nullish().transform((v) => v ?? []),
  steps: z.array(diagnosticStepSchema).nullish().transform((v) => v ?? []),
});
export type DiagnosticReport = z.infer<typeof diagnosticReportSchema>;

export const executionArtifactSchema = z.object({
  type: z.string(),
  profileVersion: z.string().optional(),
  citations: z.array(citationSchema).nullish().transform((v) => v ?? []),
  diagnosticReport: diagnosticReportSchema.nullish().transform((v) => v ?? undefined),
});
export type ExecutionArtifact = z.infer<typeof executionArtifactSchema>;

export const chatMessageSchema = z
  .object({
    id: z.string().optional(),
    role: z.string(),
    content: z.string().optional().default(''),
    created_at: z.string().optional(),
    steps: z.array(chatStepSchema).optional(),
    artifacts: z.array(executionArtifactSchema).nullish().transform((v) => v ?? []),
    interrupted: z.boolean().optional(),
  })
  .passthrough();
export interface ChatMessage {
  id?: string;
  role: string;
  content: string;
  created_at?: string;
  steps?: ChatStep[];
  artifacts?: ExecutionArtifact[];
  interrupted?: boolean;
  [key: string]: unknown;
}

export interface ExecuteAgentPayload {
  query: string;
  context?: Record<string, unknown>;
  variables?: Record<string, unknown>;
  conversation_id?: string;
}

export interface AgentExecutionResult {
  output?: string;
  steps?: ChatStep[];
  artifacts?: ExecutionArtifact[];
  error?: string;
  [key: string]: unknown;
}

export const systemAssistantSettingsSchema = z.object({
  agentId: z.string(),
  llmModel: z.string(),
  ready: z.boolean(),
  availableModels: z.array(z.string()),
});
export type SystemAssistantSettings = z.infer<typeof systemAssistantSettingsSchema>;

export interface StreamCallbacks {
  onToken: (token: string) => void;
  onDone: (data: AgentExecutionResult) => void;
	onError: (err: Error) => void;
	onApprovalRequired: (approval: ToolApproval) => void;
}

export interface ToolApproval {
	approvalId: string;
	agentId?: string;
	toolName: string;
	serverId: string;
	riskLevel: string;
	status: 'pending' | 'approved' | 'rejected' | 'expired' | 'unknown_outcome' | 'authorization_denied' | string;
	expiresAt?: string;
}

export interface ToolApprovalResumeResult {
  status: 'completed';
  output: string;
  steps: number;
  tokensUsed: number;
}
