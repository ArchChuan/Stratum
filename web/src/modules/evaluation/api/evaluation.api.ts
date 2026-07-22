import { z } from 'zod';

import {
  createSuiteResponseSchema,
  evaluationJobSchema,
  evaluationRunSchema,
  experimentResponseSchema,
  optimizationResponseSchema,
  suiteRevisionSchema,
  type EvaluationCase,
  type EvaluationJob,
  type EvaluationRun,
  type ExperimentResponse,
  type ResourceRef,
} from '../model/evaluation';

import api from '@/services/client';

export const evaluationApi = {
  createSuite: async (data: { name: string; description?: string; cases: EvaluationCase[] }) => {
    const response = await api.post('/evaluations/suites', { ...data, resource_kind: 'skill' });
    return createSuiteResponseSchema.parse(response.data);
  },
  publishSuite: async (suiteId: string) => {
    const response = await api.post(`/evaluations/suites/${suiteId}/publish`);
    return suiteRevisionSchema.parse(response.data);
  },
  enqueueRun: async (resource: ResourceRef, suiteRevisionId: string, idempotencyKey: string): Promise<EvaluationJob> => {
    const response = await api.post('/evaluations/runs', {
      resource,
      suite_revision_id: suiteRevisionId,
      idempotency_key: idempotencyKey,
    });
    return evaluationJobSchema.parse(response.data);
  },
  getJob: async (jobId: string): Promise<EvaluationJob> => {
    const response = await api.get(`/evaluations/jobs/${jobId}`);
    return evaluationJobSchema.parse(response.data);
  },
  getRun: async (runId: string): Promise<EvaluationRun> => {
    const response = await api.get(`/evaluations/runs/${runId}`);
    return evaluationRunSchema.parse(response.data);
  },
  generateOptimization: async (data: {
    baseline: ResourceRef;
    suiteRevisionId: string;
    searchSpace: Record<string, unknown[]>;
    failureSummaries?: string[];
    idempotencyKey?: string;
  }) => {
    const response = await api.post('/evaluations/optimizations', {
      baseline: data.baseline,
      suite_revision_id: data.suiteRevisionId,
      search_space: data.searchSpace,
      failure_summaries: data.failureSummaries || [],
      idempotency_key: data.idempotencyKey,
    });
    return optimizationResponseSchema.parse(response.data);
  },
  createExperiment: async (stable: ResourceRef, canary: ResourceRef, suiteRevisionId: string): Promise<ExperimentResponse> => {
    const response = await api.post('/evaluations/experiments', {
      stable,
      canary,
      suite_revision_id: suiteRevisionId,
    });
    return experimentResponseSchema.parse(response.data);
  },
  recordFeedback: async (data: {
    traceId: string;
    resourceId: string;
    score: number;
    outcome?: Record<string, unknown>;
    idempotencyKey: string;
  }) => {
    const response = await api.post('/evaluations/feedback', {
      trace_id: data.traceId,
      resource_kind: 'skill',
      resource_id: data.resourceId,
      score: data.score,
      outcome: data.outcome || {},
      idempotency_key: data.idempotencyKey,
    });
    return z.object({ decision: z.string() }).passthrough().parse(response.data);
  },
};
