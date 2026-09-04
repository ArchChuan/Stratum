import { z } from 'zod';

import {
  createSuiteResponseSchema,
  evaluationJobSchema,
  evaluationRunSchema,
  experimentResponseSchema,
  generateResultSchema,
  optimizationResponseSchema,
  suiteRevisionSchema,
  type EvaluationCase,
  type EvaluationJob,
  type EvaluationRun,
  type ExperimentResponse,
  type ResourceRef,
  type SessionScript,
  candidatePageSchema,
  centerOverviewSchema,
  evaluationCaseSchema,
  evaluationCommandSchema,
  experimentPageSchema,
  resourcePageSchema,
  runPageSchema,
  suitePageSchema,
  timelinePageSchema,
  type EvaluationCenterFilters,
  type EvaluationCommand,
  type GenerateResult,
  type ResourceKind,
  type SuiteRevision,
  resourceRefSchema,
  candidateCommandResponseSchema,
  experimentCommandResponseSchema,
  monitorResourcesPageSchema,
  monitorTrendSchema,
  type MonitorFilters,
  type MonitorResourcesPage,
  type MonitorTrend,
} from '../model/evaluation';

import api from '@/services/client';

export const evaluationApi = {
  createBaseline: async (kind: ResourceKind, resourceId: string): Promise<ResourceRef> => {
    const response = await api.post(`/evaluations/resources/${kind}/${encodeURIComponent(resourceId)}/baseline`);
    return resourceRefSchema.parse(response.data);
  },
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
  listMonitorResources: async (filters?: MonitorFilters): Promise<MonitorResourcesPage> => {
    const response = await api.get('/evaluations/monitoring/resources', filters ? { params: filters } : undefined);
    return monitorResourcesPageSchema.parse(response.data);
  },
  getMonitorTrend: async (filters: MonitorFilters): Promise<MonitorTrend> => {
    const response = await api.get('/evaluations/monitoring/resources/trend', { params: filters });
    return monitorTrendSchema.parse(response.data);
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
  generateSuiteCases: async (suiteId: string, data: { samplePolicy: 'negative_first' | 'balanced'; maxCases?: number }): Promise<GenerateResult> => {
    const { samplePolicy, maxCases } = data;
    const response = await api.post(`/evaluations/suites/${suiteId}/generate`, {
      sample_policy: samplePolicy,
      max_cases: maxCases,
    });
    return generateResultSchema.parse(response.data);
  },
  getSuiteDraft: async (suiteId: string): Promise<SuiteRevision> => {
    const response = await api.get(`/evaluations/suites/${suiteId}/draft`);
    return suiteRevisionSchema.parse(response.data);
  },
  // updateDraftCase 更新单个草稿用例。会话剧本 case 携带 session（完整替换剧本，
  // 后端对 nil Session 写回 '{}' 语义回退单轮）；input 仅在单轮形态出现，会话形态
  // 省略（undefined 键会被 JSON.stringify 丢弃）。
  updateDraftCase: async (suiteId: string, caseId: string, data: {
    name: string; input?: unknown; expectedOutput: unknown;
    assertionMode: 'exact' | 'contains' | 'regex' | 'judge'; enabled: boolean; session?: SessionScript;
  }): Promise<EvaluationCase> => {
    const { expectedOutput, assertionMode, session, ...body } = data;
    const payload: Record<string, unknown> = { ...body, expected_output: expectedOutput, assertion_mode: assertionMode };
    if (session) payload.session = session;
    const response = await api.put(`/evaluations/suites/${suiteId}/draft/cases/${caseId}`, payload);
    return evaluationCaseSchema.parse(response.data);
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
  // 删除（RBAC：owner 恒可删 / 创建者可删 / 其余 403；204 空体）。后端在
  // requireAdmin 之上再做资源级 owner-or-creator 门禁（fail-closed）。
  deleteSuite: async (suiteId: string) => { await api.delete(`/evaluations/suites/${encodeURIComponent(suiteId)}`); },
  deleteRun: async (runId: string) => { await api.delete(`/evaluations/runs/${encodeURIComponent(runId)}`); },
  deleteJob: async (jobId: string) => { await api.delete(`/evaluations/jobs/${encodeURIComponent(jobId)}`); },
  deleteExperiment: async (experimentId: string) => { await api.delete(`/evaluations/experiments/${encodeURIComponent(experimentId)}`); },
  deleteCandidate: async (candidateId: string) => { await api.delete(`/evaluations/candidates/${encodeURIComponent(candidateId)}`); },
  deleteReviewItem: async (reviewId: string) => { await api.delete(`/evaluations/review/${encodeURIComponent(reviewId)}`); },
  deleteFeedback: async (feedbackId: string) => { await api.delete(`/evaluations/feedback/${encodeURIComponent(feedbackId)}`); },
};
