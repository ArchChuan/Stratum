// LLM provider and model types — API responses only, no Zod.
// apiKey is write-only on the backend and is NEVER returned.

export type ProviderKind = 'openai_compat' | 'anthropic' | 'ollama';

export interface Provider {
  id: string;
  name: string;
  kind: ProviderKind;
  baseUrl: string;
  defaultModel: string;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

export type ModelCapability = 'chat' | 'embedding' | 'vision' | 'tool_use' | 'reasoning';

export interface Model {
  id: string;
  providerId: string;
  name: string;
  displayName: string;
  capabilities: ModelCapability[];
  contextWindow: number;
  maxTokens: number;
  operatorContextWindow?: number;
  operatorMaxTokens?: number;
  defaultOutputTokens?: number;
  // fallbackCandidates 是平台为模型显式配置的降级候选模型名（有序，最优先在
  // 前）；未配置时由注册表按隐式规则补齐。
  fallbackCandidates?: string[];
  contextWindowSource?: string;
  maxTokensSource?: string;
  inputPrice: number;
  outputPrice: number;
  recommended: boolean;
  defaultEmbedding: boolean;
  enabled: boolean;
  providerManaged: boolean;
  // 运行时健康状态（healthy/degraded/unhealthy/half_open）；未探活时为 undefined。
  health?: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateProviderInput {
  name: string;
  kind: ProviderKind;
  baseUrl: string;
  apiKey: string;
}

export interface UpdateProviderInput {
  name: string;
  kind: ProviderKind;
  baseUrl: string;
  apiKey: string;
  defaultModel: string;
}

export interface UpdateModelInput {
  displayName: string;
  capabilities: ModelCapability[];
  contextWindow: number;
  maxTokens: number;
  inputPrice: number;
  outputPrice: number;
  recommended: boolean;
}

export interface UpdateModelPolicyInput {
  operatorContextWindow?: number | null;
  operatorMaxTokens?: number | null;
  defaultOutputTokens?: number | null;
  // fallbackCandidates PATCH 语义：undefined=不发送（保留现值）；null=保留；
  // 数组（含空）= 覆盖，空数组即清空显式候选、恢复纯隐式兜底。
  fallbackCandidates?: string[] | null;
}
