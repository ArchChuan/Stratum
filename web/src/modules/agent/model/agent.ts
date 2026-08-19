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
    compaction_prompt: z.string().optional(),
    compaction_temperature: z.number().optional(),
    compaction_model: z.string().optional(),
    reasoning_effort: z.string().optional(),
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
  temperature?: number;
  max_tokens?: number;
  compaction_recent_groups?: number;
  compaction_prompt?: string;
  compaction_temperature?: number;
  compaction_model?: string;
  reasoning_effort?: string;
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
  // 采样参数(agents.parameters JSONB,merge 语义:0=unset 不落库)
  temperature?: number;
  max_tokens?: number;
  compaction_recent_groups?: number;
  // 上下文压缩设置(顶层 DTO 字段,直接进 payload;字段名必须 snake_case 匹配后端
  // json tag。空/0 = const 兜底:prompt→内置默认、temp→0.3、model→跟随主 LLMModel)
  compaction_prompt?: string;
  compaction_temperature?: number;
  compaction_model?: string;
  reasoning_effort?: string;
  // 记忆注入/提取/召回参数(agents.parameters JSONB 的 memory.* dotted 键,
  // 提交时经 buildMemoryParameters 映射;null/undefined 不落库)
  memoryMaxFactsPerExtraction?: number;
  memoryFactInjectionTopN?: number;
  memoryHistoryInjectionTopN?: number;
  memoryExtractionPrompt?: string;
  memoryExtractionModel?: string;
  memoryRecallTopK?: number;
  // registry 资源级参数的透传对象,只写 memory.* dotted 键
  parameters?: Record<string, unknown>;
  allowedSkills?: string[];
  mcpToolIds?: string[];
  knowledgeWorkspaceIds?: string[];
  memoryScope?: string;
  editors?: string[];
}

// buildMemoryParameters 把表单上的 memory.* 字段映射为 agents.parameters JSONB
// 的 dotted 键。与后端 validateAndExtractMemoryParameters 的 0=unset 语义一致:
// null/undefined 不产生键,空对象表示无 memory 覆盖(后端不落库)。
export type MemoryParamValues = Pick<
  AgentFormValues,
  | 'memoryMaxFactsPerExtraction'
  | 'memoryFactInjectionTopN'
  | 'memoryHistoryInjectionTopN'
  | 'memoryExtractionPrompt'
  | 'memoryExtractionModel'
  | 'memoryRecallTopK'
>;
export const buildMemoryParameters = (values: MemoryParamValues): Record<string, unknown> => {
  const params: Record<string, unknown> = {};
  if (values.memoryMaxFactsPerExtraction != null) {
    params['memory.max_facts_per_extraction'] = values.memoryMaxFactsPerExtraction;
  }
  if (values.memoryFactInjectionTopN != null) {
    params['memory.fact_injection_top_n'] = values.memoryFactInjectionTopN;
  }
  if (values.memoryHistoryInjectionTopN != null) {
    params['memory.history_injection_top_n'] = values.memoryHistoryInjectionTopN;
  }
  if (values.memoryExtractionPrompt != null) {
    params['memory.extraction_prompt'] = values.memoryExtractionPrompt;
  }
  if (values.memoryExtractionModel != null) {
    params['memory.extraction_model'] = values.memoryExtractionModel;
  }
  if (values.memoryRecallTopK != null) {
    params['memory.recall_top_k'] = values.memoryRecallTopK;
  }
  return params;
};

export interface GroupedModelOption {
  provider: string;
  models: { value: string; label: string; reasoning: boolean; contextWindow?: number; maxTokens?: number }[];
}

