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

export interface SkillFormValues {
  name: string;
  description?: string;
  type: SkillType;
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
