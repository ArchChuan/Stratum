import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import { evaluationApi } from '../api/evaluation.api';
import type {
  CandidatePage,
  CenterOverview,
  EvaluationCenterFilters,
  EvaluationCommand,
  ExperimentPage,
  ResourcePage,
  RunPage,
  SuitePage,
} from '../model/evaluation';

import { useAuth } from '@/modules/iam';
import { extractErrorMessage } from '@/shared/lib';

const EMPTY_PAGE = { items: [] };
const MANAGEMENT_ERROR = '仅租户管理员可执行评测命令';

export const useEvaluationCenter = (filters: EvaluationCenterFilters = {}) => {
  const { user } = useAuth();
  const role = user?.role ?? user?.current_tenant?.role ?? 'member';
  const canManageEvaluation = role === 'admin' || role === 'owner';
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
  filtersRef.current = stableFilters;

  const load = useCallback(async () => {
    const generation = requestGenerationRef.current + 1;
    requestGenerationRef.current = generation;
    const requestFilters = filtersRef.current;
    setLoading(true);
    setError('');
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

  return {
    overview, resources, suites, runs, candidates, experiments, loading, error, canManageEvaluation,
    reload: () => load(),
    rejectCandidate: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.rejectCandidate(id, command)),
    pauseExperiment: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.pauseExperiment(id, command)),
    promoteExperiment: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.promoteExperiment(id, command)),
    rollbackExperiment: (id: string, command: EvaluationCommand) => managedCommand(() => evaluationApi.rollbackExperiment(id, command)),
  };
};