// 按厂商聚合模型下拉选项（创建/编辑 Agent 页共用）。
// capabilities 可选：缺失时按默认推理名单推断（见 isReasoningModel），
// 避免调用方为拿能力标志额外加一次请求。
export const buildGroupedModels = (
  models: Array<{ providerId: string; name: string; displayName?: string; capabilities?: string[]; contextWindow?: number; maxTokens?: number }>,
  providers: Array<{ id: string; name: string }>,
): GroupedModelOption[] => {
  const providerMap = new Map(providers.map((p) => [p.id, p.name]));
  const grouped = new Map<string, { value: string; label: string; reasoning: boolean; contextWindow?: number; maxTokens?: number }[]>();
  for (const m of models) {
    const providerName = providerMap.get(m.providerId) || m.providerId;
    if (!grouped.has(providerName)) grouped.set(providerName, []);
    grouped.get(providerName)!.push({
      value: m.name,
      label: m.displayName || m.name,
      reasoning: isReasoningModel(m),
      contextWindow: m.contextWindow,
      maxTokens: m.maxTokens,
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

// NoAnswerReason 与后端 pkg/constants 的 NoAnswerReason 枚举逐值对齐
// （跨 context 单一事实源，值进入响应契约与指标 label）。
export const noAnswerReasons = [
  'no_sources',
  'threshold_filtered',
  'access_restricted',
  'insufficient_evidence',
  'unsupported_mode',
] as const;
export type NoAnswerReason = (typeof noAnswerReasons)[number];

// NoAnswerInfo 是 SSE done payload 的 noAnswer 信号（JSON 字段名 PascalCase：
// 后端 domain.NoAnswerInfo 无 tag，与 ChatCitationSource 同一序列化规则）。
// nil=有答案（omitempty 不输出键）；非 nil=无答案且 reason 说明原因。
export interface NoAnswerInfo {
  Reason: NoAnswerReason;
  RetrievedCount?: number;
  FilteredCount?: number;
  BestScore?: number;
  Retried?: boolean;
  RewrittenQuery?: string;
  Detail?: string;
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
/** 后端 TaskSnapshot 的 JSON 形态（camelCase 与 Go 对齐） */
export interface TaskSnapshot {
  goal: string;
  currentPhase: string;
  completedSteps: string[];
  nextAction: string;
  status: 'active' | 'completed' | 'abandoned';
  failures?: number;
}
export interface ChatMessage {
  id?: string;
  role: string;
  content: string;
  created_at?: string;
  steps?: ChatStep[];
  artifacts?: ExecutionArtifact[];
  interrupted?: boolean;
  sources?: ChatCitationSource[];
  /** 无答案结构化信号（nil/缺失=有答案或旧后端）；用于渲染拒答提示 */
  noAnswer?: NoAnswerInfo;
  /** 跨会话目标进度摘要（stratum_task_snapshot 透出）；无则 undefined */
  taskSnapshot?: TaskSnapshot;
  [key: string]: unknown;
}

// query/context/variables 来自 proto 契约(gen);conversation_id 是 wire-only 字段
// (后端 handler.ExecuteAgentRequest 绑定并用于会话连续性,dto 契约无此字段,parity 冻结)。
// execution_id 同为 wire-only:断线续发时 SSE 首帧下发恢复键,前端保存并在断线
// 重发请求中原样带回,供后端 resumeFromCheckpoint 定位检查点续跑。
export interface ExecuteAgentPayload extends GenExecuteAgentRequest {
  conversation_id?: string;
  execution_id?: string;
}

export interface AgentExecutionResult {
  output?: string;
  steps?: ChatStep[];
  artifacts?: ExecutionArtifact[];
  sources?: ChatCitationSource[];
  /** 无答案结构化信号：nil/缺失=有答案（omitempty），渲染拒答提示用 */
  noAnswer?: NoAnswerInfo;
  error?: string;
  metadata?: Record<string, unknown>;  // SSE done 白名单透出（thoughtsJSON/toolCallsJSON/stratum_task_snapshot）
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
	// 首帧恢复键(断线续接协议):SSE 首帧下发 execution_id,捕获后断线重发时
	// 原样带回;仅存内存供消费方读取,不持久化。
	onExecutionId?: (executionId: string) => void;
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
