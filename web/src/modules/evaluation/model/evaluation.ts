import { z } from 'zod';

export const resourceKindSchema = z.enum(['skill', 'agent', 'mcp', 'knowledge']);
export type ResourceKind = z.infer<typeof resourceKindSchema>;

export const resourceRefSchema = z.object({
  kind: resourceKindSchema,
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
  resource_kind: resourceKindSchema,
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

export const errorResponseSchema = z.object({ error: z.string() }).strict();

const safeSummarySchema = z.object({
  resource_name: z.string().optional(),
  version_label: z.string().optional(),
  changed_fields: z.array(z.string()).optional(),
  change_type: z.string().optional(),
}).strict();

const page = <T extends z.ZodTypeAny>(item: T) => z.object({
  items: z.array(item),
  next_cursor: z.string().optional(),
}).strict();

export const centerOverviewSchema = z.object({
  resources: z.number(), suites: z.number(), runs: z.number(), candidates: z.number(), experiments: z.number(),
}).strict();
export type CenterOverview = z.infer<typeof centerOverviewSchema>;

export const resourceSummarySchema = z.object({
  id: z.string(), resource_id: z.string(), status: z.string(), stable_revision_id: z.string().optional(),
  latest_run_status: z.string().optional(), resource_kind: resourceKindSchema,
  safe_summary: safeSummarySchema.default({}), created_at: z.string(),
}).strict();
export const resourcePageSchema = page(resourceSummarySchema);
export type ResourcePage = z.infer<typeof resourcePageSchema>;

export const suiteSummarySchema = z.object({
  id: z.string(), name: z.string(), description: z.string(), status: z.string(), created_at: z.string(),
}).strict();
export const suitePageSchema = page(suiteSummarySchema);
export type SuitePage = z.infer<typeof suitePageSchema>;

export const runSummarySchema = z.object({
  id: z.string(), resource_id: z.string(), revision_id: z.string(), status: z.string(),
  resource_kind: resourceKindSchema, passed: z.boolean(), total_cases: z.number(), passed_cases: z.number(),
  created_at: z.string(),
}).strict();
export const runPageSchema = page(runSummarySchema);
export type RunPage = z.infer<typeof runPageSchema>;

export const candidateSummarySchema = z.object({
  id: z.string(), resource_id: z.string(), revision_id: z.string(), parent_revision_id: z.string(),
  source: z.string(), status: z.string(), resource_kind: resourceKindSchema, rank: z.number().optional(),
  state_version: z.number().int().positive(), safe_diff: safeSummarySchema.default({}), created_at: z.string(),
}).strict();
export const candidatePageSchema = page(candidateSummarySchema);
export type CandidatePage = z.infer<typeof candidatePageSchema>;

export const experimentGateSchema = z.enum(['passed', 'failed', 'pending', 'not_applicable']);
export const experimentSummarySchema = z.object({
  id: z.string(), resource_id: z.string(), stable_revision_id: z.string(), canary_revision_id: z.string(),
  status: z.string(), recommendation: z.string(), resource_kind: resourceKindSchema, stage_percent: z.number(),
  safety_stopped: z.boolean(), state_version: z.number().int().positive(), gates: z.object({
    quality: experimentGateSchema, cost: experimentGateSchema, latency: experimentGateSchema,
    error_rate: experimentGateSchema, security: experimentGateSchema,
  }).strict().optional(), created_at: z.string(),
}).strict();
export const experimentPageSchema = page(experimentSummarySchema);
export type ExperimentPage = z.infer<typeof experimentPageSchema>;

export const timelineEventSchema = z.object({
  id: z.string(), kind: z.string(), status: z.string(), summary: z.string(), resource_id: z.string(),
  resource_kind: resourceKindSchema, created_at: z.string(),
}).strict();
export const timelinePageSchema = page(timelineEventSchema);
export type TimelinePage = z.infer<typeof timelinePageSchema>;

export const evaluationCommandSchema = z.object({
  reason: z.string(), idempotency_key: z.string(), expected_state_version: z.number().int().positive(),
}).strict();
export type EvaluationCommand = z.infer<typeof evaluationCommandSchema>;

export interface EvaluationCenterFilters {
  resource_kind?: ResourceKind;
  resource_id?: string;
  status?: string;
  cursor?: string;
  limit?: number;
}
