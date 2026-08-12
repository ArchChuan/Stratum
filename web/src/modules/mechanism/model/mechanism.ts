import { z } from 'zod';

// 机制基线（model_profiles）管理面契约：模型族档案 + 生效基线。
// 管理面依附默认租户（tenant_default），消费路径透明取用同一份档案。

export const profileBaselineSchema = z.object({
  memory_extraction: z.string().optional().default(''),
  memory_summary: z.string().optional().default(''),
  memory_enrichment: z.string().optional().default(''),
  memory_summarize: z.string().optional().default(''),
  memory_supersede: z.string().optional().default(''),
  compaction: z.string().optional().default(''),
  enrich_model: z.string().optional().default(''),
  summary_model: z.string().optional().default(''),
});
export type ProfileBaseline = z.infer<typeof profileBaselineSchema>;

export const profileSchema = z
  .object({
    id: z.string(),
    family_key: z.string(),
    display_name: z.string().optional().default(''),
    family_prefixes: z.array(z.string()).default([]),
    fingerprint: z.string().optional().default(''),
    version: z.number().optional().default(0),
    status: z.enum(['active', 'draft']).optional(),
    created_by: z.string().optional().default(''),
    created_at: z.string().optional().default(''),
    updated_at: z.string().optional().default(''),
    baseline: profileBaselineSchema.default({}),
  })
  .passthrough();
export type Profile = z.infer<typeof profileSchema>;

export type ProfileStatus = 'active' | 'draft';

/** Upsert 请求体（family_key + family_prefixes 必填）。 */
export interface UpsertProfileInput {
  family_key: string;
  display_name?: string;
  family_prefixes: string[];
  status?: ProfileStatus;
  baseline?: Partial<ProfileBaseline>;
}
