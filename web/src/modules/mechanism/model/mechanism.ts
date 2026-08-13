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
  extraction_model: z.string().optional().default(''),
  judge_model: z.string().optional().default(''),
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

// —— 评测矩阵工作台（阶段3）——

export const benchmarkSuiteSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string().optional().default(''),
  active_revision: z.string().optional().default(''),
  case_count: z.number().optional().default(0),
});
export type BenchmarkSuite = z.infer<typeof benchmarkSuiteSchema>;

export const matrixCellSchema = z.object({
  family_key: z.string(),
  display_name: z.string().optional().default(''),
  status: z.string().optional().default(''),
  fingerprint: z.string().optional().default(''),
  version: z.number().optional().default(0),
  enrich_model: z.string().optional().default(''),
  summary_model: z.string().optional().default(''),
  run_id: z.string().optional().default(''),
  passed: z.boolean().optional().default(false),
  pass_rate: z.number().optional().default(0),
  total_cost: z.number().optional().default(0),
  avg_latency: z.number().optional().default(0),
  total_cases: z.number().optional().default(0),
  frontier: z.boolean().optional().default(false),
});
export type MatrixCell = z.infer<typeof matrixCellSchema>;

export const matrixReportSchema = z.object({
  suites: z.array(benchmarkSuiteSchema).default([]),
  cells: z.array(matrixCellSchema).default([]),
  frontier_keys: z.array(z.string()).default([]),
});
export type MatrixReport = z.infer<typeof matrixReportSchema>;

export const runMatrixResultSchema = z.object({
  suite_revision_id: z.string().optional().default(''),
  triggered_count: z.number().optional().default(0),
});
export type RunMatrixResult = z.infer<typeof runMatrixResultSchema>;
