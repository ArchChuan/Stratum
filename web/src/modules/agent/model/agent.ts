import { z } from 'zod';

export const agentSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    description: z.string().optional().default(''),
    type: z.string().optional().default('react'),
    persona: z.string().optional().default(''),
    systemPrompt: z.string().optional().default(''),
    llmModel: z.string().optional().default(''),
    maxIterations: z.number().optional(),
    maxContextTokens: z.number().optional(),
    allowedSkills: z.array(z.string()).nullish().transform((v) => v ?? []),
    mcpServerIds: z.array(z.string()).nullish().transform((v) => v ?? []),
    knowledgeWorkspaceIds: z.array(z.string()).nullish().transform((v) => v ?? []),
    created_at: z.string().optional(),
    updated_at: z.string().optional(),
  })
  .passthrough();
export type Agent = z.infer<typeof agentSchema>;

export interface AgentFormValues {
  name: string;
  description?: string;
  type?: string;
  persona?: string;
  systemPrompt?: string;
  llmModel: string;
  maxIterations: number;
  maxContextTokens: number;
  allowedSkills?: string[];
  mcpServerIds?: string[];
  knowledgeWorkspaceIds?: string[];
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

export const chatMessageSchema = z
  .object({
    id: z.string().optional(),
    role: z.string(),
    content: z.string().optional().default(''),
    created_at: z.string().optional(),
    steps: z.array(chatStepSchema).optional(),
  })
  .passthrough();
export type ChatMessage = z.infer<typeof chatMessageSchema>;

export interface ExecuteAgentPayload {
  query: string;
  context?: Record<string, unknown>;
  variables?: Record<string, unknown>;
  conversation_id?: string;
}

export interface AgentExecutionResult {
  output?: string;
  steps?: ChatStep[];
  error?: string;
  [key: string]: unknown;
}

export interface StreamCallbacks {
  onToken: (token: string) => void;
  onDone: (data: AgentExecutionResult) => void;
  onError: (err: Error) => void;
}
