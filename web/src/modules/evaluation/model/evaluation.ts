import { z } from 'zod';

export const resourceRefSchema = z.object({
  kind: z.literal('skill'),
  resource_id: z.string(),
  revision_id: z.string(),
});
export type ResourceRef = z.infer<typeof resourceRefSchema>;

export const evaluationCaseSchema = z.object({
  id: z.string().optional(),
  name: z.string().optional().default(''),
  input: z.unknown(),
  expected_output: z.unknown(),
  assertion_mode: z.enum(['exact', 'contains', 'regex']),
  enabled: z.boolean().optional().default(true),
});
export type EvaluationCase = z.infer<typeof evaluationCaseSchema>;

export const suiteRevisionSchema = z.object({
  id: z.string(),
  suite_id: z.string(),
  version_no: z.number().optional(),
  status: z.string(),
  resource_kind: z.literal('skill'),
  cases: z.array(evaluationCaseSchema),
});
export type SuiteRevision = z.infer<typeof suiteRevisionSchema>;

export const createSuiteResponseSchema = z.object({
  suite: z.object({ id: z.string(), name: z.string(), draft_revision_id: z.string().optional() }),
  revision: suiteRevisionSchema,
});

export const evaluationJobSchema = z.object({
  job_id: z.string(),
  status: z.enum(['queued', 'running', 'succeeded', 'failed', 'cancelled']),
  error_message: z.string().optional(),
  result_id: z.string().optional(),
});
export type EvaluationJob = z.infer<typeof evaluationJobSchema>;

export const evaluationRunSchema = z.object({
  id: z.string(),
  resource: resourceRefSchema,
  suite_revision_id: z.string(),
  passed: z.boolean(),
  total_cases: z.number(),
  passed_cases: z.number(),
  results: z.array(
    z.object({
      case_id: z.string(),
      passed: z.boolean(),
      message: z.string().optional(),
      error: z.string().optional(),
      actual: z.unknown().optional(),
      trace_id: z.string().optional(),
      tokens: z.number().optional().default(0),
      cost_usd: z.number().optional().default(0),
      duration_ms: z.number().optional().default(0),
    }),
  ),
});
export type EvaluationRun = z.infer<typeof evaluationRunSchema>;

export const optimizationCandidateSchema = z.object({
  id: z.string(),
  optimization_job_id: z.string(),
  revision: resourceRefSchema,
  parent_revision_id: z.string(),
  source: z.string(),
  rationale: z.string().optional(),
});
export type OptimizationCandidate = z.infer<typeof optimizationCandidateSchema>;

export const optimizationResponseSchema = z.object({
  job: z.object({ id: z.string(), status: z.string() }).passthrough(),
  candidates: z.array(optimizationCandidateSchema),
});

export const experimentResponseSchema = z.object({
  experiment: z.object({ id: z.string(), status: z.string(), stage: z.number() }).passthrough(),
  deployment: z
    .object({ stable_revision_id: z.string(), canary_revision_id: z.string().optional(), canary_percent: z.number() })
    .passthrough(),
});
export type ExperimentResponse = z.infer<typeof experimentResponseSchema>;
