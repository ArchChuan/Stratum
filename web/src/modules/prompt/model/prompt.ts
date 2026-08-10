import { z } from 'zod';

// Prompt 模板列表行（GET /prompts）：每个 key 的最新版本摘要。
// 与 handler ListPrompts 渲染字段精确对齐。
export const promptSummarySchema = z
  .object({
    key: z.string(),
    latest_version: z.number(),
    latest_status: z.string(),
    created_at: z.string(),
  })
  .passthrough();
export type PromptSummary = z.infer<typeof promptSummarySchema>;

// 完整模板（GET /prompts/:key/versions 中的版本条目）。
// key 是主键，无 id 字段；status ∈ draft|published|archived。
export const promptTemplateSchema = z
  .object({
    key: z.string(),
    tenant_id: z.string().nullable().optional(),
    version: z.number(),
    content: z.string(),
    status: z.string(),
    content_hash: z.string(),
    created_by: z.string().optional().default(''),
    created_at: z.string(),
  })
  .passthrough();
export type PromptTemplate = z.infer<typeof promptTemplateSchema>;

// A/B 绑定（GET /prompts/bindings）。
// stable_version_id / canary_version_id 语义 = 版本对象的 content_hash（内容寻址）。
export const promptBindingSchema = z
  .object({
    key: z.string(),
    scope: z.string(), // tenant:<id> | agent:<id>
    stable_version_id: z.string(),
    canary_version_id: z.string().optional().default(''),
    traffic_percent: z.number().optional().default(0),
  })
  .passthrough();
export type PromptBinding = z.infer<typeof promptBindingSchema>;

export const PROMPT_STATUS_LABELS: Record<string, string> = {
  draft: '草稿',
  published: '已发布',
  archived: '已归档',
};

export const PROMPT_STATUS_COLORS: Record<string, string> = {
  draft: 'default',
  published: 'green',
  archived: 'orange',
};
