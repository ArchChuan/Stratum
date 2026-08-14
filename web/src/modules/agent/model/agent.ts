import { z } from 'zod';

import { resourceChangeProposalArtifactSchema } from './proposal';

import type { ExecuteAgentRequest as GenExecuteAgentRequest } from '@/services/gen/agent';

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
    temperature: z.number().optional(),
    max_tokens: z.number().optional(),
    compaction_recent_groups: z.number().optional(),
    compaction_safety_ratio: z.number().optional(),
    reasoning_effort: z.string().optional(),
    allowedSkills: z.array(z.string()).nullish().transform((v) => v ?? []),
    mcpToolIds: z.array(z.string()).nullish().transform((v) => v ?? []),
    knowledgeWorkspaceIds: z.array(z.string()).nullish().transform((v) => v ?? []),
    memoryScope: z.string().optional().default('user'),
    checkpointEnabled: z.boolean().optional().default(false),
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
  temperature?: number;
  max_tokens?: number;
  compaction_recent_groups?: number;
  compaction_safety_ratio?: number;
  reasoning_effort?: string;
  allowedSkills: string[];
  mcpToolIds: string[];
  knowledgeWorkspaceIds: string[];
  memoryScope: string;
  checkpointEnabled?: boolean;
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
  // 采样参数(agents.parameters JSONB,merge 语义:0=unset 不落库)
  temperature?: number;
  max_tokens?: number;
  compaction_recent_groups?: number;
  compaction_safety_ratio?: number;
  reasoning_effort?: string;
  allowedSkills?: string[];
  mcpToolIds?: string[];
  knowledgeWorkspaceIds?: string[];
  memoryScope?: string;
  checkpointEnabled?: boolean;
  editors?: string[];
}

export interface GroupedModelOption {
  provider: string;
  models: { value: string; label: string; reasoning: boolean; contextWindow?: number }[];
}

// 按厂商聚合模型下拉选项（创建/编辑 Agent 页共用）。
// capabilities 可选：缺失时按默认推理名单推断（见 isReasoningModel），
// 避免调用方为拿能力标志额外加一次请求。
export const buildGroupedModels = (
  models: Array<{ providerId: string; name: string; displayName?: string; capabilities?: string[]; contextWindow?: number }>,
  providers: Array<{ id: string; name: string }>,
): GroupedModelOption[] => {
  const providerMap = new Map(providers.map((p) => [p.id, p.name]));
  const grouped = new Map<string, { value: string; label: string; reasoning: boolean; contextWindow?: number }[]>();
  for (const m of models) {
    const providerName = providerMap.get(m.providerId) || m.providerId;
    if (!grouped.has(providerName)) grouped.set(providerName, []);
    grouped.get(providerName)!.push({
      value: m.name,
      label: m.displayName || m.name,
      reasoning: isReasoningModel(m),
      contextWindow: m.contextWindow,
    });
  }
  return Array.from(grouped.entries()).map(([provider, modelOptions]) => ({
    provider,
    models: modelOptions,
  }));
};

// 推理模型判定：优先用 catalog/DB 返回的 capabilities，缺失时按命名规则兜底
// （o1/o3/o4/deepseek-reasoner/qwq 等）。与后端 pkg/constants 的
// reasoning 能力标注保持一致；未知模型前端默认视为非推理（fail-closed）。
const isReasoningModel = (
  m: { name: string; capabilities?: string[] },
): boolean => {
  if (m.capabilities?.length) {
    return m.capabilities.includes('reasoning');
  }
  return /^(o1|o3|o4|deepseek-reasoner|qwq)/i.test(m.name);
};

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
  resourceChangeProposal: resourceChangeProposalArtifactSchema.nullish().transform((v) => v ?? undefined),
});
export type ExecutionArtifact = z.infer<typeof executionArtifactSchema>;

// ChatCitationSource is a retrieval provenance entry carried by the SSE done
// payload (JSON field names are PascalCase: the backend serializes
// domain.RAGSearchSource without tags). Each entry points at one chunk of a
// source document the assistant grounded its answer on.
export interface ChatCitationSource {
  WorkspaceID?: string;
  WorkspaceName?: string;
  ChunkID?: string;
  DocumentID?: string;
  DocumentTitle?: string;
  Snippet?: string;
  Score?: number;
  HasScore?: boolean;
}

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
  sources?: ChatCitationSource[];
  [key: string]: unknown;
}

// query/context/variables 来自 proto 契约(gen);conversation_id 是 wire-only 字段
// (后端 handler.ExecuteAgentRequest 绑定并用于会话连续性,dto 契约无此字段,parity 冻结)。
export interface ExecuteAgentPayload extends GenExecuteAgentRequest {
  conversation_id?: string;
}

export interface AgentExecutionResult {
  output?: string;
  steps?: ChatStep[];
  artifacts?: ExecutionArtifact[];
  sources?: ChatCitationSource[];
  error?: string;
  [key: string]: unknown;
}

export interface AgentExecutionFailure {
  message: string;
  code?: string;
  status?: number;
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
	status: 'pending' | 'approved' | 'rejected' | 'expired' | 'unknown_outcome' | 'authorization_denied' | 'cancelled' | 'voided' | 'invalidated' | string;
	expiresAt?: string;
	invalidationReason?: string;
}

export interface ToolApprovalResumeResult {
  status: 'completed';
  output: string;
  steps: number;
  tokensUsed: number;
}
