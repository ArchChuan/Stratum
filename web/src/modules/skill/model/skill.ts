import { z } from 'zod';

export const skillTypeSchema = z.enum(['code', 'llm', 'http', 'prompt']);
export type SkillType = z.infer<typeof skillTypeSchema>;

export const skillConfigSchema = z
  .object({
    code: z.string().optional(),
    language: z.string().optional(),
    system_prompt: z.string().optional(),
    model: z.string().optional(),
    temperature: z.number().optional(),
    max_tokens: z.number().optional(),
    url: z.string().optional(),
    method: z.string().optional(),
    timeout_sec: z.number().optional(),
    headers: z.record(z.string()).optional(),
    body_template: z.string().optional(),
    prompt_template: z.string().optional(),
  })
  .passthrough();
export type SkillConfig = z.infer<typeof skillConfigSchema>;

export const skillSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    description: z.string().optional().default(''),
    type: z.string(),
    config: skillConfigSchema.optional(),
    created_at: z.string().optional(),
    updated_at: z.string().optional(),
  })
  .passthrough();
export type Skill = z.infer<typeof skillSchema>;

const jsonObjectSchema = z.record(z.unknown());

export const skillProductSchema = z
  .object({
    id: z.string(),
    name: z.string(),
    description: z.string().optional().default(''),
    status: z.string(),
    activeVersionId: z.string().optional(),
    draftVersionId: z.string().optional(),
  })
  .passthrough();
export type SkillProduct = z.infer<typeof skillProductSchema>;

export const skillVersionSchema = z
  .object({
    id: z.string(),
    skillId: z.string(),
    versionNo: z.number().optional(),
    status: z.string(),
    capability: jsonObjectSchema.default({}),
    toolContract: jsonObjectSchema.default({}),
    implementation: jsonObjectSchema.default({}),
    testBaseline: jsonObjectSchema.optional(),
    publishChecks: jsonObjectSchema.optional(),
  })
  .passthrough();
export type SkillVersion = z.infer<typeof skillVersionSchema>;

export const skillWorkspaceSchema = z
  .object({
    skill: skillProductSchema,
    draft: skillVersionSchema,
  })
  .passthrough();
export type SkillWorkspace = z.infer<typeof skillWorkspaceSchema>;

export const skillTestResultSchema = z
  .object({
    result: z.unknown().optional(),
    error: z.string().optional(),
    traceID: z.string().optional(),
    durationMs: z.number().optional(),
  })
  .passthrough();
export type SkillTestResult = z.infer<typeof skillTestResultSchema>;

export interface SkillFormValues {
  name: string;
  description?: string;
  goal?: string;
  whenToUse?: string;
  sampleInput?: string;
  expectedInput?: string;
  expectedOutput?: string;
  sampleCases?: string;
  type?: SkillType;
  language?: string;
  code?: string;
  systemPrompt?: string;
  model?: string;
  temperature?: number;
  maxTokens?: number;
  url?: string;
  method?: string;
  timeoutSec?: number;
  headersJson?: string;
  headers?: Record<string, string>;
  bodyTemplate?: string;
  promptTemplate?: string;
}

export const buildCreateSkillPayload = (values: SkillFormValues): SkillFormValues => {
  const payload: SkillFormValues = { ...values };
  if (payload.type === 'http' && payload.headersJson) {
    payload.headers = JSON.parse(payload.headersJson);
    delete payload.headersJson;
  }
  return payload;
};

export interface CreateSkillDraftPayload {
  name: string;
  goal: string;
  whenToUse: string;
  sampleInput: string;
  expectedOutput: string;
}

export const buildCreateSkillDraftPayload = (values: SkillFormValues): CreateSkillDraftPayload => ({
  name: values.name,
  goal: values.goal || '',
  whenToUse: values.whenToUse || '',
  sampleInput: values.sampleInput || '',
  expectedOutput: values.expectedOutput || '',
});

export interface DraftSkillTestPayload {
  skill: SkillFormValues;
  input: unknown;
}

export const buildDraftSkillTestPayload = (
  values: SkillFormValues,
  rawInput: string,
): DraftSkillTestPayload => ({
  skill: buildCreateSkillPayload(values),
  input: parseSkillTestInput(rawInput),
});

export const parseSkillTestInput = (raw: string): unknown => {
  const trimmed = raw.trim();
  if (!trimmed) return '';
  try {
    return JSON.parse(trimmed);
  } catch {
    return raw;
  }
};
