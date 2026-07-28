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
  tenantId: string;
  providerId: string;
  name: string;
  displayName: string;
  capabilities: ModelCapability[];
  contextWindow: number;
  maxTokens: number;
  inputPrice: number;
  outputPrice: number;
  recommended: boolean;
  enabled: boolean;
  providerManaged: boolean;
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
  name?: string;
  kind?: ProviderKind;
  baseUrl?: string;
  apiKey?: string;
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
