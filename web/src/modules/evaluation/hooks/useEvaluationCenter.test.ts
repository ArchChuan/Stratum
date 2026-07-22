import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { useEvaluationCenter } from './useEvaluationCenter';

const auth = vi.hoisted(() => ({ role: 'member' }));
const api = vi.hoisted(() => ({
  getOverview: vi.fn(), listResources: vi.fn(), listSuites: vi.fn(), listRuns: vi.fn(),
  listCandidates: vi.fn(), listExperiments: vi.fn(), getTimeline: vi.fn(), rejectCandidate: vi.fn(),
  pauseExperiment: vi.fn(), promoteExperiment: vi.fn(), rollbackExperiment: vi.fn(),
}));
vi.mock('@/modules/iam', () => ({ useAuth: () => ({ user: { role: auth.role } }) }));
vi.mock('../api/evaluation.api', () => ({ evaluationApi: api }));

const emptyPage = { items: [] };
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
};
describe('useEvaluationCenter', () => {
  beforeEach(() => {
    auth.role = 'member';
    Object.values(api).forEach((mock) => mock.mockReset());
    api.getOverview.mockResolvedValue({ resources: 0, suites: 0, runs: 0, candidates: 0, experiments: 0 });
    api.listResources.mockResolvedValue(emptyPage); api.listSuites.mockResolvedValue(emptyPage);
    api.listRuns.mockResolvedValue(emptyPage); api.listCandidates.mockResolvedValue(emptyPage);
    api.listExperiments.mockResolvedValue(emptyPage);
  });

  it('loads center data and derives management permission from authenticated role', async () => {
    auth.role = 'admin';
    const { result } = renderHook(() => useEvaluationCenter({ resource_kind: 'skill', resource_id: 'skill-1' }));
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.canManageEvaluation).toBe(true);
    expect(api.listResources).toHaveBeenCalledWith({ resource_kind: 'skill', resource_id: 'skill-1' });
  });

  it('preserves the frozen Chinese API error after failed loading', async () => {
    api.getOverview.mockRejectedValue({ response: { data: { error: '评测资源不存在' } } });
    const { result } = renderHook(() => useEvaluationCenter());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBe('评测资源不存在');
  });

  it('does not update state after unmounting an async effect', async () => {
    let resolveOverview!: (value: unknown) => void;
    api.getOverview.mockReturnValue(new Promise((resolve) => { resolveOverview = resolve; }));
    const { result, unmount } = renderHook(() => useEvaluationCenter());
    unmount();
    await act(async () => { resolveOverview({ resources: 1, suites: 0, runs: 0, candidates: 0, experiments: 0 }); });
    expect(result.current.overview).toBeNull();
  });

  it('refuses commands for members before calling the API', async () => {
    const { result } = renderHook(() => useEvaluationCenter());
    await waitFor(() => expect(result.current.loading).toBe(false));
    await expect(result.current.rejectCandidate('candidate-1', {
      reason: '拒绝', idempotency_key: 'request-1', expected_state_version: 1,
    })).rejects.toThrow('仅租户管理员可执行评测命令');
    expect(api.rejectCandidate).not.toHaveBeenCalled();
  });

  it('ignores a stale filter load that resolves after the latest load', async () => {
    const oldOverview = deferred<any>();
    api.getOverview.mockReturnValueOnce(oldOverview.promise).mockResolvedValueOnce({
      resources: 2, suites: 0, runs: 0, candidates: 0, experiments: 0,
    });
    const { result, rerender } = renderHook(({ id }) => useEvaluationCenter({ resource_id: id }), {
      initialProps: { id: 'resource-a' },
    });
    rerender({ id: 'resource-b' });
    await waitFor(() => expect(result.current.overview?.resources).toBe(2));
    await act(async () => oldOverview.resolve({ resources: 1, suites: 0, runs: 0, candidates: 0, experiments: 0 }));
    expect(result.current.overview?.resources).toBe(2);
  });

  it('keeps the newest command refresh over an overlapping reload and current filters', async () => {
    auth.role = 'admin';
    const { result, rerender } = renderHook(({ id }) => useEvaluationCenter({ resource_id: id }), {
      initialProps: { id: 'resource-a' },
    });
    await waitFor(() => expect(result.current.loading).toBe(false));
    const stale = deferred<any>();
    api.getOverview.mockReturnValueOnce(stale.promise).mockResolvedValue({
      resources: 3, suites: 0, runs: 0, candidates: 0, experiments: 0,
    });
    await act(async () => { void result.current.reload(); rerender({ id: 'resource-b' }); });
    api.rejectCandidate.mockResolvedValue({ id: 'candidate-1' });
    await act(async () => { await result.current.rejectCandidate('candidate-1', {
      reason: '拒绝', idempotency_key: 'request-1', expected_state_version: 1,
    }); });
    await act(async () => stale.resolve({ resources: 1, suites: 0, runs: 0, candidates: 0, experiments: 0 }));
    expect(result.current.overview?.resources).toBe(3);
    expect(api.listResources).toHaveBeenLastCalledWith({ resource_id: 'resource-b' });
  });
});
