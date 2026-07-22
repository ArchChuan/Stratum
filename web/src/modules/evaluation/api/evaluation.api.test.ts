import { beforeEach, describe, expect, it, vi } from 'vitest';

import { evaluationApi } from './evaluation.api';

const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }));
vi.mock('@/services/client', () => ({ default: client }));

describe('evaluation center api', () => {
  beforeEach(() => { client.get.mockReset(); client.post.mockReset(); });

  it.each([
    ['getOverview', '/evaluations/overview', undefined, { resources: 0, suites: 0, runs: 0, candidates: 0, experiments: 0 }],
    ['listResources', '/evaluations/resources', { resource_kind: 'agent', status: 'active', cursor: 'next', limit: 20 }, { items: [] }],
    ['listSuites', '/evaluations/suites', { resource_kind: 'skill' }, { items: [] }],
    ['listRuns', '/evaluations/runs', { resource_id: 'resource-1' }, { items: [] }],
    ['listCandidates', '/evaluations/candidates', { status: 'proposed' }, { items: [] }],
    ['listExperiments', '/evaluations/experiments', { status: 'active' }, { items: [] }],
  ] as const)('%s uses the shared client and query params', async (method, path, params, data) => {
    client.get.mockResolvedValue({ data });
    await (evaluationApi[method] as (filters?: unknown) => Promise<unknown>)(params);
    if (params) expect(client.get).toHaveBeenCalledWith(path, { params });
    else expect(client.get).toHaveBeenCalledWith(path);
  });

  it('encodes timeline resource paths and forwards cursors', async () => {
    client.get.mockResolvedValue({ data: { items: [] } });
    await evaluationApi.getTimeline('knowledge', 'space/name', { cursor: 'next', limit: 10 });
    expect(client.get).toHaveBeenCalledWith('/evaluations/resources/knowledge/space%2Fname/timeline', {
      params: { cursor: 'next', limit: 10 },
    });
  });

  it.each([
    ['rejectCandidate', '/evaluations/candidates/candidate-1/reject'],
    ['pauseExperiment', '/evaluations/experiments/candidate-1/pause'],
    ['promoteExperiment', '/evaluations/experiments/candidate-1/promote'],
    ['rollbackExperiment', '/evaluations/experiments/candidate-1/rollback'],
  ] as const)('%s omits actor identity', async (method, path) => {
    const isCandidate = method === 'rejectCandidate';
    client.post.mockResolvedValue({ data: isCandidate ? {
      id: 'candidate-1', resource_id: 'skill-1', revision_id: 'revision-2', parent_revision_id: 'revision-1',
      source: 'optimization', status: 'rejected', resource_kind: 'skill', state_version: 3,
      safe_diff: { changed_fields: [], changes: {}, parent_missing: false }, created_at: '2026-01-01T00:00:00Z',
    } : {
      id: 'candidate-1', resource_kind: 'skill', resource_id: 'skill-1', stable_revision_id: 'stable-1',
      canary_revision_id: 'canary-1', suite_revision_id: 'suite-1', status: 'paused', stage: 5,
      policy: { stages: [5, 20], min_samples: 100, min_observation_minutes: 60, max_cost_regression: 0.1,
        max_latency_regression: 0.2, max_error_rate_increase: 0.01 }, state_version: 3,
      recommendation: 'hold', safety_stopped: false,
    } });
    await (evaluationApi[method] as (id: string, command: unknown) => Promise<unknown>)('candidate-1', {
      reason: '人工复核', idempotency_key: 'request-1', expected_state_version: 2,
    });
    expect(client.post).toHaveBeenCalledWith(path, {
      reason: '人工复核', idempotency_key: 'request-1', expected_state_version: 2,
    });
    expect(client.post.mock.calls[0][1]).not.toHaveProperty('actor_id');
  });

  it.each(['rejectCandidate', 'pauseExperiment', 'promoteExperiment', 'rollbackExperiment'] as const)(
    '%s rejects unexpected sensitive response fields', async (method) => {
      client.post.mockResolvedValue({ data: { id: 'resource-1', status: 'paused', raw_payload: 'secret' } });
      await expect(evaluationApi[method]('resource-1', {
        reason: '人工复核', idempotency_key: 'request-1', expected_state_version: 2,
      })).rejects.toThrow();
    },
  );
});
