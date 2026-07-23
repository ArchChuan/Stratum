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
  candidatePageSchema,
  centerOverviewSchema,
  evaluationCommandSchema,
  experimentPageSchema,
  resourcePageSchema,
  runPageSchema,
  suitePageSchema,
  timelinePageSchema,
  type EvaluationCenterFilters,
  type EvaluationCommand,
  type ResourceKind,
  candidateCommandResponseSchema,
  experimentCommandResponseSchema,
} from '../model/evaluation';

import api from '@/services/client';

export const evaluationApi = {
  getOverview: async () => {
    const response = await api.get('/evaluations/overview');
    return centerOverviewSchema.parse(response.data);
  },
  listResources: async (filters?: EvaluationCenterFilters) => {
    const response = await api.get('/evaluations/resources', filters ? { params: filters } : undefined);
    return resourcePageSchema.parse(response.data);
  },
  listSuites: async (filters?: EvaluationCenterFilters) => {
    const response = await api.get('/evaluations/suites', filters ? { params: filters } : undefined);
    return suitePageSchema.parse(response.data);
  },
  listRuns: async (filters?: EvaluationCenterFilters) => {
    const response = await api.get('/evaluations/runs', filters ? { params: filters } : undefined);
    return runPageSchema.parse(response.data);
  },
  listCandidates: async (filters?: EvaluationCenterFilters) => {
    const response = await api.get('/evaluations/candidates', filters ? { params: filters } : undefined);
    return candidatePageSchema.parse(response.data);
  },
  listExperiments: async (filters?: EvaluationCenterFilters) => {
    const response = await api.get('/evaluations/experiments', filters ? { params: filters } : undefined);
    return experimentPageSchema.parse(response.data);
  },
  getTimeline: async (kind: ResourceKind, resourceId: string, filters?: Pick<EvaluationCenterFilters, 'status' | 'cursor' | 'limit'>) => {
    const path = `/evaluations/resources/${kind}/${encodeURIComponent(resourceId)}/timeline`;
    const response = await api.get(path, filters ? { params: filters } : undefined);
    return timelinePageSchema.parse(response.data);
  },
  createSuite: async (data: { name: string; description?: string; resourceKind: ResourceKind; cases: EvaluationCase[] }) => {
    const { resourceKind, ...body } = data;
    const response = await api.post('/evaluations/suites', { ...body, resource_kind: resourceKind });
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
  rejectCandidate: async (candidateId: string, command: EvaluationCommand) => {
    const response = await api.post(`/evaluations/candidates/${encodeURIComponent(candidateId)}/reject`,
      evaluationCommandSchema.parse(command));
    return candidateCommandResponseSchema.parse(response.data);
  },
  pauseExperiment: async (experimentId: string, command: EvaluationCommand) => {
    const response = await api.post(`/evaluations/experiments/${encodeURIComponent(experimentId)}/pause`,
      evaluationCommandSchema.parse(command));
    return experimentCommandResponseSchema.parse(response.data);
  },
  promoteExperiment: async (experimentId: string, command: EvaluationCommand) => {
    const response = await api.post(`/evaluations/experiments/${encodeURIComponent(experimentId)}/promote`,
      evaluationCommandSchema.parse(command));
    return experimentCommandResponseSchema.parse(response.data);
  },
  rollbackExperiment: async (experimentId: string, command: EvaluationCommand) => {
    const response = await api.post(`/evaluations/experiments/${encodeURIComponent(experimentId)}/rollback`,
      evaluationCommandSchema.parse(command));
    return experimentCommandResponseSchema.parse(response.data);
  },
};
