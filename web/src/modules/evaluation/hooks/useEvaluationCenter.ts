import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { evaluationApi } from '../api/evaluation.api';
import type {
  CandidatePage,
  CenterOverview,
  EvaluationCenterFilters,
  EvaluationCommand,
  EvaluationCase,
  EvaluationJob,
  ExperimentPage,
  ResourcePage,
  RunPage,
  SuitePage,
  ResourceRef,
} from '../model/evaluation';

import { useAuth, useTenantRole } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';
import { createIdempotencyKey } from '@/shared/lib/idempotencyKey';

const EMPTY_PAGE = { items: [] };
const MANAGEMENT_ERROR = '仅租户管理员可执行评测命令';

export const useEvaluationCenter = (filters: EvaluationCenterFilters = {}) => {
  const { user } = useAuth();
  // 统一走 useTenantRole 读取有效租户角色：平台管理员归一化为 admin（与后端
  // EffectiveTenantRole 一致），owner 保留。canManageEvaluation = isAdmin。
  const { isAdmin, isOwner } = useTenantRole();
  const userId = user?.id;
  const canManageEvaluation = isAdmin;
  // 删除可见性：租户 owner 恒可删；资源创建者（created_by 等于当前用户）可删；
  // 其余（admin 非创建者 / member）不可删——与后端 DeleteService.authorize 一致。
  const canDeleteEntity = useCallback((createdBy?: string) => (
    isOwner || (!!createdBy && createdBy === userId)
  ), [isOwner, userId]);
  const { cursor, limit, resource_id, resource_kind, status } = filters;
  const stableFilters = useMemo(() => {
    const value: EvaluationCenterFilters = {};
    if (cursor !== undefined) value.cursor = cursor;
    if (limit !== undefined) value.limit = limit;
    if (resource_id !== undefined) value.resource_id = resource_id;
    if (resource_kind !== undefined) value.resource_kind = resource_kind;
    if (status !== undefined) value.status = status;
    return value;
  }, [cursor, limit, resource_id, resource_kind, status]);
  const [overview, setOverview] = useState<CenterOverview | null>(null);
  const [resources, setResources] = useState<ResourcePage>(EMPTY_PAGE);
  const [suites, setSuites] = useState<SuitePage>(EMPTY_PAGE);
  const [runs, setRuns] = useState<RunPage>(EMPTY_PAGE);
  const [candidates, setCandidates] = useState<CandidatePage>(EMPTY_PAGE);
  const [experiments, setExperiments] = useState<ExperimentPage>(EMPTY_PAGE);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const filtersRef = useRef(stableFilters);
  const requestGenerationRef = useRef(0);
  const mountedRef = useRef(true);
  const createWorkflowRef = useRef<{
    fingerprint: string; suiteID?: string; publishedRevisionID?: string; idempotencyKey: string;
    inFlight?: Promise<EvaluationJob>;
  }>();
  filtersRef.current = stableFilters;

  const load = useCallback(async () => {
    if (!mountedRef.current) return;
    const generation = requestGenerationRef.current + 1;
    requestGenerationRef.current = generation;
    const requestFilters = filtersRef.current;
    setLoading(true);
    setError('');
    setOverview(null);
    setResources(EMPTY_PAGE);
    setSuites(EMPTY_PAGE);
    setRuns(EMPTY_PAGE);
    setCandidates(EMPTY_PAGE);
    setExperiments(EMPTY_PAGE);
    try {
      const values = await Promise.all([
        evaluationApi.getOverview(), evaluationApi.listResources(requestFilters), evaluationApi.listSuites(requestFilters),
        evaluationApi.listRuns(requestFilters), evaluationApi.listCandidates(requestFilters),
        evaluationApi.listExperiments(requestFilters),
      ]);
      if (!mountedRef.current || generation !== requestGenerationRef.current) return;
      setOverview(values[0]); setResources(values[1]); setSuites(values[2]);
      setRuns(values[3]); setCandidates(values[4]); setExperiments(values[5]);
    } catch (err) {
      if (mountedRef.current && generation === requestGenerationRef.current) {
        setError(extractErrorMessage(err) || '加载评测与进化中心失败');
      }
    } finally {
      if (mountedRef.current && generation === requestGenerationRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    return () => { mountedRef.current = false; requestGenerationRef.current += 1; };
  }, []);

  useEffect(() => { void load(); }, [load, stableFilters]);

  const managedCommand = useCallback(async <T,>(operation: () => Promise<T>) => {
    if (!canManageEvaluation) throw new Error(MANAGEMENT_ERROR);
    const result = await operation();
    await load();
    return result;
  }, [canManageEvaluation, load]);

  const resetCreateEvaluation = useCallback(() => { createWorkflowRef.current = undefined; }, []);
  const createEvaluation = useCallback((data: {
    resource: ResourceRef; name: string; description?: string; cases: EvaluationCase[];
  }) => {
    if (!canManageEvaluation) return Promise.reject(new Error(MANAGEMENT_ERROR));
    const fingerprint = JSON.stringify(data);
    let workflow = createWorkflowRef.current;
    if (!workflow || workflow.fingerprint !== fingerprint) {
      workflow = { fingerprint, idempotencyKey: createIdempotencyKey() };
      createWorkflowRef.current = workflow;
    }
    if (workflow.inFlight) return workflow.inFlight;
    const current = workflow;
    current.inFlight = (async () => {
      if (!current.suiteID) {
        const created = await evaluationApi.createSuite({ name: data.name, description: data.description,
          resourceKind: data.resource.kind, cases: data.cases });
        current.suiteID = created.suite.id;
      }
      if (!current.publishedRevisionID) {
        const published = await evaluationApi.publishSuite(current.suiteID);
        current.publishedRevisionID = published.id;
      }
      const job = await evaluationApi.enqueueRun(data.resource, current.publishedRevisionID, current.idempotencyKey);
      await load();
      if (createWorkflowRef.current === current) resetCreateEvaluation();
      return job;
    })().finally(() => { current.inFlight = undefined; });
    return current.inFlight;
  }, [canManageEvaluation, load, resetCreateEvaluation]);

  return {
    overview, resources, suites, runs, candidates, experiments, loading, error, canManageEvaluation,
    userId, isOwner, canDeleteEntity,
    reload: () => load(),
    createEvaluation, resetCreateEvaluation,
    rejectCandidate: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.rejectCandidate(id, command)),
    pauseExperiment: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.pauseExperiment(id, command)),
    promoteExperiment: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.promoteExperiment(id, command)),
    rollbackExperiment: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.rollbackExperiment(id, command)),
    deleteSuite: (id: string) => managedCommand(() => evaluationApi.deleteSuite(id)),
    deleteRun: (id: string) => managedCommand(() => evaluationApi.deleteRun(id)),
    deleteJob: (id: string) => managedCommand(() => evaluationApi.deleteJob(id)),
    deleteExperiment: (id: string) => managedCommand(() => evaluationApi.deleteExperiment(id)),
    deleteCandidate: (id: string) => managedCommand(() => evaluationApi.deleteCandidate(id)),
    deleteReviewItem: (id: string) => managedCommand(() => evaluationApi.deleteReviewItem(id)),
    deleteFeedback: (id: string) => managedCommand(() => evaluationApi.deleteFeedback(id)),
  };
};
