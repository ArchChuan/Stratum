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

type JSONValue = string | number | boolean | null | JSONValue[] | { [key: string]: JSONValue };

const SENSITIVE_SUMMARY_KEYS = new Set([
  'payload', 'raw_payload', 'prompt', 'raw_prompt', 'credentials', 'credential', 'api_key', 'apikey', 'token',
  'access_token', 'refresh_token', 'retrieved_content', 'document_content', 'arguments', 'tool_arguments',
  'raw_response', 'tool_raw_response', 'encrypted_payload_ref', 'payload_ref', 'payload_hash', 'content_hash',
  'authorization', 'password', 'secret', 'private_key', 'client_secret', 'cookie', 'session', 'key', 'cert',
  'connection_string',
  'system_prompt', 'developer_prompt', 'api_token', 'bearer_token', 'retrieved_chunks',
]);

const normalizedKey = (key: string) => key
  .replace(/-/g, '_')
  .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
  .replace(/([A-Z]+)([A-Z][a-z])/g, '$1_$2')
  .toLowerCase();
const sensitiveSummaryAssignment = /(^|[^A-Za-z0-9_-])["']?(?:api[_-]?key|access[_-]?token|client[_-]?secret)["']?\s*[:=]\s*["']?\S/i;
const sensitiveSummaryAuthorization = /(^|[^A-Za-z0-9_-])["']?authorization["']?\s*[:=]\s*["']?(?:bearer|basic)\b/i;
const isSensitiveSummaryValue = (value: string) => sensitiveSummaryAssignment.test(value)
  || sensitiveSummaryAuthorization.test(value);
const validateSafeJSON = (value: unknown, path: string[], depth = 0): string | null => {
  if (depth > 6) return `${path.join('.')} exceeds safe summary depth`;
  if (value === null || typeof value === 'boolean') return null;
  if (typeof value === 'number') return Number.isFinite(value) ? null : `${path.join('.')} is not finite`;
  if (typeof value === 'string') {
    if (value.length > 2048) return `${path.join('.')} is too long`;
    return isSensitiveSummaryValue(value) ? `${path.join('.')} contains a sensitive value` : null;
  }
  if (Array.isArray(value)) {
    if (value.length > 64) return `${path.join('.')} has too many items`;
    for (let index = 0; index < value.length; index += 1) {
      const error = validateSafeJSON(value[index], [...path, String(index)], depth + 1);
      if (error) return error;
    }
    return null;
  }
  if (!value || typeof value !== 'object' || Object.getPrototypeOf(value) !== Object.prototype) {
    return `${path.join('.')} is not JSON-safe`;
  }
  const entries = Object.entries(value as Record<string, unknown>);
  if (entries.length > 64) return `${path.join('.')} has too many fields`;
  for (const [key, nested] of entries) {
    if (SENSITIVE_SUMMARY_KEYS.has(normalizedKey(key))) return `${[...path, key].join('.')} is sensitive`;
    const error = validateSafeJSON(nested, [...path, key], depth + 1);
    if (error) return error;
  }
  return null;
};

export const safeSummarySchema = z.unknown().superRefine((value, ctx) => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    ctx.addIssue({ code: z.ZodIssueCode.custom, message: 'safe summary must be an object' });
    return;
  }
  const error = validateSafeJSON(value, ['safe_summary']);
  if (error) ctx.addIssue({ code: z.ZodIssueCode.custom, message: error });
}).transform((value) => value as Record<string, JSONValue>);

const safeDiffValueSchema = z.unknown().superRefine((value, ctx) => {
  const error = validateSafeJSON(value, ['safe_diff']);
  if (error) ctx.addIssue({ code: z.ZodIssueCode.custom, message: error });
}).transform((value) => value as JSONValue | undefined);

export const candidateSafeDiffSchema = z.object({
  changed_fields: z.array(z.string().min(1).max(64)).max(32),
  changes: z.record(z.string().min(1).max(64),
    z.object({ before: safeDiffValueSchema.optional(), after: safeDiffValueSchema.optional() }).strict()),
  parent_missing: z.boolean(),
}).strict().superRefine((diff, ctx) => {
  const fields = diff.changed_fields;
  const keys = Object.keys(diff.changes);
  if (new Set(fields).size !== fields.length) {
    ctx.addIssue({ code: z.ZodIssueCode.custom, message: 'changed_fields must be unique' });
  }
  if (keys.length > 32 || [...fields].sort().join('\0') !== [...keys].sort().join('\0')) {
    ctx.addIssue({ code: z.ZodIssueCode.custom, message: 'changed_fields must match changes' });
  }
  for (const key of keys) {
    if (SENSITIVE_SUMMARY_KEYS.has(normalizedKey(key))) {
      ctx.addIssue({ code: z.ZodIssueCode.custom, message: `unsafe diff field: ${key}` });
    }
  }
});

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
  state_version: z.number().int().positive(), safe_diff: candidateSafeDiffSchema, created_at: z.string(),
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

export const candidateCommandResponseSchema = candidateSummarySchema;

const promotionPolicySchema = z.object({
  stages: z.array(z.number()), min_samples: z.number(), min_observation_minutes: z.number(),
  max_cost_regression: z.number(), max_latency_regression: z.number(), max_error_rate_increase: z.number(),
}).strict();
export const experimentCommandResponseSchema = z.object({
  id: z.string(), resource_kind: resourceKindSchema, resource_id: z.string(), stable_revision_id: z.string(),
  canary_revision_id: z.string(), suite_revision_id: z.string(), status: z.string(), stage: z.number(),
  policy: promotionPolicySchema, state_version: z.number().int().positive(), recommendation: z.string(),
  safety_stopped: z.boolean(),
}).strict();

export interface EvaluationCenterFilters {
  resource_kind?: ResourceKind;
  resource_id?: string;
  status?: string;
  cursor?: string;
  limit?: number;
}
